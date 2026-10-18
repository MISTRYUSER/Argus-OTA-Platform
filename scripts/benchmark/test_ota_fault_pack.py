import hashlib
import json
import pathlib
import subprocess
import tempfile
import unittest

from ota_fault_pack import generate_pack, prepare_suite


class FaultPackTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.root = pathlib.Path(cls.tmp.name) / 'pack'
        cls.receipt = generate_pack(cls.root)
        cls.public = cls.root / 'public'
        cls.labels = json.loads((cls.root / 'private' / 'labels.json').read_text())

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def test_twelve_real_faults_and_healthy_controls(self):
        self.assertEqual(len(self.labels['cases']), 12)
        for label in self.labels['cases']:
            self.assertTrue(label['injection_observed'], label['id'])
            self.assertTrue(label['control']['success'])
            self.assertTrue(label['restored']['success'])
            self.assertTrue(label['recovery_checks_passed'])
            self.assertGreater(label['fault']['actual_server_requests'], 0)
        self.assertEqual(len({x['cause_code'] for x in self.labels['cases']}), 6)

    def test_actual_git_and_artifact_are_verifiable(self):
        gitdir = self.root / 'release-source'
        for case in self.labels['cases']:
            folder = self.public / 'cases' / case['id']
            release = json.loads((folder / 'release.json').read_text())
            config = (folder / 'config.json').read_bytes()
            self.assertEqual(hashlib.sha256(config).hexdigest(), release['config_sha256'])
            committed = subprocess.check_output(['git', '-C', str(gitdir), 'show', release['git_commit'] + ':config.json'])
            self.assertEqual(committed, config)
            artifact = (folder / 'artifact.bin').read_bytes()
            self.assertEqual(hashlib.sha256(artifact).hexdigest(), release['actual_artifact_sha256'])
            self.assertNotEqual(release['git_commit'], release['baseline_git_commit'])

    def test_private_never_changes_blind_suite(self):
        before = prepare_suite(self.public, 'heldout')
        path = self.root / 'private' / 'labels.json'
        saved = path.read_text()
        try:
            path.write_text('SECRET_GROUND_TRUTH_INVALID_JSON')
            self.assertEqual(before, prepare_suite(self.public, 'heldout'))
        finally:
            path.write_text(saved)
        text = json.dumps(before)
        self.assertNotIn('cause_code', text)
        self.assertNotIn('injection_observed', text)
        for case in before['cases']:
            self.assertEqual(case['sha256'], hashlib.sha256('\n'.join(case['lines']).encode()).hexdigest())
            self.assertTrue(all(len(line) <= 400 for line in case['lines']))

    def test_distinct_timeout_causes_and_noise(self):
        suite = prepare_suite(self.public, 'dev')
        self.assertEqual(len(suite['cases']), 6)
        labels = {x['id']: x for x in self.labels['cases']}
        timeouts = [x for x in suite['cases'] if labels[x['id']]['cause_code'] in {'client_timeout', 'server_slow', 'network_delay'}]
        self.assertEqual(len(timeouts), 3)
        for case in timeouts:
            self.assertIn('OTA_DOWNLOAD_TIMEOUT', json.dumps(case['lines']))
            self.assertIn('heartbeat', json.dumps(case['lines']))
            self.assertIn('ui_banner', json.dumps(case['lines']))
            self.assertTrue(all(group for group in labels[case['id']]['key_evidence_groups']))

    def test_retry_amplification_is_measured(self):
        rows = [c for c in self.labels['cases'] if c['cause_code'] == 'retry_amplification']
        for row in rows:
            self.assertGreater(row['fault']['actual_server_requests'], row['control']['actual_server_requests'])
            self.assertGreater(row['fault']['failed_requests'], row['control']['failed_requests'])

    def test_reject_existing_pack_and_unknown_partition(self):
        with self.assertRaises(FileExistsError):
            generate_pack(self.root)
        with self.assertRaises(ValueError):
            prepare_suite(self.public, 'bad')


if __name__ == '__main__':
    unittest.main()

class FaultIntegrityTests(unittest.TestCase):
    def test_setup_delay_cannot_hide_retry_fault(self):
        from ota_fault_lab import PUBLISHED_SHA, run_download
        cfg = dict(timeout_ms=200, service_delay_ms=1, proxy_delay_ms=0, max_attempts=4,
                   retry_backoff_ms=0, ready_after_ms=80, manifest_checksum=PUBLISHED_SHA,
                   logical_requests=3, startup_delay_ms=120)
        from unittest.mock import patch
        import http.server
        import time
        original = http.server.ThreadingHTTPServer
        def slow_start(*args, **kwargs):
            time.sleep(.12)
            return original(*args, **kwargs)
        with patch('ota_fault_lab.http.server.ThreadingHTTPServer', side_effect=slow_start):
            result = run_download(cfg)
        self.assertFalse(result['success'])
        self.assertEqual(result['actual_server_requests'], 12)

    def test_unobserved_fault_never_publishes_valid_suite(self):
        from unittest.mock import patch
        from ota_fault_lab import run_download, PUBLISHED_SHA
        def healthy(_):
            return run_download(dict(timeout_ms=200, service_delay_ms=1, proxy_delay_ms=0,
                                     max_attempts=1, retry_backoff_ms=0, manifest_checksum=PUBLISHED_SHA,
                                     logical_requests=1))
        with tempfile.TemporaryDirectory() as temp, patch('ota_fault_pack.run_download', side_effect=healthy):
            root = pathlib.Path(temp) / 'bad'
            with self.assertRaises(RuntimeError):
                generate_pack(root)
            self.assertFalse((root / 'public' / 'all.json').exists())

    def test_full_ota_digest_binds_model_visible_route_and_sources(self):
        from ota_fault_pack import seal_suite, verify_suite
        suite = {'benchmark': 'test', 'adapter': 'ota-fault-pack-v1', 'task': 'ota', 'partition': 'dev',
                 'cases': [{'id': '1', 'lines': ['a'], 'error_code_stats': {'E': 1}, 'logs_summary': '{}', 'sources': [], 'snapshot_scope': 'full'}]}
        suite = seal_suite(suite)
        verify_suite(suite)
        for key, value in [('lines', ['changed']), ('error_code_stats', {'E': 2}), ('logs_summary', 'complete'), ('sources', [{'file': 'false'}])]:
            changed = json.loads(json.dumps(suite))
            changed['cases'][0][key] = value
            with self.assertRaises(ValueError):
                verify_suite(changed)
        changed = json.loads(json.dumps(suite)); changed['partition'] = 'heldout'
        with self.assertRaises(ValueError):
            verify_suite(changed)
