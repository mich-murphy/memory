#!/usr/bin/env python3
"""Frozen deterministic retrieval benchmark. See SCORING.md; stdlib only."""
import argparse, collections, hashlib, json, math, os, platform, re, shutil, sqlite3, subprocess, sys, time
from pathlib import Path
HERE = Path(__file__).resolve().parent
PROTO = HERE.parent / 'prototype'
VARIANTS = ['scoped_rg', 'map_only', 'lexical', 'map_lexical', 'hybrid']
STOP = set('a an and are as at be by can does do for from get how i in including into is it of on or the their this through to trace where which with would implemented implementation defined find code file files source docs logic rules determine'.split())
def terms(text):
    text = re.sub(r'([a-z])([A-Z])', r'\1 \2', text)
    return sorted({w[:-1] if len(w)>4 and w.endswith('s') else w for w in re.findall(r'[a-z0-9]+', text.lower()) if len(w)>1 and w not in STOP})
def execute(cmd, cwd=None):
    p = subprocess.run(cmd, cwd=cwd, capture_output=True)
    if p.returncode: raise RuntimeError(f'{cmd[:3]} exit {p.returncode}: {p.stderr.decode(errors="replace")[:1500]}')
    return p.stdout

def digest(path): return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def dump(path, obj): Path(path).write_text(json.dumps(obj, indent=2, ensure_ascii=False)+'\n')
def percentile(xs, q):
    if not xs: return None
    xs=sorted(xs); x=(len(xs)-1)*q; a=int(x); b=min(a+1,len(xs)-1)
    return xs[a]+(xs[b]-xs[a])*(x-a)
def qrels(task):
    result=[]
    for q in task.get('qrels',[]):
        if isinstance(q,str): q={'path':q}
        grade=q.get('grade',q.get('relevance',1))
        if not isinstance(grade,(int,float)): raise ValueError('qrel grade must be numeric')
        path=q['path']; kind=q.get('kind',q.get('type','doc' if path.startswith('docs/') or path.lower().endswith('.md') else 'source'))
        if kind in ('docs','documentation','entrypoint_doc','supporting_doc') or path.lower().endswith(('.md','.mdx','.rst')): kind='doc'
        groups=q.get('groups',[q.get('group',q.get('relevance_group',q.get('facet',path)))])
        if isinstance(groups,str): groups=[groups]
        result.append((path,grade,kind,groups))
    return result

def score(paths,task):
    out={}
    for kind in ('all','source','doc','test'):
        qs=[q for q in qrels(task) if q[1]>0 and (kind=='all' or q[2]==kind)]
        if not qs: out[kind]=None; continue
        relevant={q[0] for q in qs}; ranks=[i+1 for i,p in enumerate(paths) if p in relevant]
        m={'mrr':1/min(ranks) if ranks else 0}
        groups=collections.defaultdict(list)
        for p,g,_,gs in qs:
            for group in gs: groups[group].append((p,g))
        for k in (5,10):
            returned=set(paths[:k]); m[f'hit@{k}']=int(any(p in relevant for p in returned))
            coverage=[max([g for p,g in items if p in returned] or [0])/max(g for _,g in items) for items in groups.values()]
            m[f'graded_group_coverage@{k}']=sum(coverage)/len(coverage)
            m[f'group_coverage@{k}']=sum(v>0 for v in coverage)/len(coverage)
        out[kind]=m
    return out

def bounded(results,k,budget):
    chosen=[]
    for r in results[:k]:
        trial=chosen+[r]
        raw=json.dumps({'results':trial},ensure_ascii=False,separators=(',',':'))+'\n'
        if len(raw.encode())>budget: break
        chosen=trial
    return chosen,json.dumps({'results':chosen},ensure_ascii=False,separators=(',',':'))+'\n'

