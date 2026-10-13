"""Complementary deterministic strict entity score, not the official rubric.
Only contributing_factor=True candidates count; namespace, kind and GT regex
must match. GT aliases are supported. No semantic mapping or causal judgment.
"""
import argparse,hashlib,json,pathlib,re,statistics
import yaml

METRICS=('hit_at_1','precision','recall','f1')

def validate_filters(gt):
 errors=[]
 for group in gt.get('groups',[]):
  filters=group.get('filter',[])
  if not isinstance(filters,list):
   errors.append({'group_id':group.get('id'),'kind':group.get('kind'),'namespace':group.get('namespace'),'filter':filters,'error':'GT filter must be a list of regex strings'})
   continue
  for pattern in filters:
   try:
    if not isinstance(pattern,str):raise ValueError('GT regex must be a string')
    re.compile(pattern)
   except (re.error,ValueError) as error:
    errors.append({'group_id':group.get('id'),'kind':group.get('kind'),'namespace':group.get('namespace'),'filter':pattern,'error':str(error)})
 return errors

def source_receipt(path,blob):
 return {'path':str(path.resolve()),'sha256':hashlib.sha256(blob).hexdigest()}

def score(gt,prediction):
 # Validate every filter before looking at predictions: an empty/failed prediction
 # must not turn malformed gold into a misleading measured zero.
 errors=validate_filters(gt)
 if errors:
  return {'score_valid':False,**{key:None for key in METRICS},'candidate_count':None,'root_alias_group_count':None,'matched_root_groups':None,'errors':errors}
 groups={g['id']:g for g in gt.get('groups',[])};parent={k:k for k in groups}
 def find(k):
  while parent[k]!=k:k=parent[k]
  return k
 for aliases in gt.get('aliases',[]):
  known=[k for k in aliases if k in parent]
  for k in known[1:]:parent[find(k)]=find(known[0])
 roots={find(k) for k,g in groups.items() if g.get('root_cause') is True};candidates=[e for e in prediction.get('entities',[]) if e.get('contributing_factor') is True];matches=[]
 for e in candidates:
  parts=e.get('name','').split('/',2);hit=set()
  if len(parts)==3:
   namespace,kind,name=parts
   for k,g in groups.items():
    if g.get('kind','').lower()!=kind.lower() or g.get('namespace','')!=namespace:continue
    if any(re.fullmatch(pattern,name) for pattern in g.get('filter',[]) if isinstance(pattern,str)) and find(k) in roots:hit.add(find(k))
  matches.append(hit)
 found=set().union(*matches) if matches else set();tp=len(found);precision=tp/len(candidates) if candidates else 0;recall=tp/len(roots) if roots else 0;f1=2*precision*recall/(precision+recall) if precision+recall else 0
 return {'score_valid':True,'hit_at_1':int(bool(matches and matches[0])),'precision':precision,'recall':recall,'f1':f1,'candidate_count':len(candidates),'root_alias_group_count':len(roots),'matched_root_groups':sorted(found),'errors':[]}
def main():
 p=argparse.ArgumentParser();p.add_argument('--ground-truth',required=True);p.add_argument('--predictions',required=True);p.add_argument('--out',required=True);a=p.parse_args();rows=[]
 for path in sorted(pathlib.Path(a.predictions).glob('scenario-*/*/outputs/agent_output.json')):
  sid=path.parents[2].name.split('-',1)[1];trial=path.parents[1].name;gt_path=pathlib.Path(a.ground_truth)/f'Scenario-{sid}'/'ground_truth.yaml';gt_blob=gt_path.read_bytes();prediction_blob=path.read_bytes();gt=yaml.safe_load(gt_blob);s=score(gt,json.loads(prediction_blob));rows.append({'incident_id':sid,'trial':trial,'ground_truth_source':source_receipt(gt_path,gt_blob),'prediction_source':source_receipt(path,prediction_blob),**s})
 by={}
 for row in rows:by.setdefault(row['incident_id'],[]).append(row)
 observed={key:[row for row in group if row['score_valid']] for key,group in by.items()}
 observed={key:group for key,group in observed.items() if group}
 metrics={k:statistics.mean(statistics.mean(r[k] for r in group) for group in observed.values()) if observed else None for k in METRICS}
 unknown=[{'incident_id':row['incident_id'],'trial':row['trial'],'errors':row['errors'],'ground_truth_source':row['ground_truth_source']} for row in rows if not row['score_valid']]
 out={'method':'strict namespace/kind/regex + GT aliases; true contributing factors only; all submitted failures retained; invalid GT regex is explicitly unknown and never rewritten; no semantic matching','n_incidents':len(by),'n_runs':len(rows),'n_scored_incidents':len(observed),'n_scored_runs':sum(len(group) for group in observed.values()),'n_unscorable_runs':len(unknown),'n_unscorable_incidents':len({row['incident_id'] for row in unknown}),'macro_metrics':metrics,'macro_metrics_definition':'mean of per-incident trial means over scorable trials only; denominator n_scored_incidents; not an all-case score when coverage is incomplete','macro_metrics_all_incidents':metrics if not unknown else {k:None for k in METRICS},'unknown_scores':unknown,'rows':rows};pathlib.Path(a.out).write_text(json.dumps(out,indent=2,allow_nan=False));print(json.dumps(metrics))
if __name__=='__main__':main()
