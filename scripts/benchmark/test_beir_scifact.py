import json
import tempfile
import unittest
from pathlib import Path
from beir_scifact import score_rows, verify_dataset, download_dataset
class BenchmarkMetricsTests(unittest.TestCase):
    def test_partial_ranking_and_empty_query_count_in_denominator(self):
        qrels={"q1":{"gold":1},"q2":{"other":1}}
        rows=[{"query_id":"q1","hits":[{"corpus_id":"bad","rank_derived_eval_score":10},{"corpus_id":"gold","rank_derived_eval_score":9}],"latency_ms":1},{"query_id":"q2","hits":[],"error":"timeout","latency_ms":2}]
        metrics,per_query=score_rows(qrels,rows)
        self.assertEqual(metrics["query_count"],2)
        self.assertEqual(metrics["failure_count"],1)
        self.assertAlmostEqual(metrics["Recall@3"],0.5)
        self.assertAlmostEqual(metrics["P@3"],1/6)
        self.assertAlmostEqual(metrics["MRR@3"],0.25)
        self.assertEqual(per_query["q2"]["recall_3"],0)
    def test_missing_query_is_rejected_instead_of_silently_improving_metrics(self):
        with self.assertRaises(ValueError):score_rows({"q1":{"a":1},"q2":{"b":1}},[{"query_id":"q1","hits":[]}])
    def test_duplicate_results_rejected(self):
        with self.assertRaises(ValueError):score_rows({"q":{"a":1}},[{"query_id":"q","hits":[{"corpus_id":"a","rank_derived_eval_score":10},{"corpus_id":"a","rank_derived_eval_score":9}]}])
    def test_dataset_checks_judged_doc_exists(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);(root/"qrels").mkdir()
            (root/"corpus.jsonl").write_text('{"_id":"doc","title":"","text":"body"}\n')
            (root/"queries.jsonl").write_text('{"_id":"q","text":"claim"}\n')
            (root/"qrels/test.tsv").write_text('query-id\tcorpus-id\tscore\nq\tmissing\t1\n')
            with self.assertRaises(ValueError):verify_dataset(root,expected_corpus=1,expected_queries=1)

class LatencyAccountingTests(unittest.TestCase):
    def test_missing_latency_is_reported_and_excluded_not_zero(self):
        metrics,_=score_rows({'q1':{'a':1},'q2':{'b':1}},[{'query_id':'q1','hits':[],'latency_ms':10},{'query_id':'q2','hits':[]}])
        self.assertEqual(metrics['latency_ms']['sample_count'],1)
        self.assertEqual(metrics['latency_ms']['missing_count'],1)
        self.assertEqual(metrics['latency_ms']['mean'],10)
    def test_nonfinite_or_negative_latency_is_rejected(self):
        for value in [float('nan'),float('inf'),-1]:
            with self.assertRaises(ValueError):score_rows({'q':{'a':1}},[{'query_id':'q','hits':[],'latency_ms':value}])

class ArtifactPreservationTests(unittest.TestCase):
    def test_score_does_not_overwrite_either_output(self):
        from beir_scifact import write_score_outputs
        for existing in ['metrics.json','metrics-per-query.json']:
            with tempfile.TemporaryDirectory() as temp:
                out=Path(temp)/'metrics.json';p=Path(temp)/existing;p.write_bytes(b'historical bytes\n')
                with self.assertRaises(FileExistsError):write_score_outputs(out,{}, {})
                self.assertEqual(p.read_bytes(),b'historical bytes\n')
                self.assertEqual(len(list(Path(temp).iterdir())),1)
    def test_offline_baseline_refuses_existing_raw_output(self):
        from beir_scifact import run_baseline
        with tempfile.TemporaryDirectory() as temp:
            out=Path(temp)/'baseline.jsonl';out.write_bytes(b'historical raw\n')
            with self.assertRaises(FileExistsError):run_baseline(Path(temp)/'missing-dataset',out)
            self.assertEqual(out.read_bytes(),b'historical raw\n')

if __name__=="__main__":unittest.main()
