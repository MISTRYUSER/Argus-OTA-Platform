"""Exercise existing Go entrypoint over a local scripted HTTP provider. Not model-quality evidence."""
import hashlib
import http.server
import json
import os
import pathlib
import subprocess
import tempfile
import threading
import unittest
from ota_fault_pack import seal_suite


class ReplayCLITests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.go = os.environ.get('GO_BIN', '/tmp/go/bin/go' if pathlib.Path('/tmp/go/bin/go').exists() else 'go')
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = pathlib.Path(cls.temp.name)
        cls.binary = cls.root / 'agent-bench'
        subprocess.run([cls.go, 'build', '-o', str(cls.binary), './workers/ai-agent/cmd/agent-bench'], cwd=pathlib.Path(__file__).resolve().parents[2], check=True, capture_output=True)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def test_three_production_modes_and_failed_usage_receipts(self):
        behavior = {'invalid': False}
        class Provider(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass
            def do_POST(self):
                request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                messages = request['messages']
                fixed = 'Summarize the supplied observations' in messages[0]['content']
                baseline = '无工具，仅总结已提供证据' in messages[0]['content']
                tool_messages = [m for m in messages if m['role'] == 'tool']
                if not (fixed or baseline or tool_messages):
                    functions = [('read_logs', '{}'), ('error_statistics', '{}'), ('update_investigation_plan', '{"hypotheses":[{"id":"H1","description":"publisher and received bytes agree, manifest differs","status":"supported","supports":["log:1"],"next_check":"verify manifest"}]}')]
                    message = {'role': 'assistant', 'content': '', 'tool_calls': [{'id': name, 'type': 'function', 'function': {'name': name, 'arguments': args}} for name, args in functions]}
                else:
                    stats = 'stats:1' if fixed else 'stats:OTA_CHECKSUM_MISMATCH'
                    report = {'hypothesis_id': 'H1', 'root_cause': 'manifest differs from publisher and received bytes', 'cause_code': 'manifest_mismatch', 'suggestions': ['check manifest'], 'severity': 'high', 'confidence': .5, 'evidence_ids': ['log:1', stats], 'limitations': ['scripted model']}
                    message = {'role': 'assistant', 'content': 'invalid JSON' if behavior['invalid'] else json.dumps(report)}
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps({'model': 'scripted-test-v1', 'choices': [{'message': message, 'finish_reason': 'stop'}], 'usage': {'prompt_tokens': 20, 'completion_tokens': 10, 'total_tokens': 30, 'prompt_cache_hit_tokens': 0, 'prompt_cache_miss_tokens': 20}}).encode())
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        thread = threading.Thread(target=lambda: server.serve_forever(poll_interval=.01))
        thread.start()
        folder = self.root / 'three-modes'
        folder.mkdir()
        key = folder / 'key'
        key.write_text('local-script-only')
        line = '{"error_type":"OTA_CHECKSUM_MISMATCH","expected_checksum":"a","actual_checksum":"b","published_sha256":"b"}'
        input = {'benchmark': 'controlled', 'adapter': 'ota-fault-pack-v1', 'task': 'ota', 'revision': 'test', 'partition': 'heldout', 'cases': [{'id': '1001', 'lines': [line], 'sha256': hashlib.sha256(line.encode()).hexdigest(), 'error_code_stats': {'OTA_CHECKSUM_MISMATCH': 1}, 'logs_summary': '{"hybrid_evidence_schema":"v1","stats_grain":"log_occurrence","complete":true}'}]}
        input = seal_suite(input)
        file = folder / 'input.json'
        file.write_text(json.dumps(input))
        price = folder / 'pricing.json'
        price.write_text(json.dumps({'model': 'scripted-test-v1', 'as_of': '2026-10-13', 'source': 'not a real tariff', 'input_cached_usd_per_million': 1, 'input_uncached_usd_per_million': 2, 'output_usd_per_million': 3}))
        env = dict(os.environ, DEEPSEEK_API_KEY_FILE=str(key), OTEL_TRACES_SAMPLER='always_on')
        try:
            for mode, calls in [('baseline', 1), ('hybrid', 0), ('agent', 2)]:
                out = folder / mode
                command = [str(self.binary), '-input', str(file), '-out', str(out), '-mode', mode, '-trials', '1', '-parallel', '1', '-heldout', '-model', 'scripted-test-v1', '-base-url', f'http://127.0.0.1:{server.server_port}/', '-pricing', str(price)]
                subprocess.run(command, env=env, check=True, capture_output=True, timeout=20)
                row = json.loads((out / 'scenario-1001' / '0' / 'outputs' / 'argus_run.json').read_text())
                self.assertEqual(row['status'], 'success', row)
                self.assertEqual(row['actual_http_calls'], calls)
                self.assertTrue(row['cost_complete'])
                if calls:
                    self.assertEqual(row['request_tool_policy'][0]['provider_model'], 'scripted-test-v1')
                    self.assertEqual(json.loads(row['result']['RawLLMOutput'])['cause_code'], 'manifest_mismatch')
                else:
                    self.assertEqual(row['result']['routing']['path'], 'deterministic')
                    self.assertEqual(row['result']['model_steps'], 0)
                    self.assertEqual(row['cost_usd'], 0)
                    self.assertFalse(row['request_tool_policy'])
                self.assertNotIn('local-script-only', json.dumps(row))
            # Same real entrypoint, but incomplete coverage must use investigation.
            fallback_input = json.loads(json.dumps(input))
            fallback_input['cases'][0]['logs_summary'] = '{}'
            fallback_file = folder / 'fallback-input.json'
            fallback_file.write_text(json.dumps(seal_suite(fallback_input)))
            fallback_out = folder / 'hybrid-investigation'
            subprocess.run([str(self.binary), '-input', str(fallback_file), '-out', str(fallback_out),
                            '-mode', 'hybrid', '-trials', '1', '-parallel', '1', '-model', 'scripted-test-v1',
                            '-base-url', f'http://127.0.0.1:{server.server_port}/'], env=env, check=True, capture_output=True, timeout=20)
            fallback_row = json.loads((fallback_out / 'scenario-1001' / '0' / 'outputs' / 'argus_run.json').read_text())
            self.assertEqual(fallback_row['status'], 'success', fallback_row)
            self.assertEqual(fallback_row['actual_route'], 'agent')
            self.assertFalse(fallback_row['fixed_eligible'])
            self.assertEqual(fallback_row['actual_http_calls'], 2)
            self.assertTrue(fallback_row['result']['tool_trace'])
            behavior['invalid'] = True
            for mode in ('baseline',):
                out = folder / ('invalid-' + mode)
                command = [str(self.binary), '-input', str(file), '-out', str(out), '-mode', mode, '-trials', '1', '-parallel', '1', '-model', 'scripted-test-v1', '-base-url', f'http://127.0.0.1:{server.server_port}/']
                subprocess.run(command, env=env, check=True, capture_output=True, timeout=20)
                row = json.loads((out / 'scenario-1001' / '0' / 'outputs' / 'argus_run.json').read_text())
                self.assertEqual(row['status'], 'failure')
                self.assertEqual(row['request_tool_policy'][0]['supplied_evidence_ids'], ['log:1'])
                self.assertIsNone(row['result'])
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    unittest.main()
