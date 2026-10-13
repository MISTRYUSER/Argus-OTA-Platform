"""Read back local Langfuse health, imported scores and actual observations.

Credentials come only from LANGFUSE_* environment. Stores a bounded verification
receipt, not credential-bearing HTTP headers or full model inputs/outputs.
"""
import argparse
import base64
import json
import os
import pathlib
import time
import urllib.parse
import urllib.request
import urllib.error


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def verify(plan, endpoint, public_key, secret_key, execution_trace=None, minimum_spans=1, wait_seconds=60):
    parts = urllib.parse.urlsplit(endpoint)
    if parts.scheme not in ('http', 'https') or not parts.hostname or parts.username or parts.password or parts.query or parts.fragment:
        raise ValueError('invalid explicit endpoint')
    if parts.scheme == 'http' and parts.hostname not in ('localhost', '127.0.0.1', '::1'):
        raise ValueError('remote endpoints require HTTPS')
    auth = base64.b64encode((public_key + ':' + secret_key).encode()).decode()
    opener = urllib.request.build_opener(NoRedirect())
    def get(path, params=None):
        url = endpoint.rstrip('/') + path
        if params:
            url += '?' + urllib.parse.urlencode(params)
        req = urllib.request.Request(url, headers={'Authorization': 'Basic ' + auth})
        with opener.open(req, timeout=5) as response:
            body = response.read(2 * 1024 * 1024 + 1)
            if len(body) > 2 * 1024 * 1024:
                raise ValueError('verification response too large')
            return json.loads(body)
    expected_scores = {item['body']['id']: item['body'] for item in plan.get('requests', []) if item['path'] == '/api/public/scores'}
    scores = list(expected_scores)
    snapshots = [case['snapshot_trace_id'] for case in plan.get('cases', [])]
    health = get('/api/public/health')
    result = {'health_ok': True, 'health': health, 'expected_score_count': len(scores),
              'observed_score_count': 0, 'snapshot_trace_count': len(snapshots),
              'verified_snapshot_trace_count': 0, 'execution_trace_id': execution_trace,
              'execution_span_count': 0, 'execution_span_names': [], 'execution_tool_names': [], 'score_subjects_match': False, 'ok': False}
    deadline = time.monotonic() + wait_seconds
    while time.monotonic() < deadline:
        found = set()
        subject_mismatches = []
        for start in range(0, len(scores), 50):
            values = get('/api/public/v3/scores', {'id': ','.join(scores[start:start+50]), 'limit': 100, 'fields': 'subject'})
            for row in values.get('data', []):
                expected = expected_scores.get(row['id'])
                if expected is None:
                    continue
                subject = row.get('subject') or {}
                if expected.get('observationId'):
                    matched = subject.get('kind') == 'observation' and subject.get('id') == expected['observationId'] and subject.get('traceId') == expected['traceId']
                else:
                    matched = subject.get('kind') == 'trace' and subject.get('id') == expected['traceId']
                if matched:
                    found.add(row['id'])
                else:
                    subject_mismatches.append(row['id'])
        observed_snapshots = 0
        for trace_id in snapshots:
            values = get('/api/public/v2/observations', {'traceId': trace_id, 'limit': 100})
            observed_snapshots += bool(values.get('data'))
        names, tools, count = [], [], 0
        if execution_trace:
            values = get('/api/public/v2/observations', {'traceId': execution_trace, 'limit': 100, 'fields': 'basic,metadata', 'expandMetadata': 'attributes.tool.name'})
            rows = values.get('data', [])
            count = len(rows)
            names = sorted(row.get('name', '') for row in rows)
            tools = sorted({row.get('metadata', {}).get('attributes.tool.name') for row in rows if row.get('metadata', {}).get('attributes.tool.name')})
        result.update(observed_score_count=len(set(scores) & found),
                      verified_snapshot_trace_count=observed_snapshots,
                      execution_span_count=count, execution_span_names=names, execution_tool_names=tools,
                      score_subjects_match=not subject_mismatches)
        if len(set(scores) & found) == len(scores) and observed_snapshots == len(snapshots) and (not execution_trace or count >= minimum_spans):
            result['ok'] = True
            return result
        time.sleep(1)
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=pathlib.Path, required=True)
    parser.add_argument('--out', type=pathlib.Path, required=True)
    parser.add_argument('--execution-trace')
    parser.add_argument('--minimum-spans', type=int, default=1)
    parser.add_argument('--wait-seconds', type=int, default=60)
    args = parser.parse_args()
    endpoint, public, secret = [os.environ.get(key) for key in ['LANGFUSE_BASE_URL', 'LANGFUSE_PUBLIC_KEY', 'LANGFUSE_SECRET_KEY']]
    if not all([endpoint, public, secret]):
        parser.error('explicit LANGFUSE_BASE_URL, LANGFUSE_PUBLIC_KEY, LANGFUSE_SECRET_KEY required')
    result = verify(json.loads(args.plan.read_text()), endpoint, public, secret,
                    args.execution_trace, args.minimum_spans, args.wait_seconds)
    args.out.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps({key: result[key] for key in ['ok', 'observed_score_count', 'verified_snapshot_trace_count', 'execution_span_count']}))
    if not result['ok']:
        raise SystemExit(1)
