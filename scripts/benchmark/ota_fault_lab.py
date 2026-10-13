"""Isolated HTTP fault lab. Telemetry comes from executed requests, never CSV edits."""
import hashlib
import http.server
import json
import threading
import time
import urllib.error
import urllib.request

PAYLOAD = b'Argus public controlled OTA replay firmware\n'
PUBLISHED_SHA = hashlib.sha256(PAYLOAD).hexdigest()


def run_download(config):
    events = []
    lock = threading.Lock()
    server_requests = 0
    unavailable_until = None
    started = time.monotonic()

    def record(**fields):
        with lock:
            events.append(dict(timestamp_ms=round((time.monotonic() - started) * 1000, 3), **fields))

    class Origin(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            nonlocal server_requests, unavailable_until
            begin = time.monotonic()
            with lock:
                server_requests += 1
                request_number = server_requests
                if unavailable_until is None:
                    unavailable_until = time.monotonic() + config.get("ready_after_ms", 0) / 1000
            time.sleep(config['service_delay_ms'] / 1000)
            status = 503 if time.monotonic() < unavailable_until else 200
            payload = PAYLOAD if not config.get('corrupt_response', False) else PAYLOAD + b'corrupted'
            self.send_response(status)
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            try:
                self.wfile.write(payload)
            except (BrokenPipeError, ConnectionResetError):
                pass
            record(module='artifact_service', event='response', request=request_number,
                   status=status, service_elapsed_ms=round((time.monotonic() - begin) * 1000, 3))

    origin = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Origin)
    origin.daemon_threads = False
    thread = threading.Thread(target=lambda: origin.serve_forever(poll_interval=.01))
    thread.start()
    origin_url = f'http://127.0.0.1:{origin.server_port}/firmware'
    proxy = None
    proxy_thread = None
    url = origin_url
    if config['proxy_delay_ms']:
        class Proxy(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                begin = time.monotonic()
                # Delay occurs on the transit hop before the origin, independently of service work.
                time.sleep(config['proxy_delay_ms'] / 1000)
                with urllib.request.urlopen(origin_url, timeout=2) as response:
                    payload = response.read()
                self.send_response(200)
                self.send_header('Content-Length', str(len(payload)))
                self.end_headers()
                try:
                    self.wfile.write(payload)
                except (BrokenPipeError, ConnectionResetError):
                    pass
                record(module='transit_proxy', event='response', transit_elapsed_ms=round((time.monotonic() - begin) * 1000, 3))
        proxy = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Proxy)
        proxy.daemon_threads = False
        proxy_thread = threading.Thread(target=lambda: proxy.serve_forever(poll_interval=.01))
        proxy_thread.start()
        url = f'http://127.0.0.1:{proxy.server_port}/firmware'

    time.sleep(config.get('startup_delay_ms', 0) / 1000)
    success_count = 0
    failed_requests = 0
    client_attempts = 0
    latencies = []
    downloaded = b''
    logical_requests = config.get('logical_requests', 3)
    try:
        for logical in range(logical_requests):
            for attempt in range(config['max_attempts']):
                client_attempts += 1
                begin = time.monotonic()
                code = None
                try:
                    with urllib.request.urlopen(url, timeout=config['timeout_ms'] / 1000) as response:
                        downloaded = response.read()
                    actual = hashlib.sha256(downloaded).hexdigest()
                    if actual != config['manifest_checksum']:
                        code = 'OTA_CHECKSUM_MISMATCH'
                        # Publisher hash measured before transfer; distinguishes bad metadata vs damaged bytes.
                        record(error_type=code, expected_checksum=config['manifest_checksum'],
                               actual_checksum=actual, published_sha256=PUBLISHED_SHA)
                    else:
                        success_count += 1
                        record(module='download', event='complete', logical=logical, attempt=attempt + 1)
                except urllib.error.HTTPError as exc:
                    code = 'OTA_DOWNLOAD_HTTP_ERROR'
                    record(module='download', error_type=code, status=exc.code, logical=logical, attempt=attempt + 1)
                except (TimeoutError, urllib.error.URLError) as exc:
                    if isinstance(exc, urllib.error.URLError) and not isinstance(exc.reason, TimeoutError):
                        raise
                    code = 'OTA_DOWNLOAD_TIMEOUT'
                    record(module='download', error_type=code, logical=logical, attempt=attempt + 1,
                           elapsed_ms=round((time.monotonic() - begin) * 1000, 3), timeout_ms=config['timeout_ms'])
                latencies.append(round((time.monotonic() - begin) * 1000, 3))
                if code is None:
                    break
                failed_requests += 1
                if attempt + 1 < config['max_attempts']:
                    time.sleep(config['retry_backoff_ms'] / 1000)
    finally:
        # Proxy handlers may still call origin after the client timeout. Join proxy first.
        if proxy:
            proxy.shutdown()
            proxy.server_close()
            proxy_thread.join()
        origin.shutdown()
        origin.server_close()
        thread.join()
    events.sort(key=lambda x: x['timestamp_ms'])
    stats = {}
    for row in events:
        if row.get('error_type'):
            stats[row['error_type']] = stats.get(row['error_type'], 0) + 1
    return dict(success=success_count == logical_requests, successful_requests=success_count,
                failed_requests=failed_requests, logical_requests=logical_requests,
                client_attempts=client_attempts, actual_server_requests=server_requests,
                client_latencies_ms=latencies, error_counts=stats,
                events=events, downloaded=downloaded)
