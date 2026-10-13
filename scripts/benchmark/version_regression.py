#!/usr/bin/env python3
"""Deterministic held-out synthetic fault injections for the actual OTA analyzer.

These are correctness scenarios, not production data, root-cause accuracy, or
evidence of performance gains. Labels follow the injection design.
"""
import argparse
from collections import Counter
import csv
import io
import json
import math
from pathlib import Path
import random
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'workers/python-aggregator'))
import regression

KINDS = ('cpu_increase', 'memory_increase', 'error_increase', 'no_change',
         'workload_mismatch', 'hardware_mismatch', 'missing_metrics',
         'duration_mismatch', 'sample_count_mismatch', 'sampling_offset_mismatch')
REFUSALS = {'workload_mismatch', 'hardware_mismatch', 'duration_mismatch', 'sample_count_mismatch', 'sampling_offset_mismatch'}
REGRESSIONS = {'cpu_increase', 'memory_increase', 'error_increase'}


def make_scenarios(seed=20261013, repeats=6):
    if repeats < 1:
        raise ValueError('repeats must be positive')
    rng = random.Random(seed)
    scenarios = []
    for kind in KINDS:
        for repeat in range(repeats):
            identity = f'{kind}-{repeat:02d}'
            n = rng.randint(25, 70)
            interval = rng.uniform(.1, 2)
            cpu_base, memory_base = rng.uniform(10, 100), rng.uniform(100, 1000)
            before, after = [], []
            for index in range(n):
                row = dict(timestamp=1700000000 + index * interval, version='baseline',
                           vehicle_id=f'heldout-{identity}', vehicle_class='hardware-A', module='planner',
                           replay_id=f'replay-{identity}', workload='AD', error_code='',
                           cpu=cpu_base + rng.uniform(-1, 1), memory_mb=memory_base + rng.uniform(-2, 2))
                before.append(row)
                after.append(dict(row, version='candidate', timestamp=row['timestamp'] + 86400))
            for index, row in enumerate(after):
                if kind == 'cpu_increase': row['cpu'] += rng.uniform(10, 30)
                if kind == 'memory_increase': row['memory_mb'] += rng.uniform(30, 100)
                if kind == 'error_increase' and index % 4 == 0: row['error_code'] = 'INJECTED_E42'
                if kind == 'workload_mismatch': row['workload'] = 'MAN'
                if kind == 'hardware_mismatch': row['vehicle_class'] = 'hardware-B'
                if kind == 'missing_metrics': row.update(cpu='', memory_mb='')
                if kind == 'duration_mismatch': row['timestamp'] = 1700086400 + index * interval * 2
                if kind == 'sampling_offset_mismatch' and 0 < index < n - 1:
                    row['timestamp'] += interval * .49
            if kind == 'sample_count_mismatch':
                # Preserve duration; change density by over 10 percent.
                after = after[::2]
                if after[-1] is not before[-1] and after[-1]['timestamp'] != 1700086400 + (n-1)*interval:
                    after.append(dict(before[-1], version='candidate', timestamp=before[-1]['timestamp']+86400))
            scenarios.append(dict(id=identity, kind=kind,
                                  expected='refused' if kind in REFUSALS else 'regression' if kind in REGRESSIONS else 'no_regression',
                                  expected_metrics=['cpu'] if kind == 'cpu_increase' else ['memory_mb'] if kind == 'memory_increase' else ['error_rate'] if kind == 'error_increase' else [],
                                  baseline=before, candidate=after))
    return scenarios


def csv_text(rows):
    stream = io.StringIO()
    writer = csv.DictWriter(stream, fieldnames=regression.REQUIRED)
    writer.writeheader(); writer.writerows(rows)
    return stream.getvalue()