class Baseline:
    def __init__(self,repo,paths,routes): self.repo,self.paths,self.routes=repo,paths,routes
    def locate(self,query):
        ts=terms(query)
        route_scores=[(len(set(ts)&set(terms(r['terms']))),i,r) for i,r in enumerate(self.routes)]
        selected=[r for s,i,r in sorted(route_scores,key=lambda t:(-t[0],t[1]))[:3] if s>0]
        prefixes=[p for r in selected for p in r['prefixes']+r.get('docs',[])]
        scoped=[p for p in self.paths if not prefixes or any(p==x or p.startswith(x+'/') for x in prefixes)]
        scores={p:3*sum(t in ' '.join(terms(p)) for t in ts) for p in scoped}
        batch=[]; size=0
        def search(files):
            if not files or not ts:return
            cmd=['rg','--json','--max-count','30','--ignore-case','--fixed-strings']
            for t in ts:cmd+=['-e',t]
            p=subprocess.run(cmd+['--']+files,cwd=self.repo,capture_output=True)
            if p.returncode not in (0,1):raise RuntimeError(p.stderr.decode(errors='replace')[:1000])
            hits=collections.defaultdict(set)
            for line in p.stdout.splitlines():
                row=json.loads(line)
                if row['type']=='match':
                    d=row['data']; text=d['lines'].get('text','').lower(); path=d['path']['text']
                    hits[path].update(t for t in ts if t in text)
            for path,hit in hits.items():scores[path]+=len(hit)
        for p in scoped:
            if size+len(p)>80000:search(batch);batch=[];size=0
            batch.append(p);size+=len(p)+1
        search(batch)
        return [{'path':p,'score':s,'reason':'scoped filename(3)/rg(1) distinct-term overlap'} for p,s in sorted(scores.items(),key=lambda x:(-x[1],x[0])) if s>0]

