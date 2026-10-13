"""Dry-run-first offline experiment import via Langfuse v4 OTLP and Scores API.

A snapshot trace is explicitly an import, never a reconstructed execution.
If the original trace ID is available, scores also link directly to that trace.
Uses only the standard library; no model/provider calls or implicit credentials.
"""
import argparse
import base64
import hashlib
import json
import math
import os
import pathlib
import re
import time
import urllib.error
import urllib.parse
import urllib.request


def stable_id(*parts, length=32):
    return hashlib.sha256(json.dumps(parts, sort_keys=True).encode()).hexdigest()[:length]


def identity(row):
    case = row.get('scenario_id', row.get('incident_id', row.get('case_id')))
    trial = row.get('trial', row.get('trial_id', row.get('run_id')))
    if case is None or trial is None:
        raise ValueError('each row requires scenario_id/incident_id/case_id and trial/trial_id/run_id')
    return str(case), str(trial)


def read_source(path):
    blob = path.read_bytes()
    return json.loads(blob), {'path': str(path), 'sha256': hashlib.sha256(blob).hexdigest()}


def load_rows(path, kind):
    """Supports argus_run.json trees, JSON row lists, strict rows, official raw_results."""
    paths = sorted(path.glob('scenario-*/*/outputs/argus_run.json')) if path.is_dir() else [path]
    rows, sources = [], []
    for source in paths:
        data, receipt = read_source(source)
        sources.append(receipt)
        values = data if isinstance(data, list) else data.get('rows', data.get('raw_results', [data]))
        for row in values:
            if not isinstance(row, dict):
                raise ValueError('artifact rows must be objects')
            identity(row)
            rows.append((row, receipt))
    if not paths:
        raise ValueError('no argus_run.json artifacts found')
    return rows, sources


def numeric_scores(row, kind):
    if kind == 'strict':
        values = {k: row[k] for k in ['hit_at_1', 'precision', 'recall', 'f1'] if k in row}
    elif kind == 'official':
        values = {}
        def visit(prefix, value):
            if isinstance(value, dict):
                for key, item in value.items():
                    visit(prefix + '.' + key if prefix else key, item)
            elif isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value):
                values[prefix] = value
        visit('', row.get('scores', {}))
    else:
        values = {}
        if 'valid_report' in row:
            values['valid_report'] = int(bool(row['valid_report']))
        if isinstance(row.get('latency_ms'), (int, float)):
            values['latency_ms'] = row['latency_ms']
        for key in ['Attempts', 'Tokens', 'Retries']:
            value = row.get('provider_stats', {}).get(key)
            if isinstance(value, (int, float)):
                values['provider.' + key.lower()] = value
    return {kind + '.' + k: float(v) for k, v in values.items()
            if isinstance(v, (int, float)) and math.isfinite(v)}


def score_annotation(name, value):
    """Keep upstream components unchanged while separating them from headlines."""
    if not name.startswith('official.'):
        return name, {}, None
    source_path = name[len('official.'):]
    rubric, _, field = source_path.partition('.')
    k_variant = rubric.startswith('root_cause_entity_k')
    bounded = field in ('calculation_precision', 'calculation_recall', 'calculation_f1') or source_path == 'root_cause_reasoning.calculation'
    range_valid = 0 <= value <= 1 if bounded else None
    primary = source_path in ('root_cause_entity.calculation_f1', 'root_cause_reasoning.calculation')
    metadata = {
        'raw_upstream_component': True,
        'score_source_path': source_path,
        'expected_range': [0, 1] if bounded else None,
        'score_range_validation': ('within_expected_range' if range_valid else 'outside_expected_range') if bounded else 'not_a_bounded_metric',
        'k_variant': k_variant,
        'headline_component_eligible': bool(primary and range_valid),
        'validated_headline_metric': False,
        'excluded_from_validated_headline': bool(k_variant or not primary or range_valid is False),
    }
    comment = 'Raw upstream component, preserved unchanged; not a headline accuracy metric. '
    if k_variant:
        comment += 'K variant excluded from validated headline metrics. '
    if range_valid is False:
        comment += 'Upstream value outside expected [0,1]; not clamped.'
    else:
        comment += 'Aggregate definitions and missing-score coverage remain in the report.'
    return 'official.raw.' + source_path, metadata, comment


def attribute(key, value):
    return {'key': key, 'value': {'stringValue': str(value)}}


