"""Independent, deterministic label scorer; no truth is sent to a model."""
import argparse
import json
import math
import pathlib
from ota_fault_pack import verify_suite

MODES = ('baseline', 'hybrid', 'agent')
CODES = {'client_timeout', 'server_slow', 'network_delay', 'retry_amplification', 'manifest_mismatch', 'artifact_corruption', 'unknown'}


def evidence_ids(value):
    ids = set()
    if isinstance(value, list):
        for item in value:
            ids |= evidence_ids(item)
    elif isinstance(value, dict):
        if isinstance(value.get('id'), str) and value['id'].startswith('log:'):
            ids.add(value['id'])
        for key, item in value.items():
            if key in ('evidence', 'Evidence', 'output', 'Output'):
                ids |= evidence_ids(item)
    elif isinstance(value, str):
        try:
            ids |= evidence_ids(json.loads(value))
        except (ValueError, TypeError):
            pass
    return ids


def score_attempt(case, label, run):
    if case['sha256'] != label['input_sha256']:
        raise ValueError('private truth does not bind this public snapshot')
    row = {'scenario_id': case['id'], 'missing_attempt': run is None}
    run = run or {}
    if run and run.get('input_sha256') != case['sha256']:
        raise ValueError('attempt used a different public snapshot')
    if case.get('full_input_sha256'):
        if label.get('full_input_sha256') != case['full_input_sha256'] or (run and run.get('full_input_sha256') != case['full_input_sha256']):
            raise ValueError('full OTA input does not match truth or attempt')
    result = run.get('result') or {}
    code = 'unknown'
    try:
        prediction = json.loads(result.get('RawLLMOutput', '{}'))
        proposed = prediction.get('cause_code', 'unknown')
        if proposed in CODES:
            code = proposed
    except (ValueError, TypeError):
        pass
    unresolved = run.get('status') == 'unresolved' or result.get('resolution_status') == 'unresolved'
    valid = run.get('valid_report', False) and run.get('status') == 'success'
    correct = bool(valid and not unresolved and code != 'unknown' and code == label['cause_code'])
    wrong = bool(valid and not unresolved and code != 'unknown' and code != label['cause_code'])
    gathered = evidence_ids(result.get('tool_trace', []))
    checkpoint = run.get('checkpoint') or {}
    if isinstance(checkpoint, dict):
        for key, item in (checkpoint.get('Evidence') or {}).items():
            if key.startswith('log:'):
                gathered.add(key)
    supplied = set()
    for request in run.get('request_tool_policy', []):
        if isinstance(request.get('http_status'), int) and 200 <= request['http_status'] < 300:
            supplied.update(request.get('supplied_evidence_ids', []))
    gathered |= supplied
    cited = set(result.get('evidence_ids') or [])
    known = {f'log:{i+1}' for i in range(len(case['lines']))}
    gathered &= known
    supplied &= known
    cited_logs = cited & known
    groups = [set(group) for group in label['key_evidence_groups']]
    acquired = bool(groups) and all(group & gathered for group in groups)
    exposed = bool(groups) and all(group & supplied for group in groups)
    used = bool(groups) and all(group & supplied & cited_logs for group in groups)
    if correct:
        gap = 'none' if used else 'correct_cause_without_complete_evidence_use'
    elif not acquired:
        gap = 'evidence_not_acquired'
    elif not exposed:
        gap = 'evidence_not_supplied'
    elif not valid and not unresolved:
        gap = 'reporting_or_model_failure'
    elif code == 'unknown' or unresolved:
        gap = 'unresolved_with_evidence'
    else:
        gap = 'reasoning_error'
    row.update(status=run.get('status', 'missing'), failed=not valid and not unresolved, unresolved=unresolved,
               cause_code=code, root_cause_correct=correct, wrong_attribution=wrong,
               no_specific_cause=not correct and not wrong, key_evidence_acquired=acquired, key_evidence_used=used, key_evidence_supplied=exposed,
               key_evidence_acquisition_fraction=sum(bool(g & gathered) for g in groups) / len(groups) if groups else 0,
               key_evidence_use_fraction=sum(bool(g & supplied & cited_logs) for g in groups) / len(groups) if groups else 0,
               invented_log_citations=sorted(id for id in cited if id.startswith('log:') and id not in known),
               diagnostic_gap=gap, actual_http_calls=run.get('actual_http_calls'),
               latency_ms=run.get('latency_ms'), cost_usd=run.get('cost_usd') if run.get('cost_complete') else None,
               actual_route=run.get('actual_route', 'unknown'), model=run.get('model'))
    row['citation_sources'] = {id: case.get('sources', [])[int(id[4:])-1] for id in cited_logs if len(case.get('sources', [])) >= int(id[4:])}
    usage_rows = run.get('request_tool_policy', [])
    def valid_usage(request):
        usage = request.get('usage') or {}
        values = [usage.get(key) for key in ('prompt_tokens', 'completion_tokens', 'total_tokens')]
        return all(isinstance(v, int) and not isinstance(v, bool) and v >= 0 for v in values) and values[0] + values[1] == values[2]
    row['usage_complete'] = bool(not row['missing_attempt'] and len(usage_rows) == run.get('actual_http_calls') and all(valid_usage(r) for r in usage_rows))
    row['recorded_tokens'] = sum(r.get('usage', {}).get('total_tokens', 0) for r in usage_rows)
    row['actual_429_responses'] = sum(r.get('http_status') == 429 for r in usage_rows)
    return row


