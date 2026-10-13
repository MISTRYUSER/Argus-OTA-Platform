"""Normalize actual Apollo Cyber protobuf records without inventing OTA metrics.

This format is not the proprietary Inceptio .rec format. Record container
versions describe serialization, never OTA software versions.
"""
import hashlib
from pathlib import Path

METADATA_FIELDS = ('version', 'vehicle_id', 'vehicle_class', 'replay_id', 'workload')


def monitor_events(message):
    """Only explicit monitor severities are warnings/errors, not sensor samples."""
    if message.DESCRIPTOR.full_name != 'apollo.common.monitor.MonitorMessage':
        return []
    events = []
    for item in message.item:
        field = item.DESCRIPTOR.fields_by_name.get('log_level')
        if not field or not item.HasField('log_level'):
            continue
        severity = field.enum_type.values_by_number[item.log_level].name
        if severity in ('WARN', 'ERROR', 'FATAL'):
            events.append({'severity': severity, 'message': item.msg})
    return events


def parse_record(path, source, metadata, max_rows=1_000_000):
    """Return normalized observations; provenance metadata must be explicit.

    Unknown topics are retained as decoded observations. CPU/memory remain
    unavailable. ``raw.error_observed`` distinguishes monitor observations from
    topics that provide no monitor error signal.
    """
    if not isinstance(metadata, dict):
        raise ValueError('Apollo record requires explicit metadata object')
    missing = [key for key in METADATA_FIELDS if not isinstance(metadata.get(key), str) or not metadata[key].strip()]
    if missing:
        raise ValueError('Apollo record requires explicit metadata: ' + ', '.join(missing))
    if not isinstance(max_rows, int) or max_rows < 1:
        raise ValueError('max_rows must be positive')
    try:
        from cyber_record.record import Record
    except ImportError as exc:
        raise ValueError('Apollo Cyber parser unavailable; install requirements-record.txt in a virtual environment') from exc
    path = Path(path)
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    rows = []
    try:
        with Record(str(path)) as record:
            if record.size != path.stat().st_size:
                raise ValueError('record header size differs from actual file size')
            expected = record.get_message_count()
            if expected > max_rows:
                raise ValueError('record exceeds max_rows')
            for index, (topic, message, timestamp_ns) in enumerate(record.read_messages(), 1):
                if index > max_rows:
                    raise ValueError('record exceeds max_rows')
                events = monitor_events(message)
                errors = sorted({event['severity'] for event in events if event['severity'] in ('ERROR', 'FATAL')})
                is_monitor = message.DESCRIPTOR.full_name == 'apollo.common.monitor.MonitorMessage'
                rows.append({
                    **{key: metadata[key].strip() for key in METADATA_FIELDS},
                    'timestamp': timestamp_ns / 1_000_000_000,
                    'module': topic, 'error_code': '|'.join(errors),
                    'cpu': None, 'memory_mb': None, 'source': str(source), 'line': index,
                    'raw': {'format': 'apollo-cyber-record', 'message_type': message.DESCRIPTOR.full_name,
                            'timestamp_ns': timestamp_ns, 'file_sha256': digest,
                            'payload_sha256': hashlib.sha256(message.SerializeToString()).hexdigest(),
                            'error_observed': is_monitor, 'monitor_events': events,
                            'metadata_origin': 'caller-supplied'},
                })
            if len(rows) != expected:
                raise ValueError(f'record message count mismatch: expected {expected}, decoded {len(rows)}')
    except Exception as exc:
        raise ValueError(f'Cannot decode Apollo Cyber record {path.name}: {exc}') from exc
    return rows
