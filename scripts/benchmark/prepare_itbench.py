"""Label-blind, deterministic evidence adapter. No ground-truth file is read."""
import argparse,csv,json,pathlib,hashlib
csv.field_size_limit(16 * 1024 * 1024)

def records(folder):
 lines=[];record_index=0
 def add(value):
  nonlocal record_index
  record_index+=1
  text=json.dumps(value,ensure_ascii=False,separators=(',',':'))
  for i in range(0,len(text),350):
   lines.append(f'record:{record_index} part:{i//350} '+text[i:i+350])
 for path in sorted((folder/'alerts').glob('alerts_in_alerting_state*')):
  data=json.loads(path.read_text())
  alerts=data if isinstance(data,list) else data.get('data',data)
  if isinstance(alerts,dict):alerts=alerts.get('alerts', [alerts])
  for row in alerts:add({'source':path.name,'alert':row})
 event_latest={}
 with open(folder/'k8s_events_raw.tsv') as f:
  for row in csv.DictReader(f,delimiter='\t'):
   try:
    x=json.loads(row['Body']);x=x.get('object',x)
    if x.get('type')!='Warning':continue
    obj=x.get('involvedObject',{});key=(obj.get('namespace'),obj.get('name'),x.get('reason'),x.get('message'))
    event_latest[key]={'source':'k8s_events','time':row['TimestampTime'],'entity':obj,'reason':x.get('reason'),'message':x.get('message')}
   except (ValueError,KeyError):continue
 for _,r in sorted(event_latest.items(),key=lambda kv:str(kv[0])):add(r)
 latest={}
 with open(folder/'k8s_objects_raw.tsv') as f:
  for row in csv.DictReader(f,delimiter='\t'):
   try:
    x=json.loads(row['Body']);x=x.get('object',x);m=x.get('metadata',{});key=(m.get('namespace',''),x.get('kind',''),m.get('name',''))
    if key[0]!='otel-demo':continue
    for k in ['managedFields','annotations']:m.pop(k,None)
    x['metadata']={k:m[k] for k in ['namespace','name','labels','creationTimestamp'] if k in m}
    if key not in latest or row['TimestampTime'] >= latest[key]['time']:
     latest[key]={'source':'k8s_objects','time':row['TimestampTime'],'object':x}
   except (ValueError,KeyError):continue
 for _,r in sorted(latest.items()):add(r)
 return lines

def main():
 p=argparse.ArgumentParser();p.add_argument('--data',required=True);p.add_argument('--out',required=True);a=p.parse_args();d=pathlib.Path(a.data);manifest=json.loads((d/'manifest.json').read_text());inputs=[]
 for sid in manifest['scenario_ids']:
  lines=records(d/f'Scenario-{sid}');inputs.append({'id':str(sid),'lines':lines,'sha256':hashlib.sha256('\n'.join(lines).encode()).hexdigest()})
 out={'benchmark':manifest['benchmark'],'revision':manifest['revision'],'adapter':'label-blind-v1: alerting snapshot + warning events + latest otel-demo Kubernetes objects; full adapted records chunked into 350 chars with record/part IDs; metrics/logs/traces omitted','cases':inputs}
 pathlib.Path(a.out).write_text(json.dumps(out,ensure_ascii=False,indent=2));print(json.dumps({x['id']:len(x['lines']) for x in inputs}))
if __name__=='__main__':main()
