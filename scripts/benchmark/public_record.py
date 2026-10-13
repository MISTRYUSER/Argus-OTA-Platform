#!/usr/bin/env python3
"""Decode the pinned public Apollo record and report observed coverage."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import sys
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'workers/python-aggregator'))
from apollo_record import parse_record

COMMIT = 'f0b4be600dfd6fc9aac3db898a93337a091a324c'
SHA256 = '3d88a00681de8bdd077430bf3adb883f9ab94799b573f6860492b701dcfd777b'
URL = f'https://raw.githubusercontent.com/daohu527/cyber-record/{COMMIT}/test/assets/example.record.00000'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, help='Machine-readable benchmark output')
    parser.add_argument('--record', type=Path, default=ROOT / 'testdata/public-record/example.record.00000')
    parser.add_argument('--metadata', required=True, help='Explicit JSON object: version, vehicle_id, vehicle_class, replay_id, workload')
    parser.add_argument('--download', action='store_true', help='Restore public fixture from its pinned URL; verify checksum before writing')
    parser.add_argument('--rows-output', type=Path, help='Optional normalized JSON output')
    args = parser.parse_args()
    try:
        if args.download:
            with urllib.request.urlopen(URL, timeout=60) as response:
                content = response.read()
            if hashlib.sha256(content).hexdigest() != SHA256:
                raise ValueError('downloaded fixture checksum mismatch')
            args.record.parent.mkdir(parents=True, exist_ok=True)
            args.record.write_bytes(content)
        actual_sha = hashlib.sha256(args.record.read_bytes()).hexdigest()
        if actual_sha != SHA256:
            raise ValueError('public fixture checksum mismatch; use the regression adapter directly for other files')
        rows = parse_record(args.record, str(args.record), json.loads(args.metadata))
        if args.rows_output:
            args.rows_output.parent.mkdir(parents=True, exist_ok=True)
            args.rows_output.write_text(json.dumps(rows, indent=2) + '\n')
        summary = {
            'format': 'apollo-cyber-record', 'source_url': URL, 'parser_commit': COMMIT,
            'sha256': actual_sha, 'bytes': args.record.stat().st_size,
            'decoded_messages': len(rows), 'topics': dict(sorted(Counter(row['module'] for row in rows).items())),
            'duration_seconds': (max(row['raw']['timestamp_ns'] for row in rows) - min(row['raw']['timestamp_ns'] for row in rows)) / 1e9 if rows else None,
            'error_observations': sum(row['raw']['error_observed'] for row in rows),
            'cpu_observations': 0, 'memory_observations': 0,
            'limitations': ['Public Apollo format is not Inceptio .rec.', 'Caller metadata is a provenance label, not a measured OTA version.',
                            'This fixture validates decoding only; it cannot establish an OTA regression.'],
        }
        if args.out:
            args.out.parent.mkdir(parents=True, exist_ok=True)
            args.out.write_text(json.dumps(summary, indent=2)+'\n')
        print(json.dumps(summary, indent=2))
    except (ValueError, OSError) as exc:
        parser.error(str(exc))


if __name__ == '__main__':
    main()
