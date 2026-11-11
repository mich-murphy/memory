import os, json, glob, subprocess, sys
from datetime import datetime
HOME=os.path.expanduser("~")
def ts_of(o):
    for k in ("timestamp","ts","time","created_at"):
        v=o.get(k)
        if isinstance(v,str):
            try: return datetime.fromisoformat(v.replace("Z","+00:00")).timestamp()
            except Exception: pass
cands=sorted(glob.glob(f"{HOME}/.claude/projects/*/*.jsonl"), key=os.path.getsize)
picks=[cands[len(cands)//2], cands[int(len(cands)*0.9)], cands[int(len(cands)*0.99)], cands[-1]]
def zsize(b): return len(subprocess.run(["zstd","-3","-q","--stdout"],input=b,capture_output=True).stdout)
for p in picks:
    data=open(p,"rb").read(); ev=[];off=0
    for line in data.split(b"\n"):
        if not line: continue
        off+=len(line)+1
        try:o=json.loads(line)
        except Exception: continue
        t=ts_of(o) if isinstance(o,dict) else None
        if t: ev.append((t,off))
    caps=[];first=None;last=None;lo=0
    for t,o in ev:
        if first is not None and (t-last>=30 or t-first>=300): caps.append(lo);first=None
        if first is None: first=t
        last=t;lo=o
    caps.append(lo)
    whole=zsize(data); prev=0; pieces=0
    for c in caps: pieces+=zsize(data[prev:c]); prev=c
    print(f"size {len(data)/1e3:>8.0f} KB  pieces {len(caps):>4}  whole->{whole/1e3:>7.0f} KB  pieces->{pieces/1e3:>7.0f} KB  penalty {pieces/whole:.2f}x  vs whole-per-capture {sum(c for c in caps)/len(data):.1f}x")
