# Public Apollo record smoke test

This repository contains an actual public binary recording and its matching
decoder. It exercises the record-to-normalized-observation path, including
decoding protobuf schemas embedded in the file. It is **Apollo Cyber `.record`
format**, not the proprietary Inceptio `.rec` format; renaming extensions does
not establish format compatibility.

## Provenance and license

- Source repository: https://github.com/daohu527/cyber-record
- Pinned parser and fixture revision: `f0b4be600dfd6fc9aac3db898a93337a091a324c`
- Fixture: https://github.com/daohu527/cyber-record/blob/f0b4be600dfd6fc9aac3db898a93337a091a324c/test/assets/example.record.00000
- Local fixture: `testdata/public-record/example.record.00000`
- Bytes: `489010`
- SHA-256: `3d88a00681de8bdd077430bf3adb883f9ab94799b573f6860492b701dcfd777b`
- Upstream package: `cyber_record` 0.1.13, Apache-2.0; upstream LICENSE
  accompanies the fixture in `testdata/public-record/LICENSE`.
- Parser is an optional dependency pinned by Git revision in
  `workers/python-aggregator/requirements-record.txt`; protobuf is pinned to
  5.29.6. No Apollo runtime or vehicle hardware is required.

## Reproduce

Use an isolated environment so the parser's protobuf dependency does not
replace a user's system packages:

```sh
python3 -m venv /tmp/argus-record-venv
/tmp/argus-record-venv/bin/pip install -r workers/python-aggregator/requirements-record.txt
/tmp/argus-record-venv/bin/python -m unittest discover -s workers/python-aggregator -p test_apollo_record.py -v
/tmp/argus-record-venv/bin/python scripts/benchmark/public_record.py --metadata '{"version":"fixture-label","vehicle_id":"public-fixture","vehicle_class":"unknown","replay_id":"public-fixture","workload":"fixture"}'
```

The labels above explicitly describe the fixture; they are not a measured OTA
version, known production vehicle, or verified workload class. A caller must
provide these five metadata fields when normalizing a record. Container version
`1.0` must never be used as the OTA version. `--download` restores the exact
pinned fixture and verifies its checksum before writing. `--rows-output PATH`
optionally writes normalized rows; the default prints only the coverage summary.

## Verified results

The actual recording decodes all **34 messages** over **0.138967839 seconds**:

| Topic | Decoded messages |
| --- | ---: |
| `/apollo/localization/pose` | 15 |
| `/apollo/canbus/chassis` | 15 |
| `/apollo/prediction` | 2 |
| `/apollo/routing_response_history` | 1 |
| `/apollo/planning` | 1 |

There are no monitor-topic messages and no CPU or memory telemetry. Therefore
the fixture provides **zero observed error-status samples**, **zero CPU
observations**, and **zero memory observations**. An empty error code on an
ordinary sensor/planning sample is not evidence of error-free operation.
The adapter records `raw.error_observed=false` on these samples; regression
analysis must omit them from an observed error-rate denominator.

`apollo_record.parse_record(path, source, metadata, max_rows=...)` emits source
and one-based message-index references, nanosecond timestamps in raw provenance,
file/payload SHA-256 hashes, decoded protobuf message types, caller metadata,
and topic names. CPU and memory are `None`. For explicit Apollo monitor messages,
WARN severities are preserved in raw events while ERROR/FATAL severities become
error codes. Other topics remain observations even when their names are unknown.

Six adapter tests passed against the actual fixture and generated unit fixtures:
full-file counts/provenance, missing metadata, corrupt/truncated input, row
limits, unknown topics, and explicit monitor severity handling. Generated unit
fixtures test decoder edge cases; they are not production regression evidence.

This sample validates ingestion and missing-data behavior. It cannot establish
a before/after OTA regression, a hardware regression, or a causal conclusion.
A real comparison still needs independently recorded baseline/candidate runs,
explicit version provenance, matching conditions, sufficient duration/sample
coverage, and actual metrics for the requested comparison.
