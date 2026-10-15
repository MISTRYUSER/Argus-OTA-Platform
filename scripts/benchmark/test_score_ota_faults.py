import unittest
from score_ota_faults import score_attempt, summarize_mode, score_runs


class ScoreOTATests(unittest.TestCase):
    def setUp(self):
        self.case = {'id': '1001', 'sha256': 'abc', 'lines': ['a', 'b', 'c']}
        self.label = {'id': '1001', 'input_sha256': 'abc', 'cause_code': 'client_timeout', 'key_evidence_groups': [['log:1'], ['log:2']]}
        self.run = {'status': 'success', 'valid_report': True, 'input_sha256': 'abc', 'actual_http_calls': 2, 'latency_ms': 100,
                    'cost_usd': .01, 'cost_complete': True, 'request_tool_policy': [{'http_status': 200, 'supplied_evidence_ids': ['log:1', 'log:2'], 'usage': {'prompt_tokens': 1, 'completion_tokens': 1, 'total_tokens': 2}}, {'http_status': 200, 'supplied_evidence_ids': [], 'usage': {'prompt_tokens': 1, 'completion_tokens': 1, 'total_tokens': 2}}], 'result': {'RawLLMOutput': '{"cause_code":"client_timeout"}',
                    'evidence_ids': ['log:1', 'log:2'], 'tool_trace': [{'output': {'evidence': [{'id': 'log:1'}, {'id': 'log:2'}]}}]}}

    def test_score_cause_and_evidence_independently(self):
        row = score_attempt(self.case, self.label, self.run)
        self.assertTrue(row['root_cause_correct'])
        self.assertTrue(row['key_evidence_acquired'])
        self.assertTrue(row['key_evidence_used'])
        self.run['result']['evidence_ids'] = ['log:1']
        row = score_attempt(self.case, self.label, self.run)
        self.assertTrue(row['root_cause_correct'])
        self.assertFalse(row['key_evidence_used'])

    def test_distinguish_retrieval_and_reasoning_failure(self):
        self.run['result']['RawLLMOutput'] = '{"cause_code":"network_delay"}'
        row = score_attempt(self.case, self.label, self.run)
        self.assertTrue(row['wrong_attribution'])
        self.assertEqual(row['diagnostic_gap'], 'reasoning_error')
        self.run['result']['tool_trace'] = []
        self.run['request_tool_policy'] = []
        row = score_attempt(self.case, self.label, self.run)
        self.assertEqual(row['diagnostic_gap'], 'evidence_not_acquired')
        self.assertFalse(row['key_evidence_used'])

    def test_report_failure_still_acquired_and_supplied_current_evidence(self):
        self.run['status'] = 'failure'
        self.run['valid_report'] = False
        self.run['result'] = None
        row = score_attempt(self.case, self.label, self.run)
        self.assertTrue(row['key_evidence_acquired'])
        self.assertTrue(row['key_evidence_supplied'])
        self.assertEqual(row['diagnostic_gap'], 'reporting_or_model_failure')

    def test_retrieved_but_compacted_evidence_not_reasoning_error(self):
        self.run['result']['RawLLMOutput'] = '{"cause_code":"network_delay"}'
        self.run['request_tool_policy'][0]['supplied_evidence_ids'] = ['log:1']
        row = score_attempt(self.case, self.label, self.run)
        self.assertTrue(row['key_evidence_acquired'])
        self.assertFalse(row['key_evidence_supplied'])
        self.assertEqual(row['diagnostic_gap'], 'evidence_not_supplied')

    def test_empty_usage_does_not_claim_complete_tokens(self):
        self.run['actual_http_calls'] = 1
        self.run['request_tool_policy'] = [{'http_status': 200, 'usage': {}}]
        self.assertFalse(score_attempt(self.case, self.label, self.run)['usage_complete'])

    def test_unknown_and_unresolved_not_correct(self):
        self.run['status'] = 'unresolved'
        self.run['result']['RawLLMOutput'] = '{"cause_code":"unknown"}'
        row = score_attempt(self.case, self.label, self.run)
        self.assertTrue(row['unresolved'])
        self.assertFalse(row['root_cause_correct'])
        self.assertFalse(row['wrong_attribution'])

    def test_missing_failed_costs_and_p95_stay_in_denominator(self):
        a = score_attempt(self.case, self.label, self.run)
        b = score_attempt(self.case, self.label, None)
        c = score_attempt(self.case, self.label, {'status': 'failure', 'input_sha256': 'abc', 'actual_http_calls': 3, 'latency_ms': 500, 'cost_usd': None})
        summary = summarize_mode([a, b, c])
        self.assertEqual(summary['scheduled_attempts'], 3)
        self.assertEqual(summary['failed_attempts'], 2)
        self.assertEqual(summary['root_cause_accuracy'], 1/3)
        self.assertEqual(summary['mean_actual_http_calls'], None)  # missing attempt unknown, not zero
        self.assertEqual(summary['recorded_http_calls'], 5)
        self.assertEqual(summary['recorded_latency_p95_ms'], 500)
        self.assertIsNone(summary['mean_cost_usd'])
        self.assertIsNone(summary['latency_p95_ms'])

    def test_reject_input_hash_mismatch(self):
        self.run['input_sha256'] = 'different'
        with self.assertRaises(ValueError):
            score_attempt(self.case, self.label, self.run)

    def test_complete_schedule_not_found_files_controls_sample_size(self):
        suite = {'cases': [self.case]}
        labels = {'cases': [self.label]}
        result = score_runs(suite, labels, {}, trials=2)
        for mode in ('baseline', 'hybrid', 'agent'):
            self.assertEqual(result['summary'][mode]['scheduled_attempts'], 2)
            self.assertEqual(result['summary'][mode]['failed_attempts'], 2)

    def test_reject_different_model_or_budget_between_modes(self):
        suite = {'cases': [self.case], 'revision': 'revision'}
        base = dict(self.run, model='fixed-model-v1', provider_endpoint='https://provider.invalid',
                    benchmark_revision='revision', max_steps=12, reservation_budget=1000000, max_context_chars=24000)
        other = dict(base, model='different-model')
        with self.assertRaises(ValueError):
            score_runs(suite, {'cases': [self.label]}, {('baseline', '1001', 0): base, ('agent', '1001', 0): other}, 1)
        other = dict(base, reservation_budget=1)
        with self.assertRaises(ValueError):
            score_runs(suite, {'cases': [self.label]}, {('baseline', '1001', 0): base, ('agent', '1001', 0): other}, 1)

    def test_reject_mutated_full_snapshot_and_inconsistent_tariffs(self):
        from ota_fault_pack import seal_suite
        import copy
        public = seal_suite({'benchmark': 'test', 'adapter': 'ota-fault-pack-v1', 'task': 'ota', 'partition': 'dev',
                             'cases': [{'id': '1001', 'lines': ['a'], 'error_code_stats': {'E': 1}, 'logs_summary': '{}'}]})
        altered = copy.deepcopy(public)
        altered['cases'][0]['lines'] = ['changed']
        with self.assertRaises(ValueError):
            score_runs(altered, {'cases': [self.label]}, {}, 1)
        suite = {'cases': [self.case], 'revision': 'revision'}
        run = dict(self.run, model='fixed-model-v1', provider_endpoint='https://provider.invalid',
                   benchmark_revision='revision', max_steps=12, reservation_budget=1000000, max_context_chars=24000, pricing={'source': 'tariff-A'})
        changed = dict(run, pricing={'source': 'tariff-B'})
        with self.assertRaises(ValueError):
            score_runs(suite, {'cases': [self.label]}, {('baseline', '1001', 0): run, ('agent', '1001', 0): changed}, 1)

    def test_hybrid_route_strata_pair_all_modes_and_keep_missing(self):
        suite = {'cases': [self.case, dict(self.case, id='1002')], 'revision': 'revision'}
        labels = {'cases': [self.label, dict(self.label, id='1002')]}
        base = dict(self.run, model='m', provider_endpoint='local', benchmark_revision='revision',
                    max_steps=12, reservation_budget=1000000, max_context_chars=24000)
        runs = {('hybrid', '1001', 0): dict(base, actual_route='fixed'),
                ('hybrid', '1002', 0): dict(base, actual_route='agent'),
                ('agent', '1001', 0): dict(base, actual_route='agent')}
        report = score_runs(suite, labels, runs, 2)
        strata = report['summary_by_hybrid_route']
        self.assertEqual(set(strata), {'fixed', 'agent', 'unknown'})
        for mode in ('baseline', 'hybrid', 'agent'):
            self.assertEqual(sum(x[mode]['scheduled_attempts'] for x in strata.values()), 4)
        self.assertEqual(strata['fixed']['agent']['recorded_http_calls'], 2)
        self.assertEqual(strata['fixed']['baseline']['missing_attempts'], 1)
        self.assertIsNone(strata['fixed']['baseline']['mean_cost_usd'])
        self.assertEqual(strata['unknown']['hybrid']['scheduled_attempts'], 2)

    def test_mentions_correct_cause_in_reasoning_not_credit(self):
        self.run['result']['RawLLMOutput'] = '{"cause_code":"network_delay","root_cause":"client_timeout is a competing hypothesis"}'
        self.assertFalse(score_attempt(self.case, self.label, self.run)['root_cause_correct'])


if __name__ == '__main__':
    unittest.main()
