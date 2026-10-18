#!/usr/bin/env python3
"""Bounded, evidence-preserving CSV processing and comparable OTA replay statistics.

No model is used for arithmetic. CPU follows the notebook's per-process cpu_util
units (can exceed 100 on multi-core systems). Missing metrics remain unknown.
"""
import argparse
from collections import Counter, defaultdict
import csv
from datetime import datetime, timezone
import json
import math
from pathlib import Path
import sys
import statistics

REQUIRED = ('timestamp', 'version', 'vehicle_id', 'vehicle_class', 'module', 'replay_id', 'workload', 'error_code', 'cpu', 'memory_mb')
MAX_ROWS = 100000
MIN_SAMPLES = 10
MAX_BYTES = 32 * 1024 * 1024


def _time(value):
    try:
        result = float(value)
    except ValueError:
        dt = datetime.fromisoformat(value.replace('Z', '+00:00'))
        if dt.tzinfo is None:
            raise ValueError('timestamp must include timezone')
        result = dt.timestamp()
    if not math.isfinite(result) or result < 0:
        raise ValueError('invalid timestamp')
    return result


def _metric(value):
    if value.strip() == '':
        return None
    result = float(value)
    if not math.isfinite(result) or result < 0:
        raise ValueError('metrics must be finite and nonnegative')
    return result


def parse_csv(stream, source, max_rows=MAX_ROWS):
    if max_rows < 1 or max_rows > MAX_ROWS:
        raise ValueError('invalid row limit')
    reader = csv.DictReader(stream)
    if not reader.fieldnames or len(set(reader.fieldnames)) != len(reader.fieldnames) or not set(REQUIRED).issubset(reader.fieldnames):
        raise ValueError('missing or duplicated CSV headers: ' + ','.join(REQUIRED))
    rows = []
    for row in reader:
        if len(rows) >= max_rows:
            raise ValueError('too many rows')
        if None in row or any(row.get(key) is None for key in REQUIRED):
            raise ValueError('invalid CSV field count')
        record = {key: row[key].strip() for key in REQUIRED}
        for key in REQUIRED[1:7]:
            if not record[key] or len(record[key]) > 128:
                raise ValueError('empty or oversized ' + key)
        if len(record['error_code']) > 128:
            raise ValueError('oversized error_code')
        record.update(timestamp=_time(record['timestamp']), cpu=_metric(record['cpu']), memory_mb=_metric(record['memory_mb']), source=source, line=reader.line_num)
        for optional in ('error_type','expected_checksum','actual_checksum','change_id','change_description','module_name','exception_type'):
            if optional in row and row[optional].strip():
                if len(row[optional])>256:raise ValueError('oversized optional evidence')
                record[optional]=row[optional].strip()
        if row.get('exit_code','').strip():
            record['exit_code']=int(row['exit_code'].strip())
        if row.get('oom_killed','').strip():
            value=row['oom_killed'].strip().lower()
            if value not in ('true','false'):raise ValueError('oom_killed must be true or false')
            record['oom_killed']=value=='true'
        for optional in ('elapsed_ms','timeout_ms'):
            if optional in row and row[optional].strip():record[optional]=_metric(row[optional])
        rows.append(record)
    if not rows:
        raise ValueError('empty input')
    return rows


def parse_file(path, source, metadata=None):
    path = Path(path)
    if path.stat().st_size > MAX_BYTES:
        raise ValueError('input exceeds 32 MiB per file')
    if path.suffix.lower() == '.csv':
        with path.open(newline='', encoding='utf-8-sig') as f:
            return parse_csv(f, source)
    if '.record' in path.name or path.suffix.lower() == '.rec':
        if not metadata or metadata.get('record_format')!='apollo_cyber':
            raise ValueError('record format must explicitly be apollo_cyber; private rec is not interchangeable')
        from apollo_record import parse_record
        return parse_record(path, source, metadata, max_rows=MAX_ROWS)
    raise ValueError('supported inputs: public CSV or explicit Apollo Cyber Record')


