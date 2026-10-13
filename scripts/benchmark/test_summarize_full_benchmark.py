import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('summary', pathlib.Path(__file__).with_name('summarize_full_benchmark.py'))
s = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(s)


class SummaryTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name)

    def row(self, case, **extra):
        row = {'scenario_id': str(case), 'trial': 1, 'valid_report': True, 'latency_ms': 100,
               'provider_stats': {'Attempts': 2, 'Retries': 1, 'Rejected': 0, 'Tokens': 30},
               'result': {'model_steps': 2, 'tool_trace': []}, **extra}
        path = self.root / f'scenario-{case}/{row["trial"]}/outputs/argus_run.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(row))
        return path

    def scores(self, rows):
        path = self.root / 'strict.json'
        path.write_text(json.dumps({'rows': rows}))
        return path

    def score(self, case, hit=0, f1=0, matched=None):
        return {'incident_id': str(case), 'trial': '1', 'hit_at_1': hit,
                'precision': f1, 'recall': f1, 'f1': f1, 'matched_root_groups': matched or []}

    def test_failure_checkpoint_tools_all_task_denominators(self):
        self.row(1, trace_id='ab'*16, result={'model_steps': 2, 'tool_trace': [
            {'tool': 'search_history', 'output': {'evidence': [], 'unavailable': True}},
            {'tool': 'read_logs', 'output': {'evidence': [{'id': 'log:1'}]}}]})
        self.row(2, valid_report=False, latency_ms=500, result=None, checkpoint={'Step': 3, 'Observations': [
            {'tool': 'search_similar_cases', 'output': {'evidence': [{'id': 'h:1'}]}},
            {'tool': 'read_logs', 'error': 'failed', 'output': {'error': 'failed'}}]})
        result = s.summarize(self.root, self.scores([self.score(1, 1, 1, ['root']), self.score(2)]))
        self.assertEqual(result['tasks_total'], 2)
        self.assertEqual(result['valid_reports']['rate'], .5)
        self.assertEqual(result['strict']['hit_at_1']['rate'], .5)
        self.assertEqual(result['strict']['any_gold_hit']['rate'], .5)
        self.assertEqual(result['strict']['f1']['mean_all_tasks'], .5)
        self.assertEqual(result['tool_bearing_tasks']['rate'], 1)
        self.assertEqual(result['retrieval']['task_use']['rate'], 1)
        self.assertEqual(result['retrieval']['nonempty_calls']['rate'], .5)
        self.assertEqual(result['retrieval']['unavailable_calls']['rate'], .5)
        self.assertEqual(result['tools']['read_logs']['error_calls']['numerator'], 1)
        self.assertEqual(result['performance']['model_steps']['total'], 5)
        self.assertEqual(result['performance']['latency_ms']['p95_nearest_rank'], 500)
        self.assertIsNone(result['performance']['http_429']['total'])
        self.assertEqual(result['traceable_tasks']['rate'], .5)
        self.assertEqual(result['tasks'][1]['observation_source'], 'checkpoint.Observations')
        self.assertEqual(len(result['tasks'][1]['source']['sha256']), 64)

    def test_valid_report_is_not_root_cause_success_and_missing_not_zero(self):
        self.row(1)
        self.row(2)
        result = s.summarize(self.root, self.scores([self.score(1)]))
        self.assertEqual(result['valid_reports']['rate'], 1)
        self.assertEqual(result['strict']['hit_at_1']['rate'], 0)
        self.assertEqual(result['strict']['coverage']['numerator'], 1)
        self.assertEqual(result['strict']['hit_at_1']['interpretation'], 'lower_bound_missing_scores')
        self.assertIsNone(result['strict']['f1']['mean_all_tasks'])
        self.assertEqual(result['strict']['f1']['mean_observed'], 0)

    def test_duplicates_or_unmatched_scores_rejected(self):
        self.row(1)
        path = self.row(2)
        row = json.loads(path.read_text())
        row['scenario_id'] = '1'
        path.write_text(json.dumps(row))
        with self.assertRaisesRegex(ValueError, 'duplicate task'):
            s.summarize(self.root, self.scores([self.score(1)]))
        path.unlink()
        with self.assertRaisesRegex(ValueError, 'duplicate strict'):
            s.summarize(self.root, self.scores([self.score(1), self.score(1)]))
        with self.assertRaisesRegex(ValueError, 'without task'):
            s.summarize(self.root, self.scores([self.score(3)]))

    def test_known_nan_rejected_unknown_metric_not_promoted(self):
        self.row(1)
        row = self.score(1)
        row['new_metric'] = 1000
        result = s.summarize(self.root, self.scores([row]))
        self.assertNotIn('new_metric', result['strict'])
        row['f1'] = float('nan')
        with self.assertRaisesRegex(ValueError, 'finite'):
            s.summarize(self.root, self.scores([row]))

    def test_observations_not_double_counted_and_http_status_coverage(self):
        obs = {'tool': 'search_history', 'output': json.dumps({'evidence': [{'id': 'h'}]})}
        self.row(1, result={'model_steps': 2, 'tool_trace': [obs]}, checkpoint={'Observations': [obs]},
                 request_tool_policy=[{'status_code': 429}, {'status_code': 200}])
        result = s.summarize(self.root, self.scores([self.score(1)]))
        self.assertEqual(result['retrieval']['calls'], 1)
        self.assertEqual(result['performance']['http_429']['total'], 1)
        self.assertEqual(result['performance']['http_status_coverage']['numerator'], 2)

    def test_official_only_explicit_metric_not_every_nested_number(self):
        self.row(1)
        official = self.root / 'official.json'
        official.write_text(json.dumps({'raw_results': [{'incident_id': '1', 'trial': 1,
            'scores': {'root_cause_entity': {'calculation_f1': .2}, 'debug': {'arbitrary': 99}}}]}))
        result = s.summarize(self.root, self.scores([self.score(1)]), official)
        self.assertEqual(result['official']['root_cause_entity.calculation_f1']['mean_all_tasks'], .2)
        self.assertNotIn('debug.arbitrary', result['official'])

    def test_partial_http_capture_is_unknown_transport_failure_is_observed(self):
        self.row(1, request_tool_policy=[{'http_status': 429}])
        result = s.summarize(self.root, self.scores([self.score(1)]))
        self.assertIsNone(result['performance']['http_429']['total'])
        self.assertEqual(result['performance']['http_429']['observed_count'], 1)
        self.assertEqual(result['performance']['http_attempt_capture_coverage']['rate'], .5)
        self.row(1, request_tool_policy=[{'http_status': 429}, {'transport_error': True}])
        result = s.summarize(self.root, self.scores([self.score(1)]))
        self.assertEqual(result['performance']['http_429']['total'], 1)
        self.assertEqual(result['performance']['http_transport_errors']['total'], 1)
        self.assertEqual(result['performance']['http_status_coverage']['rate'], .5)

    def test_nonfinite_latency_rejected_and_expected_case_count_checked(self):
        self.row(1, latency_ms=float('inf'))
        with self.assertRaisesRegex(ValueError, 'finite'):
            s.summarize(self.root, self.scores([self.score(1)]))
        self.row(1)
        with self.assertRaisesRegex(ValueError, 'expected 35 cases'):
            s.summarize(self.root, self.scores([self.score(1)]), expected_cases=35)

    def test_cli_writes_auditable_json_without_nan(self):
        self.row(1)
        scores = self.scores([self.score(1)])
        output = self.root / 'summary.json'
        process = subprocess.run([sys.executable, s.__file__, '--runs', str(self.root),
            '--strict-scores', str(scores), '--expected-cases', '1', '--out', str(output)],
            text=True, capture_output=True)
        self.assertEqual(process.returncode, 0, process.stderr)
        report = json.loads(output.read_text())
        self.assertEqual(report['tasks_total'], 1)
        self.assertEqual(report['tasks'][0]['case'], '1')
        self.assertEqual(report['tasks'][0]['strict_source']['path'], str(scores.resolve()))

    def test_expected_trials_require_exact_zero_based_set_per_case(self):
        self.row(1, trial=0)
        self.row(1, trial=1)
        score0 = {**self.score(1), 'trial': '0'}
        score2 = {**self.score(1), 'trial': '2'}
        score3 = {**self.score(1), 'trial': '3'}
        scores = self.scores([score0, self.score(1)])
        with self.assertRaisesRegex(ValueError, 'trial set.*missing.*2'):
            s.summarize(self.root, scores, expected_trials_per_case=3)
        self.row(1, trial=2)
        scores = self.scores([score0, self.score(1), score2])
        result = s.summarize(self.root, scores, expected_cases=1, expected_trials_per_case=3)
        self.assertEqual(result['tasks_total'], 3)
        self.row(1, trial=3)
        scores = self.scores([score0, self.score(1), score2, score3])
        with self.assertRaisesRegex(ValueError, 'trial set.*extra.*3'):
            s.summarize(self.root, scores, expected_trials_per_case=3)

    def test_expected_trials_cli_rejects_missing_even_when_case_count_matches(self):
        self.row(1, trial=0)
        scores = self.scores([{**self.score(1), 'trial': '0'}])
        process = subprocess.run([sys.executable, s.__file__, '--runs', str(self.root),
            '--strict-scores', str(scores), '--expected-cases', '1',
            '--expected-trials-per-case', '3'], text=True, capture_output=True)
        self.assertNotEqual(process.returncode, 0)
        self.assertIn('trial set', process.stderr)

    def test_explicit_unscorable_gt_row_preserved_not_zeroed(self):
        self.row(1)
        self.row(38)
        unknown = {**self.score(38), 'score_valid': False, 'matched_root_groups': None,
                   'errors': [{'group_id': 'root', 'filter': '*.*', 'error': 'nothing to repeat'}]}
        for field in ['hit_at_1', 'precision', 'recall', 'f1']:
            unknown[field] = None
        result = s.summarize(self.root, self.scores([self.score(1, 1, 1, ['root']), unknown]))
        self.assertEqual(result['strict']['rows_coverage']['numerator'], 2)
        self.assertEqual(result['strict']['coverage']['numerator'], 1)
        self.assertEqual(result['strict']['hit_at_1']['rate'], .5)
        self.assertIsNone(result['strict']['f1']['mean_all_tasks'])
        self.assertEqual(result['strict']['f1']['mean_observed'], 1)
        self.assertEqual(result['strict']['unknown_scores'][0]['case'], '38')
        self.assertEqual(result['tasks'][1]['strict']['errors'][0]['filter'], '*.*')
        unknown.pop('score_valid')
        with self.assertRaises(ValueError):
            s.summarize(self.root, self.scores([self.score(1), unknown]))

    def test_unscorable_marker_requires_null_metrics_and_error(self):
        self.row(1)
        unknown = {**self.score(1), 'score_valid': False}
        with self.assertRaisesRegex(ValueError, 'null'):
            s.summarize(self.root, self.scores([unknown]))
        for field in ['hit_at_1', 'precision', 'recall', 'f1']:
            unknown[field] = None
        with self.assertRaisesRegex(ValueError, 'error'):
            s.summarize(self.root, self.scores([unknown]))


if __name__ == '__main__':
    unittest.main()
