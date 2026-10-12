"""Collect raw, versioned experiment evidence. No label data enters inference."""
import argparse,collections,hashlib,json,pathlib,platform,statistics

def read(p):return json.loads(p.read_text())
def run_stats(folder):
 rows=[read(p) for p in sorted(folder.glob('scenario-*/*/outputs/argus_run.json'))]
 lat=sorted(r['latency_ms'] for r in rows)
 return {'runs':len(rows),'valid_reports':sum(r['valid_report'] for r in rows),'failures':[{'scenario':r['scenario_id'],'trial':r['trial'],'error':r.get('error')} for r in rows if not r['valid_report']], 'provider_attempts':sum(r['provider_stats']['Attempts'] for r in rows),'provider_reported_tokens':sum(r['provider_stats']['Tokens'] for r in rows),'latency_ms':{'median':statistics.median(lat),'p95_nearest_rank':lat[max(0,__import__('math').ceil(.95*len(lat))-1)]} if lat else {},'raw_folder':folder.name}
def main():
 p=argparse.ArgumentParser();p.add_argument('--outputs',required=True);p.add_argument('--repo',required=True);a=p.parse_args();out=pathlib.Path(a.outputs);repo=pathlib.Path(a.repo)
 scores={}
 for name in ['itbench-agent-final','itbench-baseline-official','itbench-holdout-agent','itbench-holdout-baseline']:
  f=out/(name+'-scores.json');x=read(f);by={}
  for row in x['raw_results']:
   val=row.get('scores',{}).get('root_cause_entity',{}).get('calculation_f1');val=val if isinstance(val,(int,float)) else 0
   by.setdefault(row['incident_id'],[]).append(val)
  zero_f1=statistics.mean(statistics.mean(v) for v in by.values())
  scores[name]={'zero_inclusive_entity_f1':zero_f1,'upstream_statistics':x['statistics'],'raw_score_count':len(x['raw_results']),'sha256':hashlib.sha256(f.read_bytes()).hexdigest(),'path':f.name}
 regressions={}
 for f in sorted((repo/'.regression-results').glob('scenarios-*-tests.json')):
  c=collections.Counter(x['Action'] for x in map(json.loads,f.read_text().splitlines()) if x.get('Test'));regressions[f.stem]={k:c[k] for k in ['pass','fail','skip']}
 for f in (repo/'.regression-results').glob('scenarios-*-race.json'):
  c=collections.Counter(x['Action'] for x in map(json.loads,f.read_text().splitlines()) if x.get('Test'));regressions[f.stem]={k:c[k] for k in ['pass','fail','skip']}
 suites={name:run_stats(out/name) for name in ['itbench-agent-runs','itbench-agent-v2','itbench-agent-final','itbench-baseline-v2','itbench-holdout-agent','itbench-holdout-baseline']}
 summary={'date':'2026-10-12','timezone':'Asia/Shanghai','runtime_source_commit':'b290fbebc39fd2c80223af64c4b9883c694cca02','runtime_source_tree':'3377d92a776fd5aae4787d244ad4c1f08240222b','dataset_revision':'d0916b08ba421ce5e672e9ad68aa947d938dfef0','evaluator_revision':'14f026fc9cc348c4ecec5ab32714de954c95c1b1','benchmark_type':'adapted offline SRE transfer pilot, not official leaderboard or automotive/production validation','model':'deepseek-flash','judge':'deepseek-flash, separate same-model calls, official rubric and aggregation; human review not completed','modalities':['alerts','warning k8s events','latest otel-demo objects'],'omitted':['metrics','application logs','traces'],'host':{'cpu':'Apple M4 Pro','cores':14,'ram_gib':24,'os':'macOS 26.5.2','go':'1.24.12 darwin/arm64'},'runs':suites,'scores':scores,'regressions':regressions,'guided_reversal':read(out/'deepseek-hypothesis-reversal.json'),'controlled_load':read(out/'agent-load/load-summary.json'),'deterministic_entity_checks':{p.stem:read(p) for p in out.glob('*entity-check.json')},'limits':['development cases used to fix runtime problems; held-out scenarios only five','single-shot baseline receives first 24000 characters; Agent can access more through tools','model self-reported confidence is not calibrated','provider-reported total tokens include cached input; monetary cost not computed','process-local governor is not distributed tenancy/quota','parser/aggregation remain mocks; no production SLA or customer scale claim']}
 (out/'agent-evaluation-summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2));print('Evidence summary written')
if __name__=='__main__':main()
