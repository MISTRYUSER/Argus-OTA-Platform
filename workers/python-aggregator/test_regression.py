import csv
import importlib.util
import io
import math
from pathlib import Path
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('regression.py')

class RegressionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location('regression', SCRIPT)
        cls.r = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.r)

    def parse(self, **changes):
        row = dict(timestamp='2026-10-01T00:00:00Z', version='v1', vehicle_id='V1', vehicle_class='truck', module='planner', replay_id='R1', workload='AD', error_code='', cpu='20', memory_mb='100')
        row.update(changes)
        f = io.StringIO(); w = csv.DictWriter(f, fieldnames=row); w.writeheader(); w.writerow(row)
        return self.r.parse_csv(io.StringIO(f.getvalue()), 'minio:source.csv')

    def rows(self, workload='AD'):
        old = self.parse()[0]; new = self.parse(version='v2', cpu='40', memory_mb='200', error_code='E42', workload=workload)[0]
        return [dict(old, timestamp=old['timestamp']+i, line=i+2) for i in range(10)] + [dict(new, timestamp=new['timestamp']+i, line=i+12) for i in range(10)]

    def test_matched_replay_reports_deltas_and_source_evidence(self):
        d = self.r.analyze(self.rows(), 'v1', 'v2')
        self.assertEqual(d['status'], 'comparable')
        g=d['comparisons'][0]
        self.assertEqual(g['cpu']['delta'],20)
        self.assertEqual(g['error_rate']['delta'],1)
        self.assertEqual(g['new_error_codes'],['E42'])
        self.assertEqual(g['cpu']['candidate']['p95'],40)
        self.assertEqual(d['candidates'][0]['causal_status'],'unproven')
        self.assertTrue(all(e['source']=='minio:source.csv' and e['line']>=2 for e in d['evidence']))
        self.assertTrue(d['candidates'][0]['verification'])

    def test_different_workload_is_not_version_regression(self):
        d=self.r.analyze(self.rows('MAN'), 'v1','v2')
        self.assertEqual(d['status'],'not_comparable'); self.assertEqual(d['candidates'],[])
        self.assertEqual(d['matched_samples'],0)

    def test_missing_metric_stays_missing(self):
        rows=self.rows()
        for r in rows[10:]: r['memory_mb']=None
        d=self.r.analyze(rows,'v1','v2'); m=d['comparisons'][0]['memory_mb']
        self.assertIsNone(m['delta']); self.assertIsNone(m['candidate']['mean'])
        self.assertTrue(d['limitations'])

    def test_zero_baseline_has_no_infinite_percentage(self):
        rows=self.rows()
        for r in rows[:10]: r['cpu']=0
        g=self.r.analyze(rows,'v1','v2')['comparisons'][0]
        self.assertIsNone(g['cpu']['relative_change']); self.assertEqual(g['cpu']['delta'],40)

    def test_reject_nonfinite_negative_and_invalid_time(self):
        for changes in [dict(cpu='nan'),dict(memory_mb='-1'),dict(cpu='inf'),dict(timestamp='garbage'),dict(replay_id=''),dict(version='')]:
            with self.subTest(changes=changes), self.assertRaises(ValueError): self.parse(**changes)

    def test_window_and_sampling_must_match(self):
        rows=self.rows(); rows[-1]['timestamp']+=200
        d=self.r.analyze(rows,'v1','v2')
        self.assertEqual(d['status'],'not_comparable')
        self.assertTrue(any('duration' in x for x in d['limitations']))

    def test_low_sample_count_never_produces_cause(self):
        d=self.r.analyze(self.rows()[:1]+self.rows()[10:11],'v1','v2')
        self.assertEqual(d['status'],'not_comparable'); self.assertEqual(d['candidates'],[])

    def test_no_match_hardware_or_vehicle(self):
        rows=self.rows()
        for r in rows[10:]: r['vehicle_class']='sedan'
        self.assertEqual(self.r.analyze(rows,'v1','v2')['status'],'not_comparable')

    def test_unobserved_errors_remain_unknown(self):
        rows=self.rows()
        for row in rows: row["raw"]={"error_observed":False}
        d=self.r.analyze(rows,"v1","v2")
        self.assertIsNone(d["comparisons"][0]["error_rate"]["delta"])
        self.assertIsNone(d["comparisons"][0]["error_rate"]["candidate"]["mean"])

    def test_same_or_unknown_versions_rejected(self):
        for a,b in [('v1','v1'),('v1','v3')]:
            with self.assertRaises(ValueError): self.r.analyze(self.rows(),a,b)

    def test_distinct_messages_at_identical_timestamp_are_allowed(self):
        rows=self.rows()
        rows.append(dict(rows[-1],line=99,error_code="OTHER_EVENT"))
        self.assertIsNotNone(self.r.analyze(rows,"v1","v2"))

    def test_dense_replay_half_sample_offset_is_rejected(self):
        base=self.rows()
        rows=[dict(base[0],line=i+2,timestamp=1000+i*.01) for i in range(100)]+[dict(base[10],line=i+102,timestamp=1000+i*.01) for i in range(100)]
        for r in rows[101:199]:r["timestamp"]+=.0049
        self.assertEqual(self.r.analyze(rows,"v1","v2")["status"],"not_comparable")

    def test_duplicate_observations_rejected(self):
        rows=self.rows(); rows.append(dict(rows[0]))
        with self.assertRaises(ValueError): self.r.analyze(rows,'v1','v2')

    def test_maximum_rows_bounded(self):
        with self.assertRaises(ValueError): self.r.parse_csv(io.StringIO('timestamp,version\n'), 'f', max_rows=0)

    def test_csv_preserves_explicit_ota_evidence(self):
        row=self.parse(error_code="OTA_CHECKSUM_MISMATCH",error_type="OTA_CHECKSUM_MISMATCH",expected_checksum="abc",actual_checksum="def")[0]
        self.assertEqual(row["error_type"],"OTA_CHECKSUM_MISMATCH")
        self.assertEqual(row["expected_checksum"],"abc")

    def test_private_rec_dependency_absent_fails_explicitly(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'unknown.rec'; p.write_bytes(b'not a public format')
            with self.assertRaises(ValueError): self.r.parse_file(p,'f')

if __name__=='__main__': unittest.main()
