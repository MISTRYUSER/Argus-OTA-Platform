"""Summarize all benchmark trials without filtering failed executions.

Valid JSON reports measure harness completion, not root-cause correctness.
Strict entity metrics use only score_entities.py's namespace/kind/regex rubric.
Missing measurements remain unknown; rates with missing scores are lower bounds.
Only the explicit official root_cause_entity.calculation_f1 KPI is aggregated.
No model calls, credentials, or third-party packages are used.
"""
import argparse
import collections
import hashlib
import json
import math
import pathlib
import re
import statistics


STRICT_FIELDS = ('hit_at_1', 'precision', 'recall', 'f1')
RETRIEVAL_TOOLS = ('search_history', 'search_similar_cases')


def identity(row):
    case = row.get('scenario_id', row.get('incident_id', row.get('case_id')))
    trial = row.get('trial', row.get('trial_id'))
    if case is None or trial is None:
        raise ValueError('every row requires case identity and trial')
    return str(case), str(trial)


def read_source(path):
    path = pathlib.Path(path).resolve()
    blob = path.read_bytes()
    def reject_nonfinite(value):
        raise ValueError(f'input JSON requires finite numbers, found {value}')
    return json.loads(blob, parse_constant=reject_nonfinite), {'path': str(path), 'sha256': hashlib.sha256(blob).hexdigest()}


def number(value, label, maximum=None, integer=False):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ValueError(f'{label} must be a finite number')
    if value < 0 or (maximum is not None and value > maximum) or (integer and int(value) != value):
        raise ValueError(f'{label} outside valid range')
    return value


def rate(numerator, denominator):
    return {'numerator': numerator, 'denominator': denominator,
            'rate': numerator / denominator if denominator else None}


def measurements(values, tasks_total):
    observed = [value for value in values if value is not None]
    return {'coverage': rate(len(observed), tasks_total),
            'total': sum(observed) if len(observed) == tasks_total else None,
            'observed_total': sum(observed),
            'mean_all_tasks': statistics.mean(observed) if observed and len(observed) == tasks_total else None,
            'mean_observed': statistics.mean(observed) if observed else None}


def indexed_scores(path, kind, task_keys):
    if path is None:
        return {}, None
    data, receipt = read_source(path)
    rows = data if isinstance(data, list) else data.get('rows', data.get('raw_results'))
    if not isinstance(rows, list):
        raise ValueError(f'{kind} input requires rows/raw_results list')
    index = {}
    for row in rows:
        if not isinstance(row, dict):
            raise ValueError(f'{kind} row must be an object')
        key = identity(row)
        if key in index:
            raise ValueError(f'duplicate {kind} score row for {key}')
        if key not in task_keys:
            raise ValueError(f'{kind} score without task: {key}')
        if kind == 'strict':
            valid = row.get('score_valid', True)
            if not isinstance(valid, bool):
                raise ValueError('score_valid must be boolean')
            if not valid:
                if any(field not in row or row[field] is not None for field in STRICT_FIELDS):
                    raise ValueError('unscorable strict row requires null metrics')
                errors = row.get('errors', [])
                has_error = isinstance(errors, list) and any(
                    isinstance(error, str) and bool(error.strip()) or
                    isinstance(error, dict) and isinstance(error.get('error'), str) and bool(error['error'].strip())
                    for error in errors)
                if not has_error and not (isinstance(row.get('error'), str) and row['error'].strip()):
                    raise ValueError('unscorable strict row requires explicit error')
            else:
                for field in STRICT_FIELDS:
                    number(row.get(field), f'strict.{field}', 1, integer=field == 'hit_at_1')
                if 'matched_root_groups' in row and not isinstance(row['matched_root_groups'], list):
                    raise ValueError('matched_root_groups must be a list')
        index[key] = row
    return index, receipt


def observations(row):
    result = row.get('result')
    if isinstance(result, dict) and isinstance(result.get('tool_trace'), list):
        return result['tool_trace'], 'result.tool_trace'
    checkpoint = row.get('checkpoint')
    if isinstance(checkpoint, dict) and isinstance(checkpoint.get('Observations'), list):
        return checkpoint['Observations'], 'checkpoint.Observations'
    return [], 'unobserved'


def parse_output(output):
    if isinstance(output, str):
        try:
            output = json.loads(output)
        except (ValueError, TypeError):
            return None
    return output if isinstance(output, dict) else None