# Candidate adapter intentionally isolated: align to documented CLI before running,
# without inspecting outcomes or changing retrieval/scoring.
def candidate(args,mode,query):
    mode={'map_only':'map-only','map_lexical':'map'}.get(mode,mode)
    raw=execute([sys.executable,str(PROTO/'nav.py'),'--root',args.repo,'--db',str(args.index),'query','--mode',mode,'--limit',str(args.top_k),'--max-bytes',str(args.max_bytes),query])
    obj=json.loads(raw)
    results=obj.get('results',obj.get('hits',[]))
    return results,raw.decode()

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--repo',default='/Users/mm/businesscraft/businesscraft');p.add_argument('--tasks',type=Path,default=HERE/'tasks.json')
    p.add_argument('--output',type=Path,default=HERE/'first_run');p.add_argument('--index',type=Path,default=PROTO/'.cache/benchmark.sqlite3')
    p.add_argument('--repeats',type=int,choices=(3,4,5),default=3);p.add_argument('--top-k',type=int,default=10);p.add_argument('--max-bytes',type=int,default=9600)
    p.add_argument('--self-test',action='store_true');args=p.parse_args()
    if args.self_test:
        t={'qrels':[{'path':'a','grade':3,'group':'g','kind':'source'},{'path':'b','grade':1,'group':'g','kind':'source'}]}
        assert score(['x','b'],t)['source']['graded_group_coverage@5']==1/3
        assert score(['x','b'],t)['source']['mrr']==.5
        assert len(bounded([{'path':'é'*100}],10,80)[1].encode())<=80
        print('self-test passed');return
    if args.output.exists():raise SystemExit('Refusing overwrite: preserve first results; choose a new --output')
    tasks=json.loads(args.tasks.read_text())
    if any(not t.get('qrels') for t in tasks['tasks']):raise SystemExit('Qrels not ready; no run created')
    args.output.mkdir(parents=True);args.index.parent.mkdir(parents=True,exist_ok=True)
    manifest={'started_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'arguments':{k:str(v) for k,v in vars(args).items()},'machine':platform.platform(),'python':sys.version,'head':execute(['git','rev-parse','HEAD'],args.repo).decode().strip(),'tracked_status':execute(['git','--no-optional-locks','status','--porcelain','--untracked-files=no'],args.repo).decode(),'hashes':{},'scoring_contract_sha256':digest(HERE/'SCORING.md')}
    for root in (HERE,PROTO):
        for f in root.glob('*'):
            if f.is_file() and f.suffix in ('.py','.json','.md'):manifest['hashes'][str(f.relative_to(HERE.parent))]=digest(f)
    manifest['tools']={x:shutil.which(x) for x in ('git','rg','python3')};dump(args.output/'manifest.json',manifest)
    start=time.perf_counter()
    try:
        build=execute([sys.executable,str(PROTO/'nav.py'),'--root',args.repo,'--db',str(args.index),'build'])
        (args.output/'build.stdout').write_bytes(build);manifest['build_seconds']=time.perf_counter()-start
        with sqlite3.connect('file:'+str(args.index)+'?mode=ro',uri=True) as db:
            paths=[r[0] for r in db.execute("SELECT path FROM nodes WHERE kind='file' ORDER BY path")]
        manifest['baseline_allowed_paths']=len(paths)
        manifest['baseline_manifest_sha256']=hashlib.sha256('\n'.join(paths).encode()).hexdigest()
        baseline=Baseline(args.repo,paths,json.loads((PROTO/'routing.json').read_text())['routes'])
        rows=[]
        with (args.output/'queries.jsonl').open('w') as log:
            for repeat in range(args.repeats+1):
                order=VARIANTS[repeat%5:]+VARIANTS[:repeat%5]
                task_order=tasks['tasks'][repeat:]+tasks['tasks'][:repeat]
                for ti,task in enumerate(task_order):
                    rotated=order[ti%5:]+order[:ti%5]
                    for variant in rotated:
                        start=time.perf_counter();row={'task_id':task['id'],'split':task['split'],'variant':variant,'repeat':repeat,'phase':'cold_first' if repeat==0 else 'warm','query':task['query']}
                        try:
                            if variant=='scoped_rg':ranking=baseline.locate(task['query']);native=None
                            else:ranking,native=candidate(args,variant,task['query'])
                            latency=time.perf_counter()-start
                            results,raw=bounded(ranking,args.top_k,args.max_bytes)
                            native_meta=json.loads(native) if native else {}
                            row.update(freshness_seconds=native_meta.get('freshness_seconds'),retrieval_seconds=native_meta.get('retrieval_seconds'),seconds=latency,ranking=ranking,results=results,output=raw,output_bytes=len(raw.encode()),char_div_4_token_proxy=len(raw)/4,native_output=native,native_output_bytes=len(native.encode()) if native else None,metrics=score([r['path'] for r in results],task))
                        except Exception as e:row.update(seconds=time.perf_counter()-start,error=str(e))
                        rows.append(row);log.write(json.dumps(row,ensure_ascii=False)+'\n');log.flush()
        summary={}
        for variant in VARIANTS:
            selected=[r for r in rows if r['variant']==variant];warm=[r['seconds'] for r in selected if r['phase']=='warm'];cold=[r['seconds'] for r in selected if r['phase']=='cold_first']
            component_timings={key:{'p50':percentile([r[key] for r in selected if r['phase']=='warm' and r.get(key) is not None],.5),'p95':percentile([r[key] for r in selected if r['phase']=='warm' and r.get(key) is not None],.95)} for key in ('freshness_seconds','retrieval_seconds')}
            v={'warm_component_seconds':component_timings,'errors':sum('error'in r for r in selected),'warm_seconds_p50':percentile(warm,.5),'warm_seconds_p95':percentile(warm,.95),'cold_first_seconds_p50':percentile(cold,.5),'cold_first_seconds_p95':percentile(cold,.95),'splits':{}}
            for split in ('dev','test'):
                rs=[r for r in selected if r['repeat']==0 and r['split']==split and 'metrics'in r];s={}
                for kind in ('all','source','doc','test'):
                    ms=[r['metrics'][kind] for r in rs if r['metrics'][kind] is not None]
                    s[kind]={k:sum(m[k] for m in ms)/len(ms) for k in ms[0]} if ms else None
                s['tasks']=len(rs);s['mean_output_bytes']=sum(r['output_bytes'] for r in rs)/len(rs) if rs else None
                v['splits'][split]=s
            summary[variant]=v
        dump(args.output/'summary.json',summary)
    except Exception as e:
        manifest['fatal_error']=str(e);raise
    finally:
        manifest['completed_utc']=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime());dump(args.output/'manifest.json',manifest)
    print(f'Results saved: {args.output}; see summary.json')
if __name__=='__main__':main()
