"""Generate real controlled fault packs; prepare_suite reads public files only."""
import argparse
import copy
import hashlib
import json
import os
import pathlib
import subprocess
from ota_fault_lab import PAYLOAD, PUBLISHED_SHA, run_download

CAUSES = ('client_timeout', 'server_slow', 'network_delay', 'retry_amplification', 'manifest_mismatch', 'artifact_corruption')


def dump(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def sha(data):
    return hashlib.sha256(data).hexdigest()


def git(root, *args):
    env = dict(os.environ, GIT_AUTHOR_NAME='Argus experiment', GIT_AUTHOR_EMAIL='experiment@example.invalid',
               GIT_COMMITTER_NAME='Argus experiment', GIT_COMMITTER_EMAIL='experiment@example.invalid',
               GIT_AUTHOR_DATE='2026-10-13T00:00:00Z', GIT_COMMITTER_DATE='2026-10-13T00:00:00Z')
    return subprocess.check_output(['git', '-C', str(root), *args], env=env, stderr=subprocess.DEVNULL).decode().strip()


def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(',', ':')).replace('\u2028', r'\u2028').replace('\u2029', r'\u2029').encode()


def full_case_digest(case, task, adapter):
    data = {key: case[key] for key in ('id', 'lines', 'error_code_stats', 'logs_summary', 'snapshot_scope', 'sources')}
    data.update(task=task, adapter=adapter)
    return sha(canonical(data))


def suite_digest(suite):
    value = {key: suite[key] for key in ('benchmark', 'adapter', 'task', 'partition', 'input_schema')}
    value['case_digests'] = [{'id': case['id'], 'full_input_sha256': case['full_input_sha256']} for case in suite['cases']]
    return sha(canonical(value))


def seal_suite(suite):
    suite = copy.deepcopy(suite)
    suite['input_schema'] = 'ota-input-v2'
    for case in suite['cases']:
        case.setdefault('sources', [])
        case.setdefault('snapshot_scope', '')
        case.setdefault('logs_summary', '')
        case.setdefault('error_code_stats', {})
        case['sha256'] = sha('\n'.join(case['lines']).encode())
        case['full_input_sha256'] = full_case_digest(case, suite['task'], suite['adapter'])
    suite['revision'] = suite_digest(suite)
    return suite


def verify_suite(suite):
    if suite.get('task') != 'ota' or suite.get('input_schema') != 'ota-input-v2':
        raise ValueError('versioned OTA full-input contract required')
    for case in suite['cases']:
        if case['sha256'] != sha('\n'.join(case['lines']).encode()) or case.get('full_input_sha256') != full_case_digest(case, suite['task'], suite['adapter']):
            raise ValueError('OTA line or full input digest mismatch')
    if suite.get('revision') != suite_digest(suite):
        raise ValueError('OTA suite revision mismatch')