def model_steps(row):
    result = row.get('result')
    if isinstance(result, dict) and result.get('model_steps') is not None:
        return number(result['model_steps'], 'model_steps', integer=True)
    checkpoint = row.get('checkpoint')
    if isinstance(checkpoint, dict) and checkpoint.get('Step') is not None:
        return number(checkpoint['Step'], 'checkpoint.Step', integer=True)
    return None


def provider(row, field):
    stats = row.get('provider_stats')
    value = stats.get(field) if isinstance(stats, dict) else None
    return number(value, 'provider.' + field, integer=True) if value is not None else None


def status(attempt):
    if not isinstance(attempt, dict):
        raise ValueError('HTTP attempt must be an object')
    for field in ('http_status', 'status_code', 'http_status_code'):
        if field in attempt:
            value = number(attempt[field], field, maximum=599, integer=True)
            if value < 100:
                raise ValueError('invalid HTTP status')
            return value
    return None


def tools_summary(calls, tasks_total):
    by = collections.defaultdict(list)
    for call in calls:
        by[call['tool']].append(call)
    def summarize_calls(selected):
        total = len(selected)
        return {'task_use': rate(len({call['identity'] for call in selected}), tasks_total),
                'calls': total,
                'error_calls': rate(sum(call['error'] for call in selected), total),
                'unavailable_calls': rate(sum(call['unavailable'] for call in selected), total),
                'nonempty_calls': rate(sum(call['nonempty'] for call in selected), total),
                'output_parse_coverage': rate(sum(call['output_parsed'] for call in selected), total)}
    result = {name: summarize_calls(selected) for name, selected in sorted(by.items())}
    retrieval = summarize_calls([call for call in calls if call['tool'] in RETRIEVAL_TOOLS])
    retrieval['tool_names'] = list(RETRIEVAL_TOOLS)
    retrieval['definition'] = 'search_history is the current runner tool; search_similar_cases is accepted as a recorded alias'
    return result, retrieval


