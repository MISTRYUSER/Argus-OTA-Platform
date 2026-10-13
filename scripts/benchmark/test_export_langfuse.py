import importlib.util
import json
import pathlib
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SPEC = importlib.util.spec_from_file_location('exporter', pathlib.Path(__file__).with_name('export_langfuse.py'))
e = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(e)


class ExportTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name)

    def plan(self):
        runs = self.root / 'runs.json'
        runs.write_text(json.dumps([
            {'scenario_id': 1, 'trial': '1', 'valid_report': True, 'trace_id': 'ab'*16,
             'root_span_id': 'cd'*8, 'provider_stats': {'Attempts': 2, 'Tokens': 30}, 'latency_ms': 100},
            {'scenario_id': 2, 'trial': '1', 'valid_report': False, 'error': 'parse failed', 'result': None}]))
        strict = self.root / 'strict.json'
        strict.write_text(json.dumps({'rows': [{'incident_id': '1', 'trial': '1', 'f1': .5}]}))
        official = self.root / 'official.json'
        official.write_text(json.dumps({'raw_results': [{'incident_id': '2', 'trial': 1,
            'scores': {'root_cause_entity': {'calculation_f1': 0}, 'root_cause_entity_k@3': {'calculation_f1': 1.5}}, 'error': 'failure retained'}]}))
        return e.build_plan(runs, [('strict', strict), ('official', official)], dataset='local',
                            run='test-run', release='git-sha', model='fixture', timestamp_ns=123)

    def test_evidence_join_failure_and_idempotent_ids(self):
        plan = self.plan()
        self.assertEqual(len(plan['cases']), 2)
        self.assertTrue(plan['cases'][1]['failure'])
        self.assertEqual(plan['cases'][1]['raw']['official']['error'], 'failure retained')
        self.assertNotEqual(plan['cases'][0]['snapshot_trace_id'], 'ab'*16)
        linked = [r['body'] for r in plan['requests'] if r['path'].endswith('/scores') and r['body']['traceId'] == 'ab'*16]
        self.assertEqual(linked[0]['observationId'], 'cd'*8)
        self.assertTrue(any(r['name'] == 'strict.f1' for r in linked))
        self.assertTrue(all(len(s['sha256']) == 64 for s in plan['sources']))
        self.assertEqual(plan, self.plan())

    def server(self, fail=False, partial=False):
        seen = []
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                seen.append((self.path, dict(self.headers), body))
                self.send_response(500 if fail else 200)
                self.end_headers()
                response = {'partialSuccess': {'rejectedSpans': '1'}} if partial else {'id': body.get('id', 'fixture')}
                self.wfile.write(json.dumps(response).encode())
            def log_message(self, *args):
                pass
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return f'http://127.0.0.1:{server.server_port}', seen

    def test_upstream_out_of_range_k_components_are_raw_flagged_not_clamped(self):
        name, annotation, comment = e.score_annotation('official.root_cause_entity_k@3.calculation_f1', 1.5)
        self.assertEqual(name, 'official.raw.root_cause_entity_k@3.calculation_f1')
        self.assertEqual(annotation['expected_range'], [0, 1])
        self.assertEqual(annotation['score_range_validation'], 'outside_expected_range')
        self.assertTrue(annotation['excluded_from_validated_headline'])
        self.assertFalse(annotation['validated_headline_metric'])
        self.assertIn('not clamped', comment)
        name, annotation, _ = e.score_annotation('official.root_cause_entity.calculation_f1', .5)
        self.assertTrue(annotation['headline_component_eligible'])
        self.assertFalse(annotation['validated_headline_metric'])
        _, annotation, _ = e.score_annotation('official.root_cause_entity_k@1.calculation_f1', .5)
        self.assertTrue(annotation['excluded_from_validated_headline'])
        plan = self.plan()
        official = [r['body'] for r in plan['requests'] if r['path'].endswith('/scores') and r['body']['name'].startswith('official.raw.')]
        self.assertTrue(official)
        self.assertTrue(all(r['metadata']['raw_upstream_component'] for r in official))
        invalid = [r for r in official if r['metadata']['score_range_validation'] == 'outside_expected_range']
        self.assertEqual(invalid[0]['value'], 1.5)
        self.assertEqual(plan['cases'][1]['raw']['official']['scores']['root_cause_entity_k@3']['calculation_f1'], 1.5)

    def test_protocol_mock(self):
        endpoint, seen = self.server()
        results = e.send_plan(self.plan(), endpoint, 'public', 'secret')
        self.assertTrue(all(row['ok'] for row in results))
        self.assertEqual(seen[0][0], '/api/public/otel/v1/traces')
        headers = {k.lower(): v for k, v in seen[0][1].items()}
        self.assertEqual(headers['x-langfuse-ingestion-version'], '4')
        self.assertEqual(headers['authorization'], 'Basic cHVibGljOnNlY3JldA==')
        self.assertNotIn('secret', json.dumps(results))

    def test_failures_retained_and_partial_rejected(self):
        for fail, partial in [(True, False), (False, True)]:
            endpoint, _ = self.server(fail, partial)
            results = e.send_plan(self.plan(), endpoint, 'public', 'secret')
            self.assertTrue(any(not row['ok'] for row in results))
            self.assertEqual(len(results), len(self.plan()['requests']))

    def test_invalid_trace_and_duplicate_rows_rejected(self):
        path = self.root / 'invalid.json'
        for rows in [[{'case_id': 1, 'trial': 1, 'trace_id': 'invalid'}],
                     [{'case_id': 1, 'trial': 1}, {'case_id': 1, 'trial': 1}]]:
            path.write_text(json.dumps(rows))
            with self.assertRaises(ValueError):
                e.build_plan(path, [], dataset='d', run='r', release='s', model='m')

    def test_cli_dry_run_never_writes_even_with_credentials(self):
        endpoint, seen = self.server()
        path = self.root / 'one-run.json'
        path.write_text(json.dumps({'case_id': '1', 'trial': '1', 'valid_report': False}))
        out = self.root / 'plan.json'
        result = subprocess.run([sys.executable, str(pathlib.Path(e.__file__)),
            '--runs', str(path), '--dataset', 'local', '--run', 'dry-run',
            '--release', 'sha', '--model', 'fixture', '--out', str(out)],
            env={**os.environ, 'LANGFUSE_BASE_URL': endpoint, 'LANGFUSE_PUBLIC_KEY': 'public',
                 'LANGFUSE_SECRET_KEY': 'secret'}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Dry run:', result.stdout)
        self.assertEqual(seen, [])
        self.assertTrue(json.loads(out.read_text())['cases'][0]['failure'])
        self.assertNotIn('secret', out.read_text())

    def test_remote_http_and_embedded_credentials_rejected(self):
        for endpoint in ['http://example.com', 'https://user:pass@example.com', 'https://example.com?secret=x']:
            with self.assertRaises(ValueError):
                e.send_plan({'requests': []}, endpoint, 'p', 's')


if __name__ == '__main__':
    unittest.main()
