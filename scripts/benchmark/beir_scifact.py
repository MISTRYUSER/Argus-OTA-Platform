#!/usr/bin/env python3
"""Download, validate, baseline and score the complete official BEIR SciFact test."""
import argparse
import csv
import hashlib
import importlib.metadata
import json
import math
import re
import statistics
import time
import urllib.request
import zipfile
from pathlib import Path

DATASET_URL='https://public.ukp.informatik.tu-darmstadt.de/thakur/BEIR/datasets/scifact.zip'
OFFICIAL_MD5='5f7d1de60b170fc8027bb7898e2efca1'
CARD_API='https://huggingface.co/api/datasets/BeIR/scifact'
KS=(1,3,5,10)

def jsonl(path):
    with open(path) as stream:
        return [json.loads(line) for line in stream if line.strip()]

def load_qrels(path):
    with open(path) as stream:
        rows=list(csv.DictReader(stream,delimiter='\t'))
    qrels={}
    for row in rows:
        qid,did,grade=row['query-id'],row['corpus-id'],int(row['score'])
        if did in qrels.get(qid,{}):raise ValueError('duplicate qrel')
        qrels.setdefault(qid,{})[did]=grade
    return qrels

def verify_dataset(root,expected_corpus=5183,expected_queries=300):
    docs=jsonl(root/'corpus.jsonl');queries=jsonl(root/'queries.jsonl');qrels=load_qrels(root/'qrels/test.tsv')
    dids={d['_id'] for d in docs};qids={q['_id'] for q in queries}
    if len(dids)!=len(docs) or len(qids)!=len(queries):raise ValueError('duplicate dataset IDs')
    if len(docs)!=expected_corpus or len(qrels)!=expected_queries:raise ValueError('incomplete SciFact test')
    if set(qrels)-qids or {d for rels in qrels.values() for d in rels}-dids:raise ValueError('qrels reference missing query or corpus ID')
    return {'corpus_count':len(docs),'all_queries_count':len(queries),'test_queries_count':len(qrels),'qrels_count':sum(map(len,qrels.values())),'positive_qrels_count':sum(grade>0 for rels in qrels.values() for grade in rels.values())}

def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()

def fetch(url,path):
    with urllib.request.urlopen(url,timeout=60) as response:
        path.write_bytes(response.read())

