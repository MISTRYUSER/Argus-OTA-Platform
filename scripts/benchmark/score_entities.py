"""Complementary deterministic strict entity score, not the official rubric.
Only contributing_factor=True candidates count; namespace, kind and GT regex
must match. GT aliases are supported. No semantic mapping or causal judgment.
"""
import argparse,json,pathlib,re,statistics
import yaml

def score(gt,prediction):
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
 return {'hit_at_1':int(bool(matches and matches[0])),'precision':precision,'recall':recall,'f1':f1,'candidate_count':len(candidates),'root_alias_group_count':len(roots),'matched_root_groups':sorted(found)}
def main():
 p=argparse.ArgumentParser();p.add_argument('--ground-truth',required=True);p.add_argument('--predictions',required=True);p.add_argument('--out',required=True);a=p.parse_args();rows=[]
 for path in sorted(pathlib.Path(a.predictions).glob('scenario-*/*/outputs/agent_output.json')):
  sid=path.parents[2].name.split('-',1)[1];trial=path.parents[1].name;gt=yaml.safe_load((pathlib.Path(a.ground_truth)/f'Scenario-{sid}'/'ground_truth.yaml').read_text());s=score(gt,json.loads(path.read_text()));rows.append({'incident_id':sid,'trial':trial,**s})
 by={}
 for row in rows:by.setdefault(row['incident_id'],[]).append(row)
 metrics={k:statistics.mean(statistics.mean(r[k] for r in group) for group in by.values()) for k in ['hit_at_1','precision','recall','f1']}
 out={'method':'strict namespace/kind/regex + GT aliases; true contributing factors only; all submitted failures retained; no semantic matching','n_incidents':len(by),'n_runs':len(rows),'macro_metrics':metrics,'rows':rows};pathlib.Path(a.out).write_text(json.dumps(out,indent=2));print(json.dumps(metrics))
if __name__=='__main__':main()
