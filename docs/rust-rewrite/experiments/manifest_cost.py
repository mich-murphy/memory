# Cumulative manifest bytes per policy, from timestamps and offsets only (same inputs as batch_sweep.py).
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
PIECE=220; HEAD=600; CK=64
Q,M=60,300
data=0; full=0; delta=0; maxfull=0; counts=[]
for ev in sessions:
    first=None;last=None;n=0
    for t,off in ev:
        if first is not None and (t-last>=Q or t-first>=M):
            n+=1; first=None
        if first is None: first=t
        last=t
    n+=1
    counts.append(n)
    data+=ev[-1][1]
    f=sum(HEAD+PIECE*k for k in range(1,n+1)); full+=f; maxfull=max(maxfull,f)
    # delta: each capture writes HEAD + its own piece; every CK-th capture writes a full checkpoint
    d=sum((HEAD+PIECE*k) if k%CK==0 else (HEAD+PIECE) for k in range(1,n+1)); delta+=d
print(f"{len(sessions)} sessions, {sum(counts)} captures, transcript bytes {data/1e9:.2f} GB")
print(f"full rewrite: total manifest bytes {full/1e6:.0f} MB, worst session {maxfull/1e6:.1f} MB")
print(f"delta+checkpoint/{CK}: total manifest bytes {delta/1e6:.0f} MB")
print(f"manifest PUT bytes as share of transcript bytes: full {full/data:.1%}, delta {delta/data:.1%}")
