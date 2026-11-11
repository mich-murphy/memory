d=$(mktemp -d) || exit 1
cat > $d/bench.py <<'PYEOF'
import os, sys, time, json, fcntl, hashlib, tempfile, shutil, platform
MAC = sys.platform == "darwin"
F_FULL = getattr(fcntl, "F_FULLFSYNC", None)
F_BARRIER = 85  # F_BARRIERFSYNC on Darwin
def full(fd):
    if MAC: fcntl.fcntl(fd, F_FULL)
    else: os.fsync(fd)
def barrier(fd):
    if MAC: fcntl.fcntl(fd, F_BARRIER)
    else: os.fdatasync(fd)
def plain(fd): os.fsync(fd)
def nosync(fd): pass
BARRIERS = {"full": full, "plain": plain, "nosync": nosync}
if MAC: BARRIERS["barrier"] = barrier
else: BARRIERS["fdatasync"] = barrier
cnt = [0]
def durable(d, name, body, sync):
    cnt[0] += 1
    tmp = os.path.join(d, f".{name}.tmp-{cnt[0]}")
    fd = os.open(tmp, os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
    os.write(fd, body); sync(fd); os.close(fd)
    os.rename(tmp, os.path.join(d, name))
    dfd = os.open(d, os.O_RDONLY); sync(dfd); os.close(dfd)
def pct(xs, p):
    xs = sorted(xs); return xs[min(len(xs)-1, int(len(xs)*p))]
def stats(lat, total, nbytes):
    return {"n": len(lat), "p50_ms": pct(lat,.5)*1e3, "p99_ms": pct(lat,.99)*1e3,
            "mean_ms": sum(lat)/len(lat)*1e3, "per_s": len(lat)/total}
def body(n, i): return (b"%d:" % i) + os.urandom(n//2).hex().encode()[:n]
def run(root, n_small, sizes):
    out = {}
    for bname, sync in BARRIERS.items():
        N = n_small * (20 if bname=="nosync" else 1)
        for sz in sizes:
            d = tempfile.mkdtemp(dir=root); lat=[]; t0=time.perf_counter()
            for i in range(N):
                b = body(sz, i); t=time.perf_counter(); durable(d,"f",b,sync); lat.append(time.perf_counter()-t)
            out[f"c1_rename_{bname}_{sz}"] = stats(lat, time.perf_counter()-t0, sz)
            shutil.rmtree(d)
    # candidate 5: append log, fsync per append, with compaction each 100 appends (snapshot via durable)
    for bname in ("full","plain","barrier" if MAC else "fdatasync"):
        sync = BARRIERS[bname]
        for rec, snap in ((1024, 100*1024),):
            d = tempfile.mkdtemp(dir=root); lat=[]; t0=time.perf_counter()
            fd = os.open(os.path.join(d,"log"), os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600)
            dfd = os.open(d, os.O_RDONLY); sync(dfd)
            for i in range(n_small):
                b = body(rec,i)+b"\n"; t=time.perf_counter(); os.write(fd,b); sync(fd)
                if i%100==99: durable(d,"snap",body(snap,i),sync); os.ftruncate(fd,0); sync(fd)
                lat.append(time.perf_counter()-t)
            out[f"c5_log_{bname}_1024_snap100k_per100"] = stats(lat, time.perf_counter()-t0, rec)
            shutil.rmtree(d)
    # append only, no compaction
    for bname in ("full","plain"):
        sync = BARRIERS[bname]; d = tempfile.mkdtemp(dir=root); lat=[]; t0=time.perf_counter()
        fd = os.open(os.path.join(d,"log"), os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600)
        for i in range(n_small):
            b = body(1024,i)+b"\n"; t=time.perf_counter(); os.write(fd,b); sync(fd); lat.append(time.perf_counter()-t)
        out[f"c5_appendonly_{bname}_1024"] = stats(lat, time.perf_counter()-t0, 1024)
        shutil.rmtree(d)
    # candidate 3: skip-unchanged (digest compare), 90% unchanged; cost of the skip itself
    d = tempfile.mkdtemp(dir=root); last=None; lat=[]; t0=time.perf_counter()
    for i in range(n_small*10):
        b = body(100*1024, i//10)  if i%10==0 else prev
        prev=b; t=time.perf_counter(); h=hashlib.sha256(b).digest()
        if h!=last: durable(d,"f",b,full); last=h
        lat.append(time.perf_counter()-t)
    out["c3_skip_unchanged_90pct_full_100k"] = stats(lat, time.perf_counter()-t0, 0)
    shutil.rmtree(d)
    # candidate 4: group commit: K logical updates coalesced into one durable write
    for K in (1, 10, 60):
        d = tempfile.mkdtemp(dir=root); lat=[]; t0=time.perf_counter(); b=None
        for i in range(n_small*K):
            t=time.perf_counter(); b=body(100*1024,i) if False else b
            if i%K==K-1: durable(d,"f",body(100*1024,i),full)
            lat.append(time.perf_counter()-t)
        out[f"c4_coalesce_K{K}_full_100k_per_logical_update"] = stats(lat, time.perf_counter()-t0, 0)
        shutil.rmtree(d)
    return out
if __name__=="__main__":
    root=sys.argv[1]; n=int(sys.argv[2]); sizes=[1024,10240,102400,512000]
    t=time.time(); r=run(root,n,sizes)
    print(json.dumps({"platform":platform.platform(),"n_small":n,"started":t,"results":r}))
PYEOF
(uname -sr; df -T $d | tail -1; mount | grep -E " / " | head -1; lsblk -d -o NAME,ROTA,MODEL 2>/dev/null | head -5) > $d/env.txt
python3 -I $d/bench.py $d 200 > $d/res.json 2> $d/err.txt; echo "BENCHSTATUS $?" >&2
echo ENVBEGIN; cat $d/env.txt; echo RESBEGIN; cat $d/res.json; echo ERR; tail -3 $d/err.txt
rm -rf $d; ls -d $d 2>&1 | sed 's/^/AFTER: /'
