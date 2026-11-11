import os, json, glob
from datetime import datetime
HOME=os.path.expanduser("~")
FILES=glob.glob(f"{HOME}/.claude/projects/*/*.jsonl")+glob.glob(f"{HOME}/.claude/projects/*/*/subagents/*.jsonl")+glob.glob(f"{HOME}/.pi/agent/sessions/**/*.jsonl",recursive=True)+glob.glob(f"{HOME}/.codex/sessions/**/*.jsonl",recursive=True)
def ts_of(o):
    for k in ("timestamp","ts","time","created_at"):
        v=o.get(k)
        if isinstance(v,str):
            try: return datetime.fromisoformat(v.replace("Z","+00:00")).timestamp()
            except Exception: pass
        if isinstance(v,(int,float)): return v/1000 if v>1e11 else v
sessions=[]
for p in FILES:
    ev=[];off=0
    try:
        with open(p,"rb") as f:
            for line in f:
                off+=len(line)
                try:o=json.loads(line)
                except Exception: continue
                if isinstance(o,dict):
                    t=ts_of(o)
                    if t is not None: ev.append((t,off))
    except Exception: continue
    if len(ev)>=2: sessions.append(ev)
tot=sum(ev[-1][1] for ev in sessions); nrec=sum(len(ev) for ev in sessions)
print(f"{len(sessions)} sessions, {nrec} records, {tot/1e9:.2f} GB")
def pct(xs,p):
    xs=sorted(xs);return xs[min(len(xs)-1,int(round(p/100*(len(xs)-1))))] if xs else 0
print(f"{'quiet Q':>8}{'cap M':>7}{'captures':>10}{'PUTs(x3)':>10}{'pieces p99':>11}{'max':>6}{'lat mean s':>11}{'lat p99 s':>10}{'tail p50 KB':>12}{'tail p99 KB':>12}")
for Q,M in ((5,60),(15,120),(30,300),(60,300),(60,600),(120,600),(300,1800),(600,3600)):
    caps=0;lat=[];pieces=[];tails=[]
    for ev in sessions:
        first=None;last=None;lo=0;pending=[];n=0;prev=0
        for t,off in ev:
            if first is not None and (t-last>=Q or t-first>=M):
                ct=max(last+Q, first+M) if t-first>=M and t-last<Q else last+Q
                ct=min(ct,t)  # capture happens no later than next event arrival in this model
                lat.extend(ct-pt for pt in pending); pending=[]; n+=1; tails.append(lo-prev); prev=lo; first=None
            if first is None: first=t
            last=t;lo=off;pending.append(t)
        ct=last+Q; lat.extend(ct-pt for pt in pending); n+=1; tails.append(lo-prev)
        caps+=n;pieces.append(n)
    print(f"{Q:>8}{M:>7}{caps:>10}{3*caps:>10}{pct(pieces,99):>11}{max(pieces):>6}{sum(lat)/len(lat):>11.0f}{pct(lat,99):>10.0f}{pct(tails,50)/1e3:>12.0f}{pct(tails,99)/1e3:>12.0f}")
