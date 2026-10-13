"""Live local Collector -> Prometheus/Loki/Tempo smoke check after Compose boot.

Exits nonzero unless all three backends contain this unique fixture.
Use --log-dir with the mounted ARGUS_LOG_DIR to also verify filelog/slog ingestion.
"""
import argparse
import datetime
import json
import pathlib
import secrets
import time
import urllib.parse
import urllib.request


def request(url, body=None):
    req = urllib.request.Request(url, data=json.dumps(body).encode() if body is not None else None,
                                 headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=5) as response:
        return json.loads(response.read(2 * 1024 * 1024))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--collector', default='http://127.0.0.1:4318')
    p.add_argument('--prometheus', default='http://127.0.0.1:9090')
    p.add_argument('--loki', default='http://127.0.0.1:3100')
    p.add_argument('--tempo', default='http://127.0.0.1:3200')
    p.add_argument('--log-dir', type=pathlib.Path)
    p.add_argument('--wait-seconds', type=int, default=45)
    a = p.parse_args()
    trace, span, probe = secrets.token_hex(16), secrets.token_hex(8), secrets.token_hex(6)
    now = time.time_ns()
    attrs = [{'key': 'service.name', 'value': {'stringValue': 'argus-smoke'}}]
    request(a.collector + '/v1/traces', {'resourceSpans': [{'resource': {'attributes': attrs},
        'scopeSpans': [{'scope': {'name': 'argus.smoke'}, 'spans': [{'traceId': trace, 'spanId': span,
        'name': 'argus.smoke', 'kind': 1, 'startTimeUnixNano': str(now),
        'endTimeUnixNano': str(now+1000000), 'attributes': []}]}]}]})
    request(a.collector + '/v1/metrics', {'resourceMetrics': [{'resource': {'attributes': attrs},
        'scopeMetrics': [{'scope': {'name': 'argus.smoke'}, 'metrics': [{'name': 'argus_smoke_checks',
        'sum': {'aggregationTemporality': 2, 'isMonotonic': True,
        'dataPoints': [{'startTimeUnixNano': str(now-1000000), 'timeUnixNano': str(now), 'asInt': '1'}]}}]}]}]})
    request(a.collector + '/v1/logs', {'resourceLogs': [{'resource': {'attributes': attrs},
        'scopeLogs': [{'scope': {'name': 'argus.smoke'}, 'logRecords': [{'timeUnixNano': str(now),
        'severityNumber': 9, 'severityText': 'INFO', 'traceId': trace, 'spanId': span,
        'body': {'stringValue': json.dumps({'msg': 'argus-smoke', 'probe': probe, 'trace_id': trace, 'span_id': span})}}]}]}]})
    if a.log_dir:
        a.log_dir.mkdir(parents=True, exist_ok=True)
        with (a.log_dir / 'smoke.jsonl').open('a') as file:
            file.write(json.dumps({'time': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                'level': 'INFO', 'msg': 'argus-smoke-filelog', 'probe': probe,
                'trace_id': trace, 'span_id': span}) + '\n')
    passed = {}
    deadline = time.monotonic() + a.wait_seconds
    while time.monotonic() < deadline:
        checks = {
            'trace': lambda: bool(request(a.tempo + '/api/traces/' + trace)),
            'metric': lambda: bool(request(a.prometheus + '/api/v1/query?' + urllib.parse.urlencode(
                {'query': 'argus_smoke_checks_total'}))['data']['result']),
            'log': lambda: probe in json.dumps(request(a.loki + '/loki/api/v1/query_range?' + urllib.parse.urlencode(
                {'query': '{service_name="argus-smoke"} |= "' + probe + '"', 'limit': 10}))),
        }
        if a.log_dir:
            checks['filelog'] = lambda: probe in json.dumps(request(a.loki + '/loki/api/v1/query_range?' + urllib.parse.urlencode(
                {'query': '{service_name=~".+"} |= "argus-smoke-filelog" |= "' + probe + '"', 'limit': 10})))
        for key, check in checks.items():
            if not passed.get(key):
                try:
                    passed[key] = check()
                except Exception:
                    passed[key] = False
        if all(passed.get(key) for key in checks):
            print(json.dumps({'verified': passed, 'trace_id': trace, 'probe': probe}))
            return
        time.sleep(1)
    print(json.dumps({'verified': passed, 'trace_id': trace, 'probe': probe}))
    raise SystemExit(1)


if __name__ == '__main__':
    main()