def filter_stats(data):
    """Reuse notebook's floor(n*p) percentile convention, without empty->zero."""
    if not data:
        return {'samples': 0, 'mean': None, 'min': None, 'max': None, 'p95': None, 'p99': None}
    values = sorted(data)
    n = len(values)
    return dict(samples=n, mean=sum(values)/n, min=values[0], max=values[-1], p95=values[min(n-1, math.floor(n*.95))], p99=values[min(n-1, math.floor(n*.99))])


def _change(before, after):
    b, a = filter_stats(before), filter_stats(after)
    delta = None if b['mean'] is None or a['mean'] is None else a['mean']-b['mean']
    relative = None if delta is None or b['mean'] == 0 else delta/b['mean']
    return dict(baseline=b, candidate=a, delta=delta, relative_change=relative)


def _key(row):
    return tuple(row[key] for key in ('vehicle_id', 'vehicle_class', 'module', 'replay_id', 'workload'))


def analyze(rows, baseline=None, candidate=None):
    if not rows or len(rows) > MAX_ROWS:
        raise ValueError('empty or excessive aggregate rows')
    seen = set()
    groups = defaultdict(lambda: defaultdict(list))
    for row in rows:
        if not isinstance(row, dict) or not set(REQUIRED + ('source','line')).issubset(row):
            raise ValueError('invalid parsed row')
        # Validate persisted rows as well as fresh CSV input.
        if any(not isinstance(row[k],str) or not row[k] or len(row[k])>128 for k in REQUIRED[1:7]):
            raise ValueError('invalid metadata')
        if not isinstance(row['error_code'],str) or len(row['error_code'])>128:
            raise ValueError('invalid error code')
        if not isinstance(row['line'],int) or row['line']<1 or not isinstance(row['source'],str) or not row['source']:
            raise ValueError('invalid source locator')
        for metric in ('timestamp','cpu','memory_mb'):
            v=row[metric]
            if (metric=='timestamp' and v is None) or (v is not None and (not isinstance(v,(float,int)) or not math.isfinite(v) or v<0)):
                raise ValueError('invalid persisted metric')
        identity = (_key(row), row['version'], row.get('raw',{}).get('file_sha256',row['source']), row['line'])
        if identity in seen:
            raise ValueError('duplicate observation; refusing inflated sample counts')
        seen.add(identity)
        groups[_key(row)][row['version']].append(row)
    versions = sorted({r['version'] for r in rows})
    report = dict(schema_version=1, experiment_source='uploaded_observations', baseline_version=baseline, candidate_version=candidate,
                  sample_count=len(rows), matched_samples=0, versions=versions, comparisons=[], unmatched_groups=[], concentration=[], candidates=[], evidence=[], timeline=[], limitations=[],
                  policy=dict(min_samples_per_version=MIN_SAMPLES, max_duration_ratio=1.1, max_sample_ratio=1.1, percentile='sorted[floor(n*p)]',sampling_tolerance='max(1us,1% median positive baseline sample interval)', match_keys=['vehicle_id','vehicle_class','module','replay_id','workload']))
    if baseline is None and candidate is None:
        report.update(status='descriptive_only')
        report['limitations'].append('Explicit baseline and candidate versions required for regression comparison; version order is never inferred.')
        report['topic_counts']=dict(Counter(r['module'] for r in rows))
        return report
    if not baseline or not candidate or baseline==candidate or baseline not in versions or candidate not in versions:
        raise ValueError('baseline and candidate must be distinct observed versions')
    if set(versions) != {baseline,candidate}:
        report['limitations'].append('Other observed versions excluded from the requested two-version comparison.')
    for key, byversion in sorted(groups.items()):
        label=dict(zip(report['policy']['match_keys'],key))
        before=sorted(byversion.get(baseline,[]),key=lambda r:r['timestamp'])
        after=sorted(byversion.get(candidate,[]),key=lambda r:r['timestamp'])
        reason=None
        if len(before)<MIN_SAMPLES or len(after)<MIN_SAMPLES:
            reason='missing version or insufficient samples'
        else:
            db=before[-1]['timestamp']-before[0]['timestamp']; da=after[-1]['timestamp']-after[0]['timestamp']
            gaps=[b['timestamp']-a['timestamp'] for a,b in zip(before,before[1:]) if b['timestamp']>a['timestamp']]
            tolerance=max(.000001, statistics.median(gaps)*.01) if gaps else .000001
            if db<=0 or da<=0 or max(db,da)/min(db,da)>1.1:
                reason='duration mismatch'
            elif max(len(before),len(after))/min(len(before),len(after))>1.1:
                reason='sample count mismatch'
            elif any(abs((b['timestamp']-before[0]['timestamp'])-(a['timestamp']-after[0]['timestamp']))>tolerance for b,a in zip(before,after)):
                reason='replay sampling offset mismatch'
        if reason:
            report['unmatched_groups'].append(dict(group=label,reason=reason,baseline_samples=len(before),candidate_samples=len(after)))
            report['limitations'].append(str(label)+': '+reason)
            continue
        report['matched_samples']+=len(before)+len(after)
        comparison=dict(group=label,baseline_window=[before[0]['timestamp'],before[-1]['timestamp']],candidate_window=[after[0]['timestamp'],after[-1]['timestamp']],baseline_samples=len(before),candidate_samples=len(after))
        comparison['error_rate']=_change([int(bool(r['error_code'])) for r in before if r.get('raw',{}).get('error_observed',True)],[int(bool(r['error_code'])) for r in after if r.get('raw',{}).get('error_observed',True)])
        if comparison['error_rate']['baseline']['samples']!=len(before) or comparison['error_rate']['candidate']['samples']!=len(after):
            comparison['error_rate']['delta']=None; comparison['error_rate']['relative_change']=None
            report['limitations'].append(str(label)+': error signal not observed for the complete windows')
        comparison['baseline_error_counts']=dict(Counter(r['error_code'] for r in before if r['error_code']))
        comparison['candidate_error_counts']=dict(Counter(r['error_code'] for r in after if r['error_code']))
        comparison['new_error_codes']=sorted(set(comparison['candidate_error_counts'])-set(comparison['baseline_error_counts']))
        for metric in ('cpu','memory_mb'):
            comparison[metric]=_change([r[metric] for r in before if r[metric] is not None],[r[metric] for r in after if r[metric] is not None])
            if comparison[metric]['baseline']['samples']!=len(before) or comparison[metric]['candidate']['samples']!=len(after):
                report['limitations'].append(str(label)+': '+metric+' has missing samples; cannot assert full-window regression')
                # Do not compare complete baseline against selected candidate samples.
                comparison[metric]['delta']=None; comparison[metric]['relative_change']=None
        worsened=[k for k in ('error_rate','cpu','memory_mb') if comparison[k]['delta'] is not None and comparison[k]['delta']>0]
        ids=[]
        # Evidence tied to extrema and errors, not arbitrary unrelated first rows.
        evidence_rows=[]
        for versionrows in (before,after):
            evidence_rows += [versionrows[0], versionrows[-1]]
            evidence_rows += [next((r for r in versionrows if r['error_code']),versionrows[0])]
            for metric in ('cpu','memory_mb'):
                available=[r for r in versionrows if r[metric] is not None]
                if available: evidence_rows.append(max(available,key=lambda r:r[metric]))
        for row in evidence_rows:
            eid=f"source:{row['source']}:{row['line']}"
            if eid not in ids:
                ids.append(eid)
                report['evidence'].append(dict(id=eid,source=row['source'],line=row['line'],timestamp=row['timestamp'],version=row['version'],module=row['module'],error_code=row['error_code'],cpu=row['cpu'],memory_mb=row['memory_mb']))
        comparison['evidence_ids']=ids
        report['comparisons'].append(comparison)
        for metric in ('cpu','memory_mb'):
            if metric in worsened:
                row=max((r for r in after if r[metric] is not None),key=lambda r:r[metric])
                report['timeline'].append(dict(type='resource_increase',timestamp=row['timestamp'],version=candidate,module=row['module'],metric=metric,delta=comparison[metric]['delta'],source=row['source'],line=row['line']))
        report['timeline'].append(dict(type='declared_version_transition',timestamp=after[0]['timestamp'],baseline_version=baseline,candidate_version=candidate,timestamp_origin='candidate_window_start; actual deployment time not observed',group=label))
        for row in after:
            if row.get('change_id'):
                report['timeline'].append(dict(type='declared_module_change',timestamp=row['timestamp'],version=candidate,module=row['module'],change_id=row['change_id'],description=row.get('change_description',''),source=row['source'],line=row['line']))
                break
        report['timeline'].append(dict(type='version_comparison',group=label,baseline_window=comparison['baseline_window'],candidate_window=comparison['candidate_window'],evidence_ids=ids))
        for row in after:
            if row['error_code'] in comparison['new_error_codes']:
                report['timeline'].append(dict(type='new_error',timestamp=row['timestamp'],version=candidate,module=row['module'],error_code=row['error_code'],source=row['source'],line=row['line']))
                break
        if worsened:
            report['candidates'].append(dict(id='C'+str(len(report['candidates'])+1),group=label,description='Observed increases in '+','.join(worsened)+' after version change; module-associated candidate, not proven root cause',metrics=worsened,causal_status='unproven',evidence_ids=ids,verification=['Repeat both versions on the exact same replay and hardware with randomized execution order.','Check environment, temperature, dependencies and module change manifest; revert the suspected change separately.']))
    clusters=defaultdict(lambda:dict(matched=set(),affected=set(),metrics=set()))
    for comparison in report['comparisons']:
        group=comparison['group'];clusters[(group['vehicle_class'],group['module'])]['matched'].add(group['vehicle_id'])
    for candidate_item in report['candidates']:
        group=candidate_item['group'];cluster=clusters[(group['vehicle_class'],group['module'])]
        cluster['affected'].add(group['vehicle_id']);cluster['metrics'].update(candidate_item['metrics'])
    report['concentration']=[dict(vehicle_class=key[0],module=key[1],matched_vehicles=len(value['matched']),affected_vehicles=len(value['affected']),affected_vehicle_ids=sorted(value['affected']),metrics=sorted(value['metrics']),meaning='vehicles with any matched replay group showing an observed increase; not confirmed failures') for key,value in sorted(clusters.items())]
    report['status']='comparable' if report['comparisons'] else 'not_comparable'
    report['limitations'].append('Replay/workload labels are caller-supplied; matched labels and timing reduce confounding but do not prove identical environment or causality. No statistical significance claim; inspect independent repeat runs.')
    report['timeline'].sort(key=lambda e:e.get('timestamp',e.get('candidate_window',[0])[0]))
    return report


def main():
    p=argparse.ArgumentParser(); p.add_argument('mode',choices=['parse','analyze']); p.add_argument('--file'); p.add_argument('--source'); p.add_argument('--metadata-json'); p.add_argument('--baseline'); p.add_argument('--candidate')
    args=p.parse_args()
    try:
        if args.mode=='parse':
            if not args.file or not args.source: raise ValueError('--file and --source required')
            result=parse_file(args.file,args.source,json.loads(args.metadata_json) if args.metadata_json else None)
        else:
            raw=sys.stdin.buffer.read(MAX_BYTES+1)
            if len(raw)>MAX_BYTES: raise ValueError('aggregate input exceeds 32 MiB')
            result=analyze(json.loads(raw),args.baseline,args.candidate)
        print(json.dumps(result,allow_nan=False,ensure_ascii=False))
    except (ValueError,OSError,ImportError) as e:
        print(str(e),file=sys.stderr); return 2
    return 0

if __name__=='__main__': sys.exit(main())
