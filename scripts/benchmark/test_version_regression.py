import tempfile
from pathlib import Path
import unittest
import version_regression as benchmark


class VersionRegressionBenchmarkTests(unittest.TestCase):
    def test_real_analyzer_covers_every_scenario_kind(self):
        result = benchmark.run(repeats=1)
        self.assertEqual(result['summary']['total_tasks'], 10)
        self.assertEqual(result['summary']['passed_tasks'], 10, result['scenarios'])
        self.assertEqual(result['summary']['unmatched_refusal_rate'], 1)

    def test_failed_tasks_remain_in_denominator(self):
        def failure(*args):
            raise RuntimeError('injected benchmark task failure')
        result = benchmark.run(repeats=1, analyzer=failure)
        summary = result['summary']
        self.assertEqual(summary['failed_tasks'], 10)
        self.assertEqual(summary['quality_all_tasks'], 0)
        self.assertEqual(summary['evidence_correct_rate_all_tasks'], 0)
        self.assertEqual(summary['latency_ms']['samples'], 10)
        self.assertEqual(sum(row['failed'] for row in summary['confusion_matrix'].values()), 10)

    def test_seed_reproduces_independent_inputs(self):
        self.assertEqual(benchmark.make_scenarios(42, 1), benchmark.make_scenarios(42, 1))
        self.assertNotEqual(benchmark.make_scenarios(42, 1), benchmark.make_scenarios(43, 1))

    def test_bad_evidence_cannot_pass(self):
        def corrupt(rows, before, after):
            report = benchmark.regression.analyze(rows, before, after)
            report['evidence'][0]['source'] = 'fabricated.csv'
            return report
        case = benchmark.evaluate_case(benchmark.make_scenarios(repeats=1)[0], analyzer=corrupt)
        self.assertFalse(case['passed'])
        self.assertFalse(case['evidence_valid'])

    def test_fixtures_written_and_parseable(self):
        with tempfile.TemporaryDirectory() as directory:
            scenario = benchmark.make_scenarios(repeats=1)[0]
            case = benchmark.evaluate_case(scenario, fixtures_dir=directory)
            self.assertTrue(case['passed'])
            path = Path(directory) / scenario['id'] / 'baseline.csv'
            rows = benchmark.regression.parse_file(path, 'fixture')
            self.assertEqual(len(rows), len(scenario['baseline']))


if __name__ == '__main__':
    unittest.main()
