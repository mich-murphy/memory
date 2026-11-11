import os, sys, json, glob, statistics, time
from datetime import datetime, timezone

HOME = os.path.expanduser("~")
ROOTS = {
  "claude": glob.glob(f"{HOME}/.claude/projects/*/*.jsonl") + glob.glob(f"{HOME}/.claude/projects/*/*/subagents/*.jsonl"),
  "codex": glob.glob(f"{HOME}/.codex/sessions/**/*.jsonl", recursive=True),
  "pi": glob.glob(f"{HOME}/.pi/agent/sessions/**/*.jsonl", recursive=True),
}

def ts_of(obj):
    for k in ("timestamp","ts","time","created_at"):
        v = obj.get(k)
        if isinstance(v,str):
            try:
                return datetime.fromisoformat(v.replace("Z","+00:00")).timestamp()
            except Exception:
                pass
        if isinstance(v,(int,float)):
            return v/1000 if v > 1e11 else v
    return None

def events(path):
    out=[]; off=0
    with open(path,"rb") as f:
        for line in f:
            off += len(line)
            try: obj=json.loads(line)
            except Exception: continue
            if not isinstance(obj,dict): continue
            t=ts_of(obj)
            if t is None: continue
            out.append((t,off))
    return out

def simulate(ev, Q, M):
    """captures when quiet >= Q since last event or M since first uncaptured event. returns list of sizes at capture"""
    caps=[]; first=None; last=None; lastoff=0
    for t,off in ev:
        if first is not None and (t-last >= Q or t-first >= M):
            caps.append(lastoff); first=None
        if first is None: first=t
        last=t; lastoff=off
    if first is not None: caps.append(lastoff)
    return caps

def pct(xs,p):
    xs=sorted(xs); 
    return xs[min(len(xs)-1, int(round((p/100)*(len(xs)-1))))] if xs else 0

t0=time.time()
for name,files in ROOTS.items():
    rows=[]
    for p in files:
        try: ev=events(p)
        except Exception: continue
        if len(ev)<2: continue
        size=os.path.getsize(p)
        r={"records":len(ev),"size":size,"dur":ev[-1][0]-ev[0][0]}
        for Q,M in ((30,300),(60,600),(300,1800)):
            caps=simulate(ev,Q,M)
            r[f"caps_{Q}"]=len(caps)
            r[f"whole_{Q}"]=sum(caps)
        rows.append(r)
    if not rows: print(name,"no files"); continue
    n=len(rows)
    tot=sum(r["size"] for r in rows)
    print(f"\n== {name}: {n} session files with >=2 timestamped records, total {tot/1e6:.1f} MB, records p50 {pct([r['records'] for r in rows],50)} p99 {pct([r['records'] for r in rows],99)}")
    print(f"   size p50 {pct([r['size'] for r in rows],50)/1e3:.1f} KB  p90 {pct([r['size'] for r in rows],90)/1e6:.2f} MB  p99 {pct([r['size'] for r in rows],99)/1e6:.2f} MB  max {max(r['size'] for r in rows)/1e6:.1f} MB")
    print(f"   per-turn-equivalent (every record a capture): whole bytes = {sum(r['size']*r['records']/2 for r in rows)/1e9:.2f} GB approx (k/2 model)")
    for Q in (30,60,300):
        caps=[r[f"caps_{Q}"] for r in rows]; whole=sum(r[f"whole_{Q}"] for r in rows)
        big=[r for r in rows if r["size"]>8e6]
        bigamp = (sum(r[f"whole_{Q}"] for r in big)/sum(r["size"] for r in big)) if big else 0
        print(f"   Q={Q:>3}s: captures total {sum(caps):>7}  per session p50 {pct(caps,50)} p90 {pct(caps,90)} p99 {pct(caps,99)} max {max(caps)} | whole-object upload {whole/1e9:.2f} GB = {whole/tot:.1f}x delta ({tot/1e9:.2f} GB) | sessions>8MB: {len(big)}, their amp {bigamp:.1f}x, PUTs {3*sum(caps)}")
print(f"\nwalltime {time.time()-t0:.1f}s")
