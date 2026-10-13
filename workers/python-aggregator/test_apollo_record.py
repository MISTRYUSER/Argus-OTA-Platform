import hashlib
from pathlib import Path
import tempfile
import unittest
from apollo_record import parse_record

try:
    from cyber_record.record import Record
    from google.protobuf import descriptor_pb2, descriptor_pool, message_factory
    AVAILABLE = True
except ImportError:
    AVAILABLE = False

FIXTURE = Path(__file__).resolve().parents[2] / 'testdata/public-record/example.record.00000'
METADATA = dict(version='fixture-label', vehicle_id='public-fixture', vehicle_class='unknown', replay_id='public-fixture', workload='fixture')


@unittest.skipUnless(AVAILABLE, 'install optional requirements-record.txt')
class ApolloRecordTests(unittest.TestCase):
    def test_entire_public_fixture_counts_and_provenance(self):
        from collections import Counter
        rows = parse_record(FIXTURE, 'public:test', METADATA)
        self.assertEqual(len(rows), 34)
        self.assertEqual(Counter(row['module'] for row in rows), {
            '/apollo/localization/pose': 15, '/apollo/canbus/chassis': 15,
            '/apollo/prediction': 2, '/apollo/routing_response_history': 1, '/apollo/planning': 1})
        self.assertEqual(hashlib.sha256(FIXTURE.read_bytes()).hexdigest(), '3d88a00681de8bdd077430bf3adb883f9ab94799b573f6860492b701dcfd777b')
        self.assertEqual([row['line'] for row in rows], list(range(1, 35)))
        self.assertTrue(all(row['cpu'] is None and row['memory_mb'] is None for row in rows))
        self.assertTrue(all(not row['raw']['error_observed'] and row['error_code'] == '' for row in rows))
        self.assertTrue(all(row['source'] == 'public:test' and row['version'] == 'fixture-label' for row in rows))

    def test_missing_metadata_rejected(self):
        for key in METADATA:
            metadata = dict(METADATA); del metadata[key]
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, 'explicit metadata'):
                parse_record(FIXTURE, 'test', metadata)

    def test_corrupt_and_truncated_records_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'corrupt.record'
            for data in (b'not a Cyber record', FIXTURE.read_bytes()[:-100]):
                path.write_bytes(data)
                with self.assertRaisesRegex(ValueError, 'Cannot decode'):
                    parse_record(path, 'test', METADATA)

    def test_row_limit_rejected(self):
        with self.assertRaisesRegex(ValueError, 'max_rows'):
            parse_record(FIXTURE, 'test', METADATA, max_rows=20)

    def test_unknown_topic_retained_without_invented_metrics(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'unknown.record'
            with Record(str(path), mode='w') as record:
                record.write('/unknown/topic', descriptor_pb2.FileDescriptorProto(name='example.proto'), 1234567890)
            rows = parse_record(path, 'unknown:test', METADATA)
            self.assertEqual(rows[0]['module'], '/unknown/topic')
            self.assertEqual(rows[0]['timestamp'], 1.23456789)
            self.assertFalse(rows[0]['raw']['error_observed'])

    def test_only_explicit_monitor_severity_is_error(self):
        proto = descriptor_pb2.FileDescriptorProto(name='monitor.proto', package='apollo.common.monitor', syntax='proto2')
        item = proto.message_type.add(name='MonitorMessageItem')
        level = item.enum_type.add(name='LogLevel')
        for number, name in enumerate(('INFO', 'WARN', 'ERROR', 'FATAL')):
            level.value.add(name=name, number=number)
        item.field.add(name='log_level', number=1, label=1, type=14, type_name='.apollo.common.monitor.MonitorMessageItem.LogLevel')
        item.field.add(name='msg', number=2, label=1, type=9)
        monitor = proto.message_type.add(name='MonitorMessage')
        monitor.field.add(name='item', number=1, label=3, type=11, type_name='.apollo.common.monitor.MonitorMessageItem')
        pool = descriptor_pool.DescriptorPool(); pool.Add(proto)
        cls = message_factory.GetMessageClass(pool.FindMessageTypeByName('apollo.common.monitor.MonitorMessage'))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'monitor.record'
            with Record(str(path), mode='w') as record:
                for index, severity in enumerate((0, 1, 2, 3)):
                    message = cls(); message.item.add(log_level=severity, msg='unit test monitor event')
                    record.write('/apollo/monitor', message, 1_000_000_000 + index)
            rows = parse_record(path, 'test', METADATA)
            self.assertEqual([row['error_code'] for row in rows], ['', '', 'ERROR', 'FATAL'])
            self.assertEqual(rows[1]['raw']['monitor_events'][0]['severity'], 'WARN')
            self.assertTrue(all(row['raw']['error_observed'] for row in rows))


if __name__ == '__main__':
    unittest.main()