def prepare_suite(public, partition):
    if partition not in {'dev', 'heldout', 'all'}:
        raise ValueError('partition must be dev/heldout/all')
    manifest = json.loads((public / 'manifest.json').read_text())
    cases = []
    for entry in manifest['cases']:
        if partition != 'all' and entry['partition'] != partition:
            continue
        folder = public / 'cases' / entry['id']
        lines = []
        sources = []
        def add(value, file, line, pointer=None):
            text = json.dumps(value, ensure_ascii=False, separators=(',', ':'))
            # Small, lossless parts fit the production read_logs 400-rune bound.
            if len(text) <= 400:
                lines.append(text)
                sources.append({'file': f'cases/{entry["id"]}/{file}', 'line': line, **({'json_pointer': pointer} if pointer is not None else {})})
            else:
                for offset in range(0, len(text), 120):
                    lines.append(json.dumps({'source': file, 'line': line, 'part': offset // 120,
                                             'text': text[offset:offset+120]}, ensure_ascii=False, separators=(',', ':')))
                    sources.append({'file': f'cases/{entry["id"]}/{file}', 'line': line, 'part': offset // 120, **({'json_pointer': pointer} if pointer is not None else {})})
        logs = [json.loads(line) for line in (folder / 'logs.jsonl').read_text().splitlines()]
        for n, row in enumerate(logs, 1):
            add(row, 'logs.jsonl', n)
        # Compact cases have actual error records carrying publisher/received hashes;
        # additional pack assets remain archived, but are not accessible in any strategy.
        if entry['snapshot_scope'] == 'full':
            for file in ('config.json', 'config-diff.json', 'release.json', 'dependencies.json', 'metrics.json'):
                value = json.loads((folder / file).read_text())
                items = value.items() if isinstance(value, dict) else enumerate(value)
                for key, item in items:
                    raw_lines = (folder / file).read_text().splitlines()
                    physical = next((n for n, line in enumerate(raw_lines, 1) if line.lstrip().startswith(json.dumps(str(key)) + ':')), 1) if isinstance(value, dict) else 1
                    add({'source': file, 'key': str(key), 'value': item}, file, physical, '/' + str(key))
        stats = {}
        for row in logs:
            if row.get('error_type'):
                stats[row['error_type']] = stats.get(row['error_type'], 0) + 1
        cases.append({'id': entry['id'], 'lines': lines, 'sha256': sha('\n'.join(lines).encode()),
                      'error_code_stats': stats, 'sources': sources, 'snapshot_scope': entry['snapshot_scope'],
                      'logs_summary': json.dumps({'hybrid_evidence_schema': 'v1', 'stats_grain': 'log_occurrence',
                                                  'complete': entry['snapshot_scope'] == 'compact'}, separators=(',', ':'))})
    identity = sha(json.dumps(cases, sort_keys=True, ensure_ascii=False).encode())
    return seal_suite({'benchmark': 'argus-ota-controlled-http-v1', 'revision': identity, 'partition': partition,
            'adapter': 'ota-fault-pack-v1', 'task': 'ota', 'cases': cases})


def generate_pack(out):
    out.mkdir(parents=True, exist_ok=False)
    public = out / 'public'
    private = out / 'private'
    public.mkdir()
    private.mkdir(mode=0o700)
    release_source = out / 'release-source'
    release_source.mkdir()
    git(release_source, 'init', '-q')
    (release_source / 'download_service.py').write_bytes(pathlib.Path(__file__).with_name('ota_fault_lab.py').read_bytes())
    labels = []
    entries = []
    for partition, offset, scale in [('dev', 1000, 1), ('heldout', 2000, 1.3)]:
        for index, cause in enumerate(CAUSES, 1):
            sid = str(offset + index)
            folder = public / 'cases' / sid
            folder.mkdir(parents=True)
            base = {'timeout_ms': round(180 * scale), 'service_delay_ms': round(20 * scale),
                    'proxy_delay_ms': 0, 'max_attempts': 1, 'retry_backoff_ms': round(110 * scale),
                    'manifest_checksum': PUBLISHED_SHA, 'ui_banner': 'old', 'logical_requests': 3}
            if cause == 'client_timeout':
                base['service_delay_ms'] = round(65 * scale)
            if cause == 'retry_amplification':
                base.update(ready_after_ms=round(80 * scale), max_attempts=2, service_delay_ms=1)
            current = copy.deepcopy(base)
            current['ui_banner'] = 'new'  # Recent unrelated change appears in every full case.
            if cause == 'client_timeout':
                current['timeout_ms'] = round(25 * scale)
            elif cause == 'server_slow':
                current['service_delay_ms'] = round(290 * scale)
            elif cause == 'network_delay':
                current['proxy_delay_ms'] = round(290 * scale)
            elif cause == 'retry_amplification':
                current.update(max_attempts=4, retry_backoff_ms=0)
            elif cause == 'manifest_mismatch':
                current['manifest_checksum'] = sha(b'other published artifact')
            # Transfer corruption is injected at runtime; it is not a clear-text root-cause label in config.
            runtime = copy.deepcopy(current)
            if cause == 'artifact_corruption':
                runtime['corrupt_response'] = True
            dump(release_source / 'config.json', base)
            git(release_source, 'add', '.')
            git(release_source, 'commit', '--allow-empty', '-qm', f'base configuration {sid}')
            before = git(release_source, 'rev-parse', 'HEAD')
            dump(release_source / 'config.json', current)
            git(release_source, 'add', '.')
            git(release_source, 'commit', '-qm', f'release configuration {sid}')
            commit = git(release_source, 'rev-parse', 'HEAD')
            control = run_download(base)
            fault = run_download(runtime)
            restored = run_download(base)
            if not control['success'] or not restored['success'] or fault['success']:
                raise RuntimeError(f'controlled experiment invariants not met for {sid}; no benchmark inputs published')
            if cause == 'retry_amplification' and not (fault['actual_server_requests'] > control['actual_server_requests'] and fault['failed_requests'] > control['failed_requests']):
                raise RuntimeError(f'retry amplification not observed for {sid}; no benchmark inputs published')
            (folder / 'config.json').write_bytes((release_source / 'config.json').read_bytes())
            (folder / 'artifact.bin').write_bytes(fault['downloaded'] or PAYLOAD)
            dump(folder / 'config-diff.json', {key: {'before': base.get(key), 'after': val}
                                             for key, val in current.items() if base.get(key) != val})
            release = {'experiment_source': 'local controlled HTTP execution; not production',
                       'baseline_git_commit': before, 'git_commit': commit,
                       'config_blob': git(release_source, 'rev-parse', 'HEAD:config.json'),
                       'config_sha256': sha((folder / 'config.json').read_bytes()),
                       'published_artifact_sha256': PUBLISHED_SHA,
                       'actual_artifact_sha256': sha((folder / 'artifact.bin').read_bytes()),
                       'received_artifact_sha256': sha(fault['downloaded']) if fault['downloaded'] else None,
                       'artifact_observation': 'downloaded bytes' if fault['downloaded'] else 'build reference; no successful download',
                       'declared_manifest_sha256': current['manifest_checksum']}
            dump(folder / 'release.json', release)
            dump(folder / 'dependencies.json', [{'upstream': 'download', 'downstream': 'artifact_service', 'source': 'executed HTTP'},
                                                {'upstream': 'download', 'downstream': 'transit_proxy', 'source': 'configured hop'}] if current['proxy_delay_ms'] else [{'upstream': 'download', 'downstream': 'artifact_service', 'source': 'executed HTTP'}])
            metrics = {k: v for k, v in fault.items() if k not in {'events', 'downloaded', 'error_counts'}}
            dump(folder / 'metrics.json', metrics)
            compact = cause == 'manifest_mismatch'
            rows = [row for row in fault['events'] if row.get('error_type')] if compact else fault['events'] + [
                {'module': 'display', 'event': 'heartbeat', 'status': 'healthy'},
                {'module': 'display', 'event': 'config_applied', 'key': 'ui_banner', 'value': 'new'}]
            (folder / 'logs.jsonl').write_text(''.join(json.dumps(row, separators=(',', ':')) + '\n' for row in rows))
            entries.append({'id': sid, 'partition': partition, 'snapshot_scope': 'compact' if compact else 'full'})
            checks = control['success'] and restored['success'] and restored['failed_requests'] <= control['failed_requests']
            labels.append({'id': sid, 'partition': partition, 'cause_code': cause,
                           'injection': {k: v for k, v in runtime.items() if base.get(k) != v and k != 'ui_banner'},
                           'injection_observed': not fault['success'], 'recovery_checks_passed': checks,
                           'recovery_condition': 'all logical requests succeed and failed attempts return to control bound',
                           'control': {k: v for k, v in control.items() if k not in {'events', 'downloaded'}},
                           'fault': {k: v for k, v in fault.items() if k not in {'events', 'downloaded'}},
                           'restored': {k: v for k, v in restored.items() if k not in {'events', 'downloaded'}}})
    dump(public / 'manifest.json', {'schema': 'ota-fault-pack-v1', 'cases': entries})
    suite = prepare_suite(public, 'all')
    for label, case in zip(labels, suite['cases']):
        label['input_sha256'] = case['sha256']
        label['full_input_sha256'] = case['full_input_sha256']
        # Score causal evidence groups, not just any error citation. All locations map to raw public files.
        error_ids = [f'log:{i+1}' for i, line in enumerate(case['lines']) if 'error_type' in line]
        if label['cause_code'] == 'manifest_mismatch':
            groups = [error_ids]
        else:
            def ids_with(*terms):
                return [f'log:{i+1}' for i, line in enumerate(case['lines']) if all(term in line for term in terms)]
            cause = label['cause_code']
            if cause == 'client_timeout':
                groups = [error_ids, ids_with('"source":"config.json"', '"key":"timeout_ms"'), ids_with('service_elapsed_ms')]
            elif cause == 'server_slow':
                groups = [error_ids, ids_with('service_elapsed_ms')]
            elif cause == 'network_delay':
                groups = [error_ids, ids_with('transit_elapsed_ms'), ids_with('service_elapsed_ms')]
            elif cause == 'retry_amplification':
                groups = [error_ids, ids_with('"source":"config.json"', '"key":"max_attempts"'), ids_with('"source":"metrics.json"', '"key":"client_attempts"')]
            else:
                groups = [error_ids, ids_with('published_sha256')]
        label['key_evidence_groups'] = groups
    dump(private / 'labels.json', {'schema': 'ota-fault-truth-v1', 'cases': labels})
    for partition in ('dev', 'heldout', 'all'):
        dump(public / f'{partition}.json', prepare_suite(public, partition))
    receipt = {'source': 'actual local HTTP execution', 'cases': len(labels),
               'injections_observed': sum(x['injection_observed'] for x in labels),
               'healthy_controls': sum(x['control']['success'] for x in labels),
               'recovery_checks_passed': sum(x['recovery_checks_passed'] for x in labels),
               'live_model_status': 'pending: provider credentials required',
               'generator_sha256': sha(pathlib.Path(__file__).read_bytes()),
               'lab_sha256': sha(pathlib.Path(__file__).with_name('ota_fault_lab.py').read_bytes())}
    git(release_source, 'bundle', 'create', str((out / 'release-source.bundle').resolve()), '--all')
    dump(out / 'receipt.json', receipt)
    return receipt


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--out', required=True)
    args = p.parse_args()
    print(json.dumps(generate_pack(pathlib.Path(args.out))))


if __name__ == '__main__':
    main()