def download_dataset(dest):
    dest.mkdir(parents=True,exist_ok=True)
    archive=dest/'scifact.zip'
    if not archive.exists():fetch(DATASET_URL,archive)
    if hashlib.md5(archive.read_bytes()).hexdigest()!=OFFICIAL_MD5:raise ValueError('official SciFact archive MD5 mismatch')
    with zipfile.ZipFile(archive) as z:
        for name in z.namelist():
            p=Path(name)
            if p.is_absolute() or '..' in p.parts:raise ValueError('unsafe archive path')
        z.extractall(dest)
    fetch(CARD_API,dest/'card-api.json')
    revision=json.loads((dest/'card-api.json').read_text())['sha']
    card_url=f'https://huggingface.co/datasets/BeIR/scifact/raw/{revision}/README.md'
    fetch(card_url,dest/'dataset-card.md')
    if OFFICIAL_MD5 not in (dest/'dataset-card.md').read_text():raise ValueError('dataset card does not confirm expected archive MD5')
    references=dest/'official-references';references.mkdir(exist_ok=True)
    fetch('https://huggingface.co/api/datasets/BeIR/scifact-qrels',references/'qrels-api.json')
    qrels_revision=json.loads((references/'qrels-api.json').read_text())['sha']
    qrels_url=f'https://huggingface.co/datasets/BeIR/scifact-qrels/raw/{qrels_revision}/test.tsv'
    fetch(qrels_url,references/'test.tsv')
    if load_qrels(references/'test.tsv')!=load_qrels(dest/'scifact/qrels/test.tsv'):raise ValueError('immutable official qrels differ from archive')
    fetch('https://api.github.com/repos/beir-cellar/beir/commits/main',references/'beir-commit.json')
    beir_revision=json.loads((references/'beir-commit.json').read_text())['sha']
    for name in ('evaluation.py','custom_metrics.py'):
        fetch(f'https://raw.githubusercontent.com/beir-cellar/beir/{beir_revision}/beir/retrieval/{name}',references/name)
    counts=verify_dataset(dest/'scifact')
    manifest={'dataset':'BEIR SciFact','split':'test','license':'CC-BY-SA-4.0','archive_url':DATASET_URL,'archive_official_md5':OFFICIAL_MD5,'card_url':card_url,'card_revision':revision,'counts':counts,'official_qrels':{'revision':qrels_revision,'url':qrels_url,'parsed_rows_match_archive':True},'official_beir_evaluator_commit':beir_revision,'retrieved_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'files':{str(p.relative_to(dest)):sha(p) for p in sorted(dest.rglob('*')) if p.is_file() and p.name!='manifest.json'}}
    (dest/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    return manifest

def score_rows(qrels,rows):
    import pytrec_eval
    by_id={r['query_id']:r for r in rows}
    if len(by_id)!=len(rows) or set(by_id)!=set(qrels):raise ValueError('run must contain exactly every judged test query once')
    results={}
    for qid,row in by_id.items():
        ids=[h['corpus_id'] for h in row['hits']]
        if len(ids)!=len(set(ids)):raise ValueError('duplicate result corpus IDs')
        if row.get('error') and ids:raise ValueError('failed query cannot contain scored partial hits')
        # BEIR's default ignore_identical_ids=True protocol.
        results[qid]={h['corpus_id']:h['rank_derived_eval_score'] for h in row['hits'] if h['corpus_id']!=qid}
    evaluator=pytrec_eval.RelevanceEvaluator(qrels,{'recall.1,3,5,10','P.1,3,5,10','ndcg_cut.1,3,5,10','map_cut.1,3,5,10'})
    measured=evaluator.evaluate(results)
    per_query={}
    metric_names=[f'{prefix}_{k}' for prefix in ('recall','P','ndcg_cut','map_cut') for k in KS]
    for qid in qrels:per_query[qid]={name:measured.get(qid,{}).get(name,0.0) for name in metric_names}
    out={'query_count':len(qrels),'failure_count':sum(bool(r.get('error')) for r in rows),'empty_result_queries':sum(not r['hits'] for r in rows),'nonempty_result_queries':sum(bool(r['hits']) for r in rows)}
    for label,key in (('Recall','recall'),('P','P'),('NDCG','ndcg_cut'),('MAP','map_cut')):
        for k in KS:out[f'{label}@{k}']=sum(m[f'{key}_{k}'] for m in per_query.values())/len(qrels)
    # trec_eval reciprocal rank on each top-k is equivalent to BEIR's custom MRR@k.
    rr=pytrec_eval.RelevanceEvaluator(qrels,{'recip_rank'})
    for k in KS:
        truncated={qid:dict(sorted(hits.items(),key=lambda x:x[1],reverse=True)[:k]) for qid,hits in results.items()}
        mrr=rr.evaluate(truncated)
        out[f'MRR@{k}']=sum(mrr.get(qid,{}).get('recip_rank',0.0) for qid in qrels)/len(qrels)
        for qid in qrels:per_query[qid][f'MRR@{k}']=mrr.get(qid,{}).get('recip_rank',0.0)
    latencies=[]
    for row in rows:
        if 'latency_ms' not in row:continue
        value=row['latency_ms']
        if isinstance(value,bool) or not isinstance(value,(int,float)) or not math.isfinite(value) or value<0:raise ValueError('latency must be finite and nonnegative')
        latencies.append(value)
    latencies.sort()
    out['latency_ms']={'sample_count':len(latencies),'missing_count':len(rows)-len(latencies),'mean':statistics.mean(latencies) if latencies else None,'p50':latencies[len(latencies)//2] if latencies else None,'p95':latencies[min(len(latencies)-1,int(len(latencies)*.95))] if latencies else None,'total':sum(latencies)}
    out['evaluation']={'protocol':'BEIR/pytrec_eval macro over ALL test qrels, empty/error rows contribute zero','ignore_identical_ids':True,'scorer_package':'pytrec-eval-terrier','scorer_version':importlib.metadata.version('pytrec-eval-terrier'),'official_reference':'https://github.com/beir-cellar/beir/blob/main/beir/retrieval/evaluation.py','mrr_reference':'https://github.com/beir-cellar/beir/blob/main/beir/retrieval/custom_metrics.py'}
    return out,per_query

def run_baseline(data,out):
    if out.exists():raise FileExistsError(out)
    import numpy as np
    from rank_bm25 import BM25Okapi
    docs=jsonl(data/'corpus.jsonl');qrels=load_qrels(data/'qrels/test.tsv');queries=[q for q in jsonl(data/'queries.jsonl') if q['_id'] in qrels]
    tokens=lambda text:re.findall(r'\w+',text.lower())
    index=BM25Okapi([tokens(d['title']+' '+d['text']) for d in docs])
    rows=[]
    for q in sorted(queries,key=lambda q:q['_id']):
        start=time.perf_counter();scores=index.get_scores(tokens(q['text']));order=np.argsort(-scores,kind='stable')[:10]
        rows.append({'query_id':q['_id'],'query':q['text'],'hits':[{'corpus_id':docs[int(i)]['_id'],'rank_derived_eval_score':float(10-rank),'bm25_score':float(scores[i]),'rank':rank+1} for rank,i in enumerate(order)],'latency_ms':(time.perf_counter()-start)*1000})
    with out.open('x') as stream:stream.write(''.join(json.dumps(row)+'\n' for row in rows))
    return rows

def write_score_outputs(out,metrics,per_query):
    per_path=out.with_name(out.stem+'-per-query.json')
    for path in (out,per_path):
        if path.exists():raise FileExistsError(path)
    opened=[]
    try:
        for path in (out,per_path):opened.append((path,path.open('x')))
        for (_,stream),value in zip(opened,(metrics,per_query)):stream.write(json.dumps(value,indent=2)+'\n')
    except Exception:
        for path,stream in opened:
            stream.close();path.unlink()
        raise
    finally:
        for _,stream in opened:stream.close()

def main():
    parser=argparse.ArgumentParser();sub=parser.add_subparsers(dest='command',required=True)
    dl=sub.add_parser('download');dl.add_argument('--dest',type=Path,required=True)
    baseline=sub.add_parser('baseline');baseline.add_argument('--data',type=Path,required=True);baseline.add_argument('--out',type=Path,required=True)
    score=sub.add_parser('score');score.add_argument('--qrels',type=Path,required=True);score.add_argument('--run',type=Path,required=True);score.add_argument('--out',type=Path,required=True)
    args=parser.parse_args()
    if args.command=='download':print(json.dumps(download_dataset(args.dest)['counts']))
    elif args.command=='baseline':args.out.parent.mkdir(parents=True,exist_ok=True);run_baseline(args.data,args.out)
    else:
        metrics,per=score_rows(load_qrels(args.qrels),jsonl(args.run));metrics['run_sha256']=sha(args.run);metrics['qrels_sha256']=sha(args.qrels)
        args.out.parent.mkdir(parents=True,exist_ok=True);write_score_outputs(args.out,metrics,per);print(json.dumps(metrics,indent=2))
if __name__=='__main__':main()