def evaluate_case(scenario, analyzer=regression.analyze, fixtures_dir=None):
    result = {key: scenario[key] for key in ('id', 'kind', 'expected', 'expected_metrics')}
    start = time.perf_counter()
    try:
        rows = []
        for version in ('baseline', 'candidate'):
            text = csv_text(scenario[version])
            source = f'synthetic:heldout/{scenario["id"]}/{version}.csv'
            if fixtures_dir is not None:
                path = Path(fixtures_dir) / scenario['id'] / f'{version}.csv'
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(text)
            rows.extend(regression.parse_csv(io.StringIO(text), source))
        report = analyzer(rows, 'baseline', 'candidate')
        predicted = 'refused' if report['status'] == 'not_comparable' else 'regression' if report['candidates'] else 'no_regression'
        locators = {(row['source'], row['line']): row for row in rows}
        evidence_valid = all((entry['source'], entry['line']) in locators and
                             all(entry[key] == locators[(entry['source'], entry['line'])][key]
                                 for key in ('timestamp', 'version', 'module', 'error_code', 'cpu', 'memory_mb'))
                             for entry in report['evidence'])
        if predicted != 'refused' and not report['evidence']:
            evidence_valid = False
        metrics = sorted({metric for candidate in report['candidates'] for metric in candidate['metrics']})
        causal_valid = all(candidate['causal_status'] == 'unproven' for candidate in report['candidates'])
        unknown_valid = scenario['kind'] != 'missing_metrics' or all(
            comparison[metric]['delta'] is None for comparison in report['comparisons'] for metric in ('cpu', 'memory_mb'))
        result.update(predicted=predicted, actual_metrics=metrics, evidence_valid=evidence_valid,
                      causal_claim_valid=causal_valid, missingness_valid=unknown_valid,
                      matched_samples=report['matched_samples'], evidence_count=len(report['evidence']),
                      passed=predicted == scenario['expected'] and metrics == sorted(scenario['expected_metrics']) and evidence_valid and causal_valid and unknown_valid)
    except Exception as exc:
        result.update(predicted='failed', passed=False, error=f'{type(exc).__name__}: {exc}')
    result['latency_ms'] = (time.perf_counter() - start) * 1000
    return result


def summarize(cases):
    labels = ('regression', 'no_regression', 'refused')
    matrix = {expected: {predicted: 0 for predicted in (*labels, 'failed')} for expected in labels}
    for case in cases:
        matrix[case['expected']][case['predicted']] += 1
    latency = sorted(case['latency_ms'] for case in cases)
    expected_counts = Counter(case['expected'] for case in cases)
    def rate(numerator, denominator):
        return numerator / denominator if denominator else None
    return dict(total_tasks=len(cases), passed_tasks=sum(case['passed'] for case in cases),
                failed_tasks=sum(case['predicted'] == 'failed' for case in cases),
                quality_all_tasks=rate(sum(case['passed'] for case in cases), len(cases)),
                detection_recall=rate(matrix['regression']['regression'], expected_counts['regression']),
                no_change_correct_rate=rate(matrix['no_regression']['no_regression'], expected_counts['no_regression']),
                unmatched_refusal_rate=rate(matrix['refused']['refused'], expected_counts['refused']),
                evidence_correct_tasks=sum(case.get('evidence_valid', False) for case in cases),
                evidence_correct_rate_all_tasks=rate(sum(case.get('evidence_valid', False) for case in cases), len(cases)),
                confusion_matrix=matrix,
                latency_ms=dict(samples=len(latency), mean=sum(latency)/len(latency) if latency else None,
                                p95=latency[min(len(latency)-1, math.floor(len(latency)*.95))] if latency else None))


def run(seed=20261013, repeats=6, fixtures_dir=None, analyzer=regression.analyze):
    cases = [evaluate_case(scenario, analyzer, fixtures_dir) for scenario in make_scenarios(seed, repeats)]
    return dict(schema_version=1, benchmark='heldout_synthetic_version_regression', seed=seed, repeats_per_kind=repeats,
                label_origin='Programmatic labels from independent synthetic fault-injection design.',
                scope='Observed-regression detection and incomparable-input refusal; no causal root-cause score.',
                limitations=['Synthetic correctness scenarios are not production gains or production accuracy.',
                             'Inputs pair identical per-sample noise; injected changes test arithmetic and comparison guards.',
                             'Latency covers CSV generation/parsing, optional fixture writes, and local analysis; environment-dependent.',
                             'Failures remain in every all-task denominator and the confusion matrix.'],
                summary=summarize(cases), scenarios=cases)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--seed', type=int, default=20261013)
    parser.add_argument('--repeats', type=int, default=6)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--fixtures-dir', type=Path)
    args = parser.parse_args()
    try:
        result = run(args.seed, args.repeats, args.fixtures_dir)
    except ValueError as exc:
        parser.error(str(exc))
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(result, indent=2, allow_nan=False) + '\n')
    print(json.dumps(result['summary'], indent=2))
    return 0 if result['summary']['passed_tasks'] == result['summary']['total_tasks'] else 1


if __name__ == '__main__':
    sys.exit(main())
