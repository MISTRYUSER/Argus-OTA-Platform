"""Download a predeclared ITBench-Lite SRE pilot, with hashes and blind inputs.
Ground truths are kept separately; they are never added to agent inputs.
"""
import argparse, concurrent.futures, hashlib, json, pathlib, urllib.request, time
REV = 'd0916b08ba421ce5e672e9ad68aa947d938dfef0'
VERSION = 'v0.2-B96DF826-4BB2-4B62-97AB-6D84254C53D7'
IDS = [1,2,4,5,6,7,8,9,11,12]  # ten lowest numeric scenario IDs; selected before labels.
def main():
 p=argparse.ArgumentParser();p.add_argument('--out',required=True);p.add_argument('--ids',default='');a=p.parse_args();ids=[int(x) for x in a.ids.split(',')] if a.ids else IDS;out=pathlib.Path(a.out);out.mkdir(parents=True,exist_ok=True)
 files=[]
 for sid in ids:
  path=f'snapshots/sre/{VERSION}/Scenario-{sid}'
  u=f'https://huggingface.co/api/datasets/ibm-research/ITBench-Lite/tree/{REV}/{path}?recursive=true&limit=1000'
  rows=json.load(urllib.request.urlopen(u,timeout=60))
  selected=[r for r in rows if r['type']=='file' and (r['path'].endswith(('ground_truth.yaml','k8s_events_raw.tsv','k8s_objects_raw.tsv')) or '/alerts/' in r['path'])]
  files.extend((sid,r) for r in selected)
 def download_once(item):
  sid,r=item;rel=r['path'].split(f'Scenario-{sid}/',1)[1];dest=out/f'Scenario-{sid}'/rel;dest.parent.mkdir(parents=True,exist_ok=True)
  if not dest.exists() or dest.stat().st_size!=r['size']:
   u=f'https://huggingface.co/datasets/ibm-research/ITBench-Lite/resolve/{REV}/{r["path"]}'
   with urllib.request.urlopen(u,timeout=180) as src,open(str(dest)+'.tmp','wb') as dst:
    while True:
     b=src.read(1024*1024)
     if not b:break
     dst.write(b)
   pathlib.Path(str(dest)+'.tmp').replace(dest)
  if dest.stat().st_size != r['size']: raise ValueError("download size mismatch")
  h=hashlib.sha256()
  with open(dest,'rb') as f:
   for b in iter(lambda:f.read(1024*1024),b''):h.update(b)
  return {'scenario':sid,'path':r['path'],'size':dest.stat().st_size,'sha256':h.hexdigest()}
 def download(item):
  for attempt in range(4):
   try:return download_once(item)
   except Exception:
    if attempt==3:raise
    time.sleep(0.5*(2**attempt))
 with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
  rows=list(pool.map(download,files))
 manifest={'benchmark':'IBM ITBench-Lite SRE','revision':REV,'version':VERSION,'scenario_ids':ids,'selection':'predeclared held-out IDs without labels' if a.ids else 'ten lowest numeric IDs, selected before labels','modalities':['alerts','k8s_events','k8s_objects'],'omitted':['metrics','otel_logs','otel_traces'],'license':'Apache-2.0','source':'https://huggingface.co/datasets/ibm-research/ITBench-Lite','files':rows}
 (out/'manifest.json').write_text(json.dumps(manifest,indent=2));print(json.dumps({'downloaded_files':len(rows),'bytes':sum(r['size'] for r in rows),'scenarios':ids}))
if __name__=='__main__':main()
