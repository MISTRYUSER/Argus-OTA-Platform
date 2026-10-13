"""Static cross-file checks; backend native validators still required with Docker."""
import json
import pathlib
import re
import shutil
import xml.etree.ElementTree as ET
import unittest
import importlib.util
import tempfile
import stat
import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2] / 'deployments' / 'observability'


def load(name):
    return yaml.safe_load((ROOT / name).read_text())


class ConfigTest(unittest.TestCase):
    def test_yaml_and_json_parse(self):
        for path in ROOT.rglob('*.yaml'):
            self.assertIsInstance(yaml.safe_load(path.read_text()), dict, str(path))
        for path in ROOT.rglob('*.json'):
            self.assertIsInstance(json.loads(path.read_text()), dict, str(path))

    def test_bindings_pins_mounts(self):
        for name in ['compose.yaml', 'compose.langfuse.yaml', 'compose.langfuse-dev-storage.yaml']:
            compose = load(name)
            for service in compose['services'].values():
                for port in service.get('ports', []):
                    self.assertTrue(port.startswith('127.0.0.1:'), port)
                if 'image' in service:
                    self.assertRegex(service['image'], r':(?:v?\d+\.\d+(?:\.\d+)?(?:-alpine)?|RELEASE\.\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z)$|@sha256:[a-f0-9]{64}$')
                for volume in service.get('volumes', []):
                    if volume.startswith('./'):
                        self.assertTrue((ROOT / volume.split(':')[0]).exists(), volume)

    def test_collector_graph_and_optional_filter(self):
        base = load('collector.yaml')
        for name in ['collector.yaml', 'collector-langfuse.yaml']:
            config = load(name)
            for pipeline in config['service']['pipelines'].values():
                for kind in ['receivers', 'processors', 'exporters']:
                    for item in pipeline[kind]:
                        self.assertIn(item, {**base.get(kind, {}), **config.get(kind, {})})
        self.assertNotIn('otlp_http/langfuse', base['exporters'])
        optional = load('collector-langfuse.yaml')
        self.assertEqual(optional['processors']['filter/agent']['traces']['span'], ['attributes["argus.agent"] != true'])
        self.assertEqual(optional['exporters']['otlp_http/langfuse']['headers']['x-langfuse-ingestion-version'], '4')
        self.assertEqual(base['exporters']['otlp_http/loki']['endpoint'], 'http://loki:3100/otlp')
        self.assertFalse(base['exporters']['prometheus']['resource_to_telemetry_conversion']['enabled'])

    def test_collector_storage_init_runs_before_nonroot_collector(self):
        services = load('compose.yaml')['services']
        init = services['collector-storage-init']
        collector = services['collector']
        self.assertEqual(collector['user'], '10001:10001')
        self.assertEqual(init['command'], ['chown', '-R', '10001:10001', '/var/lib/otelcol'])
        self.assertEqual(init['volumes'], ['collector-data:/var/lib/otelcol'])
        self.assertEqual(init['cap_drop'], ['ALL'])
        self.assertEqual(set(init['cap_add']), {'CHOWN', 'DAC_READ_SEARCH'})
        self.assertTrue(init['read_only'])
        self.assertEqual(init['restart'], 'no')
        self.assertEqual(collector['depends_on']['collector-storage-init']['condition'], 'service_completed_successfully')

    def test_startup_logs_without_ids_skip_trace_parser(self):
        operators = load('collector.yaml')['receivers']['file_log']['operators']
        parser = next(operator for operator in operators if operator['type'] == 'trace_parser')
        self.assertEqual(parser['if'], 'attributes.trace_id != nil && attributes.trace_id != "" && attributes.span_id != nil && attributes.span_id != ""')
        self.assertEqual(parser['trace_id']['parse_from'], 'attributes.trace_id')
        self.assertEqual(parser['span_id']['parse_from'], 'attributes.span_id')

    def test_grafana_references(self):
        sources = load('grafana/provisioning/datasources/sources.yaml')['datasources']
        self.assertEqual({s['uid'] for s in sources}, {'prometheus', 'tempo', 'loki'})
        by = {s['uid']: s for s in sources}
        self.assertEqual(by['loki']['jsonData']['derivedFields'][0]['datasourceUid'], 'tempo')
        self.assertEqual(by['tempo']['jsonData']['tracesToLogsV2']['datasourceUid'], 'loki')
        dashboard = json.loads((ROOT / 'grafana/dashboards/argus.json').read_text())
        self.assertGreaterEqual(len(dashboard['panels']), 4)
        for panel in dashboard['panels']:
            for target in panel['targets']:
                self.assertNotRegex(target['expr'], r'batch_id|vin|prompt')

    def test_dev_credentials_are_private_and_never_overwritten(self):
        spec = importlib.util.spec_from_file_location('prepare_dev', pathlib.Path(__file__).with_name('prepare_langfuse_dev.py'))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as folder:
            env, sql = module.prepare(pathlib.Path(folder))
            self.assertEqual(stat.S_IMODE(env.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(sql.stat().st_mode), 0o600)
            values = dict(line.split('=', 1) for line in env.read_text().splitlines())
            self.assertEqual(len(values['LANGFUSE_ENCRYPTION_KEY']), 64)
            self.assertTrue(values['LANGFUSE_PUBLIC_KEY'].startswith('pk-lf-'))
            self.assertTrue(values['LANGFUSE_SECRET_KEY'].startswith('sk-lf-'))
            self.assertIn('CREATE DATABASE langfuse OWNER langfuse;', sql.read_text())
            original = env.read_bytes()
            with self.assertRaises(ValueError):
                module.prepare(pathlib.Path(folder))
            self.assertEqual(env.read_bytes(), original)

    def test_disposable_storage_is_separate_private_and_bounded(self):
        config = load('compose.langfuse-dev-storage.yaml')
        self.assertNotIn('postgres', config['services'])
        self.assertNotIn('redis', config['services'])
        self.assertNotIn('minio', config['services'])
        self.assertIn('noeviction', config['services']['langfuse-dev-redis']['command'])
        init = config['services']['langfuse-dev-minio-init']['command'][0]
        self.assertIn('mc anonymous set none lf/argus-langfuse', init)
        policy = json.loads((ROOT / 'minio-langfuse-policy.json').read_text())
        for statement in policy['Statement']:
            self.assertTrue(all(resource.startswith('arn:aws:s3:::argus-langfuse') for resource in statement['Resource']))
        memory = ET.parse(ROOT / 'clickhouse/local-memory.xml').getroot()
        self.assertLessEqual(int(memory.findtext('max_server_memory_usage')), 1024**3)
        optional = load('compose.langfuse.yaml')
        for name in ['clickhouse', 'langfuse-web', 'langfuse-worker']:
            self.assertIn('mem_limit', optional['services'][name])

    def test_reused_langfuse_storage_is_explicit(self):
        config = load('compose.langfuse.yaml')
        self.assertNotIn('postgres', config['services'])
        self.assertNotIn('redis', config['services'])
        self.assertNotIn('minio', config['services'])
        env = config['services']['langfuse-worker']['environment']
        self.assertEqual(env['REDIS_KEY_PREFIX'], 'argus-langfuse:')
        self.assertEqual(env['LANGFUSE_S3_EVENT_UPLOAD_BUCKET'], 'argus-langfuse')
        self.assertIn(':?', env['DATABASE_URL'])
        self.assertEqual(env['TELEMETRY_ENABLED'], 'false')


if __name__ == '__main__':
    print('Static source checks only; Docker available:', bool(shutil.which('docker')))
    unittest.main()
