import os, json, glob, time
from datetime import datetime
HOME=os.path.expanduser("~")
ROOTS={"claude": glob.glob(f"{HOME}/.claude/projects/*/*.jsonl")+glob.glob(f"{HOME}/.claude/projects/*/*/subagents/*.jsonl"),
       "pi": glob.glob(f"{HOME}/.pi/agent/sessions/**/*.jsonl",recursive=True)}
def ts_of(o):
    for k in ("timestamp","ts","time","created_at"):
        v=o.get(k)
        if isinstance(v,str):
            try: return datetime.fromisoformat(v.replace("Z","+00:00")).timestamp()
            except Exception: pass
        if isinstance(v,(int,float)): return v/1000 if v>1e11 else v
def events(p):
    out=[];off=0
    with open(p,"rb") as f:
        for line in f:
            off+=len(line)
            try:o=json.loads(line)
            except Exception: continue
            if isinstance(o,dict):
                t=ts_of(o)
                if t is not None: out.append((t,off))
    return out
def captures(ev,Q=30,M=300):
    caps=[];first=None;last=None;lo=0
    for t,off in ev:
        if first is not None and (t-last>=Q or t-first>=M): caps.append(lo);first=None
        if first is None: first=t
        last=t;lo=off
    if first is not None: caps.append(lo)
    return caps
def policy(caps,T,K):
    """T: whole-object below T bytes, tail pieces at/above. K: consolidate (re-upload whole) when pieces>K. returns (bytes, puts, maxpieces, consolidations)"""
    up=0;puts=0;pieces=0;mx=0;prev=0;cons=0
    for s in caps:
        if T is None or s<T: up+=s;puts+=1;pieces=1
        else:
            up+=s-prev;puts+=1;pieces+=1
            if K and pieces>K: up+=s;puts+=1;pieces=1;cons+=1
        prev=s;mx=max(mx,pieces)
    return up,puts,mx,cons
def pct(xs,p):
    xs=sorted(xs);return xs[min(len(xs)-1,int(round(p/100*(len(xs)-1))))] if xs else 0
for name,files in ROOTS.items():
    rows=[]
    for p in files:
        try: ev=events(p)
        except Exception: continue
        if len(ev)<2: continue
        rows.append((os.path.getsize(p),captures(ev),ev[-1][0]-ev[0][0]))
    tot=sum(s for s,_,_ in rows)
    print(f"\n== {name}: {len(rows)} sessions, {tot/1e9:.2f} GB; session duration p50 {pct([d for _,_,d in rows],50)/3600:.1f} h p99 {pct([d for _,_,d in rows],99)/3600/24:.1f} d max {max(d for _,_,d in rows)/3600/24:.1f} d")
    print(f"{'policy':<34}{'upload GB':>10}{'x delta':>9}{'PUT objs':>10}{'max pieces':>12}{'consol.':>9}")
    for label,T,K in (("whole always",None,0),("tails always, never consolidate",0,0),("tails always, consolidate >64",0,64),("tails always, consolidate >16",0,16),
                      ("whole <1MB, tails above, >64",1e6,64),("whole <4MB, tails above, >64",4e6,64),("whole <8MB, tails above, >64",8e6,64),("whole <8MB, tails above, >16",8e6,16)):
        up=puts=cons=0;mxs=[]
        for s,caps,_ in rows:
            u,pu,mx,c=policy(caps,T,K);up+=u;puts+=pu;mxs.append(mx);cons+=c
        print(f"{label:<34}{up/1e9:>10.2f}{up/tot:>9.1f}{puts:>10}{max(mxs):>12}{cons:>9}")