def summarize(runs, strict_scores, official_scores=None, expected_cases=None, expected_trials_per_case=None):
    runs = pathlib.Path(runs)
    paths = sorted(runs.glob('scenario-*/*/outputs/argus_run.json')) if runs.is_dir() else [runs]
    if not paths:
        raise ValueError('no benchmark tasks found')
    loaded = {}
    for path in paths:
        row, receipt = read_source(path)
        if not isinstance(row, dict):
            raise ValueError('argus_run.json must contain one task object')
        key = identity(row)
        if key in loaded:
            raise ValueError(f'duplicate task identity {key}')
        if not isinstance(row.get('valid_report'), bool):
            raise ValueError('valid_report must be an explicit boolean')
        loaded[key] = row, receipt
    total = len(loaded)
    cases = {key[0] for key in loaded}
    if expected_cases is not None and len(cases) != expected_cases:
        raise ValueError(f'expected {expected_cases} cases, found {len(cases)}')
    if expected_trials_per_case is not None:
        number(expected_trials_per_case, 'expected_trials_per_case', integer=True)
        if expected_trials_per_case < 1:
            raise ValueError('expected_trials_per_case must be positive')
        expected_trials_per_case = int(expected_trials_per_case)
        expected_trials = {str(trial) for trial in range(expected_trials_per_case)}
        for case in sorted(cases):
            actual_trials = {key[1] for key in loaded if key[0] == case}
            if actual_trials != expected_trials:
                raise ValueError(f'case {case} trial set mismatch: missing={sorted(expected_trials - actual_trials)} extra={sorted(actual_trials - expected_trials)}')
    strict, strict_source = indexed_scores(strict_scores, 'strict', loaded)
    official, official_source = indexed_scores(official_scores, 'official', loaded)
    tasks, calls, latencies, steps = [], [], [], []
    attempts, retries, rejected, tokens, statuses_per_task, attempts_observed, transport_errors = [], [], [], [], [], [], []
    for key, (row, receipt) in sorted(loaded.items()):
        trace = row.get('trace_id') or row.get('execution_trace_id')
        result = row.get('result')
        if not trace and isinstance(result, dict):
            trace = result.get('execution_trace_id')
        traceable = bool(isinstance(trace, str) and re.fullmatch('[a-fA-F0-9]{32}', trace) and int(trace, 16))
        obs, origin = observations(row)
        task_calls = []
        for observation in obs:
            if not isinstance(observation, dict) or not isinstance(observation.get('tool'), str):
                raise ValueError('tool observation requires tool name')
            output = parse_output(observation.get('output'))
            unavailable = output is not None and output.get('unavailable') is True
            error = bool(observation.get('error')) or bool(output and output.get('error'))
            nonempty = (output is not None and isinstance(output.get('evidence'), list)
                        and bool(output['evidence']) and not unavailable and not error)
            call = {'identity': key, 'tool': observation['tool'], 'error': error,
                    'unavailable': unavailable, 'nonempty': nonempty, 'output_parsed': output is not None}
            calls.append(call)
            task_calls.append({k: v for k, v in call.items() if k != 'identity'})
        latency = row.get('latency_ms')
        latency = number(latency, 'latency_ms') if latency is not None else None
        latencies.append(latency)
        step = model_steps(row)
        steps.append(step)
        for values, field in ((attempts, 'Attempts'), (retries, 'Retries'), (rejected, 'Rejected'), (tokens, 'Tokens')):
            values.append(provider(row, field))
        raw_attempts = row.get('request_tool_policy')
        if raw_attempts is not None and not isinstance(raw_attempts, list):
            raise ValueError('request_tool_policy must be a list')
        observed = [status(attempt) for attempt in raw_attempts] if isinstance(raw_attempts, list) else []
        statuses_per_task.append(observed)
        attempts_observed.append(len(raw_attempts) if isinstance(raw_attempts, list) else None)
        # An explicit transport error records an attempt without an HTTP response.
        transport_errors.append([attempt.get('transport_error') is True for attempt in raw_attempts]
                                if isinstance(raw_attempts, list) else [])
        score = strict.get(key)
        tasks.append({'case': key[0], 'trial': key[1], 'source': receipt,
                      'strict_source': strict_source if score is not None else None,
                      'official_source': official_source if key in official else None,
                      'valid_report': row['valid_report'], 'error': row.get('error'),
                      'trace_id': trace if traceable else None, 'traceable': traceable,
                      'observation_source': origin, 'tools': task_calls,
                      'latency_ms': latency, 'model_steps': step,
                      'provider_stats': row.get('provider_stats'), 'strict': score,
                      'official': official.get(key), 'benchmark_revision': row.get('benchmark_revision'),
                      'input_sha256': row.get('input_sha256'), 'model': row.get('model'), 'mode': row.get('mode')})
    scorable = {key: row for key, row in strict.items() if row.get('score_valid', True)}
    score_values = {field: [scorable[key].get(field) if key in scorable else None for key in loaded] for field in STRICT_FIELDS}
    strict_summary = {'definition': 'strict namespace/kind/GT regex+aliases, contributing_factor=true; entity match does not prove causal reasoning',
                      'rows_coverage': rate(len(strict), total),
                      'coverage': rate(len(scorable), total),
                      'unknown_scores': [{'case': key[0], 'trial': key[1],
                                          'errors': strict[key].get('errors', [strict[key].get('error')]) if key in strict else ['missing_score_row']}
                                         for key in sorted(loaded) if key not in scorable]}
    for field in ('precision', 'recall', 'f1'):
        strict_summary[field] = measurements(score_values[field], total)
    any_hits = [bool(score.get('matched_root_groups')) if 'matched_root_groups' in score else score['recall'] > 0 for score in scorable.values()]
    for field, count in (('hit_at_1', sum(score_values['hit_at_1'][i] or 0 for i in range(total))), ('any_gold_hit', sum(any_hits))):
        strict_summary[field] = {**rate(count, total), 'known_scores': len(scorable),
                                 'interpretation': 'complete_scores' if len(scorable) == total else 'lower_bound_missing_scores'}
    all_statuses = [value for observed in statuses_per_task for value in observed]
    status_known = [value for value in all_statuses if value is not None]
    http429, transport_counts = [], []
    for i, observed in enumerate(statuses_per_task):
        capture_complete = attempts_observed[i] is not None and (attempts[i] is None or attempts[i] == attempts_observed[i])
        classified = all(value is not None or transport_errors[i][j] for j, value in enumerate(observed))
        complete = capture_complete and classified
        http429.append(sum(value == 429 for value in observed) if complete else (0 if attempts[i] == 0 else None))
        transport_counts.append(sum(transport_errors[i]) if complete else (0 if attempts[i] == 0 else None))
    performance = {'model_steps': measurements(steps, total),
                   'http_attempts': measurements(attempts, total),
                   'recorded_http_attempts': measurements(attempts_observed, total),
                   'provider_retries': measurements(retries, total),
                   'queue_rejections': measurements(rejected, total),
                   'provider_reported_tokens': measurements(tokens, total),
                   'http_429': measurements(http429, total),
                   'http_transport_errors': measurements(transport_counts, total),
                   'http_status_coverage': rate(len(status_known), len(all_statuses))}
    performance['http_attempt_capture_coverage'] = rate(sum(value or 0 for value in attempts_observed), sum(attempts)) if all(value is not None for value in attempts) else {'numerator': sum(value or 0 for value in attempts_observed), 'denominator': None, 'rate': None}
    performance['http_attempt_capture_coverage']['definition'] = 'recorded attempts / provider-reported attempts; mismatch leaves total HTTP429 unknown'
    performance['http_429']['observed_count'] = sum(value == 429 for value in status_known)
    performance['http_429']['definition'] = 'actual HTTP 429 responses; absent attempt statuses are unknown, not inferred from retries'
    performance['http_status_coverage']['denominator_definition'] = 'all recorded HTTP attempts, including attempts lacking statuses'
    observed_latencies = sorted(value for value in latencies if value is not None)
    performance['latency_ms'] = {**measurements(latencies, total),
                               'p50': statistics.median(observed_latencies) if observed_latencies else None,
                               'p95_nearest_rank': observed_latencies[math.ceil(.95 * len(observed_latencies))-1] if observed_latencies else None,
                               'quantile_denominator': len(observed_latencies)}
    tools, retrieval = tools_summary(calls, total)
    retrieval['nonempty_definition'] = 'call returned at least one evidence item, with neither tool error nor unavailable=true; denominator includes every retrieval call'
    official_summary = None
    if official_source is not None:
        values = []
        for key in loaded:
            scores = official.get(key, {}).get('scores', {})
            entity = scores.get('root_cause_entity', {}) if isinstance(scores, dict) else {}
            value = entity.get('calculation_f1') if isinstance(entity, dict) else None
            values.append(number(value, 'official.root_cause_entity.calculation_f1', 1) if value is not None else None)
        official_summary = {'source': official_source, 'rows_coverage': rate(len(official), total),
                            'root_cause_entity.calculation_f1': measurements(values, total),
                            'definition': 'official entity F1 only; missing/error scores remain unknown; other rubric fields retained per task'}
    return {'version': 1, 'tasks_total': total, 'cases_total': len(cases),
            'denominator_definition': 'all recorded case/trial tasks, including invalid reports and failed executions',
            'missing_measurements': 'null means unknown; observed totals/means retain measured rows; all-task totals/means require complete coverage',
            'valid_reports': {**rate(sum(task['valid_report'] for task in tasks), total),
                              'definition': 'harness produced valid report; distinct from root-cause task success'},
            'strict': strict_summary, 'official': official_summary,
            'tool_bearing_tasks': rate(len({call['identity'] for call in calls}), total),
            'observation_coverage': rate(sum(task['observation_source'] != 'unobserved' for task in tasks), total),
            'tools': tools, 'retrieval': retrieval, 'performance': performance,
            'traceable_tasks': {**rate(sum(task['traceable'] for task in tasks), total),
                                'definition': 'nonzero W3C execution trace ID recorded; does not establish backend export/materialization'},
            'tasks': tasks}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runs', type=pathlib.Path, required=True)
    parser.add_argument('--strict-scores', type=pathlib.Path, required=True)
    parser.add_argument('--official-scores', type=pathlib.Path)
    parser.add_argument('--expected-cases', type=int)
    parser.add_argument('--expected-trials-per-case', type=int,
                        help='require every case to have exactly zero-based trials 0..N-1')
    parser.add_argument('--out', type=pathlib.Path)
    args = parser.parse_args()
    result = summarize(args.runs, args.strict_scores, args.official_scores, args.expected_cases, args.expected_trials_per_case)
    output = json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False) + '\n'
    if args.out:
        args.out.write_text(output)
    else:
        print(output, end='')


if __name__ == '__main__':
    main()