def percentile(values):
    values = sorted(values)
    return values[math.ceil(len(values) * .95) - 1] if values else None


def summarize_mode(rows):
    n = len(rows)
    calls = [r['actual_http_calls'] for r in rows]
    costs = [r['cost_usd'] for r in rows]
    latencies = [r['latency_ms'] for r in rows]
    recorded_calls = [x for x in calls if x is not None]
    recorded_latency = [x for x in latencies if x is not None]
    def rate(key):
        return sum(bool(r[key]) for r in rows) / n if n else None
    return {'scheduled_attempts': n, 'missing_attempts': sum(r['missing_attempt'] for r in rows),
            'failed_attempts': sum(r['failed'] for r in rows), 'unresolved_attempts': sum(r['unresolved'] for r in rows),
            'root_cause_accuracy': rate('root_cause_correct'), 'wrong_attribution_rate': rate('wrong_attribution'),
            'no_specific_cause_rate': rate('no_specific_cause'),
            'key_evidence_acquisition_rate': rate('key_evidence_acquired'), 'key_evidence_supplied_rate': rate('key_evidence_supplied'), 'key_evidence_use_rate': rate('key_evidence_used'),
            'recorded_http_calls': sum(recorded_calls),
            'mean_actual_http_calls': sum(recorded_calls) / n if n and len(recorded_calls) == n else None,
            'mean_cost_usd': sum(costs) / n if n and all(x is not None for x in costs) else None,
            'latency_p95_ms': percentile(recorded_latency) if len(recorded_latency) == n else None,
            'recorded_latency_p95_ms': percentile(recorded_latency),
            'recorded_provider_tokens': sum(r['recorded_tokens'] for r in rows),
            'usage_complete': all(r['usage_complete'] for r in rows),
            'actual_429_responses': sum(r['actual_429_responses'] for r in rows),
            'route_counts': {k: sum(r['actual_route'] == k for r in rows) for k in sorted({r['actual_route'] for r in rows})},
            'diagnostic_gaps': {k: sum(r['diagnostic_gap'] == k for r in rows) for k in sorted({r['diagnostic_gap'] for r in rows})}}