def build_plan(runs, score_sources, *, dataset, run, release, model, timestamp_ns=None):
    """One imported snapshot per case/trial, all failure rows remain in the plan."""
    now = timestamp_ns if timestamp_ns is not None else time.time_ns()
    cases = {}
    sources = []
    for kind, path in [('run', runs)] + list(score_sources):
        rows, receipts = load_rows(path, kind)
        sources.extend(receipts)
        for row, receipt in rows:
            key = identity(row)
            case = cases.setdefault(key, {'raw': {}, 'receipts': [], 'scores': {}, 'trace_id': None, 'span_id': None})
            if kind in case['raw']:
                raise ValueError(f'duplicate {kind} row for {key}; select a single experiment input')
            case['raw'][kind] = row
            case['receipts'].append(receipt)
            case['scores'].update(numeric_scores(row, kind))
            trace = row.get('trace_id') or row.get('execution_trace_id')
            if trace:
                if not re.fullmatch('[a-fA-F0-9]{32}', str(trace)) or int(trace, 16) == 0:
                    raise ValueError('trace_id must be a nonzero W3C 32 character hex ID')
                trace = trace.lower()
                if case['trace_id'] and case['trace_id'] != trace:
                    raise ValueError(f'conflicting execution trace IDs for {key}')
                case['trace_id'] = trace
            span = row.get('root_span_id')
            if span:
                if not re.fullmatch('[a-fA-F0-9]{16}', str(span)) or int(span, 16) == 0:
                    raise ValueError('root_span_id must be a nonzero 16 character hex ID')
                span = span.lower()
                if case['span_id'] and case['span_id'] != span:
                    raise ValueError(f'conflicting root span IDs for {key}')
                case['span_id'] = span
    requests, manifest = [], []
    experiment_id = stable_id(dataset, run, release, model)
    for (case_id, trial), case in sorted(cases.items()):
        snapshot_trace = stable_id('import', experiment_id, case_id, trial)
        snapshot_span = stable_id('snapshot', experiment_id, case_id, trial, length=16)
        item_id = stable_id(dataset, case_id, trial)
        execution_trace = case['trace_id']
        metadata = {'dataset': dataset, 'case': case_id, 'trial': trial, 'run': run,
                    'release': release, 'model': model, 'execution_trace_id': execution_trace,
                    'import_snapshot': True, 'source_receipts': case['receipts']}
        attrs = {
            'langfuse.experiment.id': experiment_id,
            'langfuse.experiment.name': run,
            'langfuse.experiment.dataset.id': dataset,
            'langfuse.experiment.item.id': item_id,
            'langfuse.experiment.item.root_observation_id': snapshot_span,
            'langfuse.environment': 'experiment',
            'langfuse.release': release,
            'langfuse.observation.type': 'evaluator',
            'langfuse.observation.input': json.dumps({'case': case_id, 'trial': trial}),
            'langfuse.observation.output': json.dumps(case['raw'], ensure_ascii=False),
        }
        for key, value in metadata.items():
            attrs['langfuse.experiment.item.metadata.' + key] = json.dumps(value)
            attrs['langfuse.trace.metadata.' + key] = json.dumps(value)
        failed = case['raw'].get('run', {}).get('valid_report') is False
        span = {'traceId': snapshot_trace, 'spanId': snapshot_span,
                'name': 'argus.eval.import_snapshot', 'kind': 1,
                'startTimeUnixNano': str(now), 'endTimeUnixNano': str(now + 1),
                'attributes': [attribute(k, v) for k, v in attrs.items()],
                'status': {'code': 2 if failed else 1, 'message': 'imported failed execution' if failed else ''}}
        payload = {'resourceSpans': [{'resource': {'attributes': [attribute('service.name', 'argus-eval-import')]},
                    'scopeSpans': [{'scope': {'name': 'argus.eval.import'}, 'spans': [span]}]}]}
        requests.append({'path': '/api/public/otel/v1/traces', 'body': payload, 'case': case_id, 'trial': trial})
        for name, value in sorted(case['scores'].items()):
            display_name, annotation, comment = score_annotation(name, value)
            # Scores visible in experiment snapshot and original execution, if recorded.
            targets = [(snapshot_trace, snapshot_span)]
            if execution_trace:
                targets.append((execution_trace, case['span_id']))
            for trace_id, observation_id in targets:
                body = {'id': stable_id(experiment_id, case_id, trial, name, trace_id),
                        'name': display_name, 'value': value, 'dataType': 'NUMERIC',
                        'traceId': trace_id, 'environment': 'experiment', 'metadata': {**metadata, **annotation}}
                if comment:
                    body['comment'] = comment
                if observation_id:
                    body['observationId'] = observation_id
                requests.append({'path': '/api/public/scores', 'body': body, 'case': case_id, 'trial': trial})
        manifest.append({**metadata, 'snapshot_trace_id': snapshot_trace, 'snapshot_span_id': snapshot_span,
                         'failure': failed, 'raw': case['raw']})
    return {'version': 1, 'transport': 'Langfuse v4 OTLP HTTP JSON + single-score REST',
            'experiment_id': experiment_id, 'sources': sources, 'cases': manifest, 'requests': requests}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def send_plan(plan, endpoint, public_key, secret_key, timeout=10):
    parts = urllib.parse.urlsplit(endpoint)
    if parts.scheme not in ('http', 'https') or not parts.hostname or parts.username or parts.password or parts.query or parts.fragment:
        raise ValueError('endpoint must be a plain HTTP(S) base URL without credentials/query/fragment')
    if parts.scheme == 'http' and parts.hostname not in ('localhost', '127.0.0.1', '::1'):
        raise ValueError('remote endpoints require HTTPS')
    auth = base64.b64encode((public_key + ':' + secret_key).encode()).decode()
    opener = urllib.request.build_opener(NoRedirect())
    outcomes = []
    for index, item in enumerate(plan['requests']):
        req = urllib.request.Request(endpoint.rstrip('/') + item['path'],
            data=json.dumps(item['body'], allow_nan=False).encode(), method='POST',
            headers={'Content-Type': 'application/json', 'Authorization': 'Basic ' + auth,
                     'x-langfuse-ingestion-version': '4'})
        result = {'index': index, 'path': item['path'], 'case': item['case'], 'trial': item['trial']}
        try:
            with opener.open(req, timeout=timeout) as response:
                body = response.read(65537)
                if len(body) > 65536:
                    raise ValueError('response exceeds 64 KiB')
                parsed = json.loads(body) if body else {}
                partial = parsed.get('partialSuccess', {})
                if response.status == 207 or int(partial.get('rejectedSpans', 0)) > 0:
                    raise ValueError('backend reports partial rejection')
                if item['path'].endswith('/scores') and not parsed.get('id'):
                    raise ValueError('score response missing created ID')
                result.update(ok=True, status=response.status)
        except urllib.error.HTTPError as error:
            # Retain status, not untrusted response body which could echo credentials.
            result.update(ok=False, status=error.code, error='HTTP request rejected')
        except Exception as error:
            result.update(ok=False, error=type(error).__name__)
        outcomes.append(result)
    return outcomes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runs', type=pathlib.Path, required=True)
    parser.add_argument('--strict-scores', type=pathlib.Path)
    parser.add_argument('--official-scores', type=pathlib.Path)
    for name in ['dataset', 'run', 'release', 'model']:
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--out', type=pathlib.Path, required=True)
    parser.add_argument('--write', action='store_true', help='explicitly send using LANGFUSE_* environment credentials')
    args = parser.parse_args()
    score_sources = [(kind, path) for kind, path in [('strict', args.strict_scores), ('official', args.official_scores)] if path]
    plan = build_plan(args.runs, score_sources, dataset=args.dataset, run=args.run, release=args.release, model=args.model)
    # Save reviewable evidence before attempting writes. Credentials never enter it.
    args.out.write_text(json.dumps(plan, ensure_ascii=False, indent=2, allow_nan=False) + '\n')
    if args.write:
        endpoint, public, secret = [os.environ.get(k) for k in ['LANGFUSE_BASE_URL', 'LANGFUSE_PUBLIC_KEY', 'LANGFUSE_SECRET_KEY']]
        if not all([endpoint, public, secret]):
            parser.error('--write requires LANGFUSE_BASE_URL, LANGFUSE_PUBLIC_KEY, LANGFUSE_SECRET_KEY')
        results = send_plan(plan, endpoint, public, secret)
        receipt = args.out.with_suffix(args.out.suffix + '.receipts.json')
        receipt.write_text(json.dumps(results, indent=2) + '\n')
        print(f'{len(results)} requests, {sum(not r["ok"] for r in results)} failed; receipts: {receipt}')
        if any(not row['ok'] for row in results):
            raise SystemExit(1)
    else:
        print(f'Dry run: {len(plan["cases"])} cases, {len(plan["requests"])} requests; plan: {args.out}')


if __name__ == '__main__':
    main()