def score_runs(suite, labels, runs, trials=3):
    if trials not in (1, 2, 3):
        raise ValueError('trials must be 1..3')
    if suite.get('task') == 'ota':
        verify_suite(suite)
    truth = {c['id']: c for c in labels['cases']}
    if len(truth) != len(labels['cases']):
        raise ValueError('duplicate private labels')
    ids = [c['id'] for c in suite['cases']]
    if len(ids) != len(set(ids)):
        raise ValueError('duplicate public cases')
    expected = {(mode, case, trial) for mode in MODES for case in ids for trial in range(trials)}
    if any(key not in expected for key in runs):
        raise ValueError('attempt outside scheduled cases/trials')
    protocol_keys = ('model', 'provider_endpoint', 'max_steps', 'reservation_budget', 'max_context_chars')
    protocols = set()
    provider_models = set()
    for run in runs.values():
        if run.get('benchmark_revision') != suite.get('revision'):
            raise ValueError('attempt benchmark revision mismatch')
        if any(key not in run for key in protocol_keys):
            raise ValueError('attempt lacks fixed model/budget protocol')
        protocols.add(tuple(run[key] for key in protocol_keys) + (json.dumps(run.get('pricing'), sort_keys=True),))
        for response in run.get('request_tool_policy', []):
            if response.get('provider_model'):
                provider_models.add(response['provider_model'])
    if len(protocols) > 1 or len(provider_models) > 1:
        raise ValueError('model/provider/budget changed across compared attempts')
    attempts = []
    for mode in MODES:
        for case in suite['cases']:
            for trial in range(trials):
                row = score_attempt(case, truth[case['id']], runs.get((mode, case['id'], trial)))
                hybrid_run = runs.get(('hybrid', case['id'], trial)) or {}
                route = hybrid_run.get('actual_route', 'unknown')
                row.update(mode=mode, trial=trial,
                           hybrid_route=route if route in ('fixed', 'semantic', 'agent') else 'unknown')
                attempts.append(row)
    return {'scope': 'controlled local HTTP faults; categorical top candidate and evidence rubric, not production accuracy',
            'live_model_status': 'not_run' if not runs else 'recorded_runs',
            'provider_reported_models': sorted(provider_models),
            'provider_model_consistent': len(provider_models) == 1,
            'model_revision_status': 'provider-reported model name only; immutable revision not attested',
            'semantic_review_status': 'not performed: categorical cause and evidence rubric do not prove narrative correctness',
            'protocol': dict(zip(protocol_keys + ('pricing_json',), next(iter(protocols)))) if protocols else None,
            'summary_by_hybrid_route': {
                route: {m: summarize_mode([r for r in attempts if r['mode'] == m and r['hybrid_route'] == route]) for m in MODES}
                for route in ('fixed', 'semantic', 'agent', 'unknown')},
            'summary': {m: summarize_mode([r for r in attempts if r['mode'] == m]) for m in MODES}, 'attempts': attempts}


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--input', required=True)
    p.add_argument('--truth', required=True)
    p.add_argument('--runs', required=True, help='root containing baseline/hybrid/agent directories')
    p.add_argument('--trials', type=int, default=3)
    p.add_argument('--out', required=True)
    args = p.parse_args()
    runs = {}
    for mode in MODES:
        for path in (pathlib.Path(args.runs) / mode).glob('scenario-*/*/outputs/argus_run.json'):
            row = json.loads(path.read_text())
            if row.get('mode') != mode:
                raise ValueError('mode/path mismatch')
            key = (mode, row['scenario_id'], row['trial'])
            if key in runs:
                raise ValueError('duplicate attempt')
            runs[key] = row
    report = score_runs(json.loads(pathlib.Path(args.input).read_text()), json.loads(pathlib.Path(args.truth).read_text()), runs, args.trials)
    out = pathlib.Path(args.out)
    with out.open('x') as f:
        json.dump(report, f, ensure_ascii=False, indent=2)
    print(json.dumps(report['summary'], ensure_ascii=False))


if __name__ == '__main__':
    main()
