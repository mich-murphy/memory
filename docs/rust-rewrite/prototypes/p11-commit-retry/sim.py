#!/usr/bin/env python3
"""P9 commit-retry simulation. Deterministic (seeded), stdlib only. Run: python3 -I sim.py [seeds]"""
import heapq, itertools, json, math, random, sys, time, platform
from collections import deque

H = 3600.0
DUR, ARR_END, T_FAULT = 4 * H, 3 * H, 1 * H
N, QUIET, CAP = 100, 60.0, 300.0
PUT_MED, PUT_SIG, GET_MED, GET_SIG = 0.172, 0.22, 0.067, 0.35  # from r2-raw.json
TIMEOUT, CAP_NORMAL = 10.0, 4.0  # client timeout s; server capacity req/s (10x healthy offered load)
SCEN = {
    'healthy': dict(p=0.0005),
    'err1': dict(p=0.01), 'err5': dict(p=0.05), 'err20': dict(p=0.20),
    'outage30s': dict(p=0.0005, outage=(T_FAULT, T_FAULT + 30)),
    'outage5m': dict(p=0.0005, outage=(T_FAULT, T_FAULT + 300)),
    'storm5m': dict(p=0.0005, storm=(T_FAULT, T_FAULT + 300, 0.3)),
}
APP = {
    'a': dict(pause='fixed', rebase=4, cap='det', bucket=False),
    'b': dict(pause='full', rebase=4, cap='det', bucket=False),
    'c': dict(pause='decor', rebase=4, cap='det', bucket=False),
    'd': dict(pause='full', rebase=0, cap='window', bucket=False),
    'e': dict(pause='full', rebase=4, cap='det', bucket=True),
    'f': dict(pause='full', rebase=4, cap='transient', bucket=False),
    'g': dict(pause='full', rebase=4, cap='transient', bucket=True),
    'h': dict(pause='fixed', rebase=4, cap='full', bucket=False),
}

class Owner:
    def __init__(s, i):
        s.i = i; s.produced = 0.0; s.landed_to = 0.0; s.chunks = deque()
        s.first = None; s.last = 0.0; s.busy = False; s.followup = False
        s.etag = 0; s.mid = 0; s.needs_recover = True; s.streak = 0; s.emit = 0.0
        s.on_until = 0.0; s.own = set(); s.dec_prev = 0.5

class Sim:
    def __init__(s, seed, scen, app):
        s.rng = random.Random(seed); s.sc = SCEN[scen]; s.ap = APP[app]
        s.t = 0.0; s.heap = []; s.cnt = itertools.count()
        s.win = deque(); s.reqs = 0; s.peak = 0.0
        s.tok = 10.0; s.tok_t = 0.0
        s.srv_etag = [0] * N; s.srv_mid = [0] * N; s.mids = {0: (-1, 0.0, -1)}; s.next_mid = 1
        s.landed_count = {}; s.owners = [Owner(i) for i in range(N)]
        s.lat = []; s.lags = []; s.landed = 0; s.wasted = 0; s.stalled = []
        s.v = dict(two_inflight=0, dup_landing=0, nonmonotone=0, foreign=0)
        s.st = dict(own_adopt=0, zombie_landed=0, zombie_fenced=0, lost_reply_ours=0, base_unchanged_exhaust=0, landing_unknown=0, cap_fail=0, bucket_denied=0)
    def at(s, t, fn, *a): heapq.heappush(s.heap, (t, next(s.cnt), fn, a))
    def lat_(s, m, sg): return m * math.exp(s.rng.gauss(0, sg))
    def take(s):
        if not s.ap['bucket']: return True
        s.tok = min(10.0, s.tok + (s.t - s.tok_t) * 1.0); s.tok_t = s.t
        if s.tok >= 1: s.tok -= 1; return True
        s.st['bucket_denied'] += 1; return False
    def admit(s):
        t = s.t; s.reqs += 1; w = s.win; w.append(t)
        while w and w[0] < t - 10: w.popleft()
        load = len(w) / 10.0; s.peak = max(s.peak, load)
        o = s.sc.get('outage')
        if o and o[0] <= t < o[1]: return 'timeout'
        cap = CAP_NORMAL; st = s.sc.get('storm')
        if st and st[0] <= t < st[1]: cap = st[2]
        if load > cap and s.rng.random() < 1 - cap / load: return 'reject'
        if s.rng.random() < s.sc['p']: return 'err'
        return 'ok'
    def simple(s, med, sg):
        out = s.admit()
        d = {'ok': s.lat_(med, sg), 'reject': 0.1, 'err': 0.15}.get(out, TIMEOUT)
        yield d
        return out
    def land(s, i, etag, mid):
        if s.srv_etag[i] != etag: return None
        _, to, _ = s.mids[mid]
        if to < s.mids[s.srv_mid[i]][1]: s.v['nonmonotone'] += 1
        if mid in s.landed_count: s.v['dup_landing'] += 1
        s.landed_count[mid] = 1; s.srv_etag[i] += 1; s.srv_mid[i] = mid
        return s.srv_etag[i]
    def zombie(s, i, etag, mid):
        if s.land(i, etag, mid) is None: s.st['zombie_fenced'] += 1
        else: s.st['zombie_landed'] += 1
    def put_current(s, o, etag, mid):
        out = s.admit()
        if out == 'err':
            u = s.rng.random(); out = 'err' if u < .45 else 'lost' if u < .8 else 'zombie'
        if out == 'ok':
            yield s.lat_(PUT_MED, PUT_SIG)
            n = s.land(o.i, etag, mid)
            return ('ok', n) if n else ('412', None)
        if out == 'lost':
            if s.land(o.i, etag, mid): s.st['lost_reply_ours'] += 1
            yield TIMEOUT; return ('fail', None)
        if out == 'zombie':
            s.at(s.t + s.rng.uniform(2, 20), s.zombie, o.i, etag, mid)
            yield TIMEOUT; return ('fail', None)
        yield {'reject': 0.1, 'err': 0.15}.get(out, TIMEOUT)
        return ('fail', None)
    def get(s, o):
        out = yield from s.simple(GET_MED, GET_SIG)
        if out != 'ok': return None
        return (s.srv_etag[o.i], s.srv_mid[o.i])
    def pause(s, o, n):
        k = s.ap['pause']
        if k == 'fixed': return 0.1
        if k == 'full': return s.rng.uniform(0, min(30.0, 0.5 * 2 ** n))
        o.dec_prev = min(30.0, s.rng.uniform(0.5, o.dec_prev * 3)); return o.dec_prev
    def capture(s, o):
        to = o.produced; pm = 0
        if o.needs_recover:
            g = yield from s.get(o)
            if g is None: return 'fail'
            o.etag, o.mid = g; o.landed_to = s.mids[o.mid][1]; o.needs_recover = False
        if to <= o.landed_to: return 'landed'
        for _ in range(2):
            pm += 1
            if (yield from s.simple(PUT_MED, PUT_SIG)) != 'ok':
                s.wasted += pm; return 'fail'
        mid = s.next_mid; s.next_mid += 1; s.mids[mid] = (o.i, to, o.mid); o.own.add(mid)
        etag = o.etag; rebases = 0; o.dec_prev = 0.5
        while True:
            r, new = yield from s.put_current(o, etag, mid)
            if r == 'ok':
                o.etag, o.mid, o.landed_to = new, mid, to; return 'landed'
            g = None
            for a in range(4):
                if a > 0 and not s.take(): break
                g = yield from s.get(o)
                if g is not None: break
                if a < 3: yield s.pause(o, a + 1)
            if g is None:
                o.needs_recover = True; s.wasted += pm; s.st['landing_unknown'] += 1; return 'fail'
            ge, gm = g
            if gm == mid:
                o.etag, o.mid, o.landed_to = ge, mid, to; return 'landed'
            if gm == o.mid:
                etag = ge; rebases += 1
                if rebases > s.ap['rebase'] or not s.take():
                    o.needs_recover = True; s.wasted += pm; s.st['base_unchanged_exhaust'] += 1; return 'fail'
                yield s.pause(o, rebases); continue
            if gm in o.own:
                o.etag, o.mid, o.landed_to = ge, gm, s.mids[gm][1]; o.followup = True
                s.st['own_adopt'] += 1; return 'landed'
            s.v['foreign'] += 1; s.wasted += pm; return 'fail'
    def step(s, gen, o):
        try: d = next(gen)
        except StopIteration as e: return s.finish(o, e.value)
        s.at(s.t + d, s.step, gen, o)
    def start(s, o):
        if o.busy: s.v['two_inflight'] += 1; return
        o.busy = True; o.emit = s.t; s.step(s.capture(o), o)
    def retry(s, o): s.step(s.capture(o), o)
    def finish(s, o, res):
        if res == 'landed':
            o.busy = False; o.streak = 0; s.landed += 1; s.lat.append(s.t - o.emit)
            while o.chunks and o.chunks[0][0] <= o.landed_to: s.lags.append(s.t - o.chunks.popleft()[1])
            if o.followup: o.followup = False; s.start(o)
            return
        s.st['cap_fail'] += 1
        base = min(900.0, max(1.0, 2.0 ** o.streak)); k = s.ap['cap']
        if k == 'det': d = base
        elif k == 'full': d = max(1.0, s.rng.uniform(0, base))
        elif k == 'window': d = QUIET * s.rng.uniform(0.5, 1.5)
        else: d = s.rng.uniform(1.0, min(120.0, 2.0 * 2 ** o.streak))
        o.streak += 1; s.at(s.t + d, s.retry, o)
    def sig(s, o):
        t = s.t
        if t > ARR_END: return
        if t >= o.on_until:
            nxt = t + s.rng.expovariate(1 / 2400.0); o.on_until = nxt + s.rng.expovariate(1 / 1200.0)
            return s.at(nxt, s.sig, o)
        o.produced += s.rng.expovariate(1 / 2000.0) + 1; o.chunks.append((o.produced, t)); o.last = t
        if o.first is None: o.first = t; s.at(t + CAP, s.check, o)
        s.at(t + QUIET, s.check, o); s.at(t + s.rng.expovariate(1 / 20.0), s.sig, o)
    def check(s, o):
        if o.first is None: return
        if s.t + 1e-9 < min(o.last + QUIET, o.first + CAP): return
        o.first = None
        if o.busy: o.followup = True
        else: s.start(o)
    def sample(s):
        s.stalled.append((s.t, sum(1 for o in s.owners if o.chunks and s.t - o.chunks[0][1] > 600)))
        s.at(s.t + 10, s.sample)
    def run(s):
        for o in s.owners:
            o.on_until = 0.0; s.at(s.rng.uniform(0, 3000), s.sig, o)
        s.at(10, s.sample)
        while s.heap:
            t, _, fn, a = heapq.heappop(s.heap)
            if t > DUR: break
            s.t = t; fn(*a)
        s.t = DUR
        ends = [DUR - o.chunks[0][1] for o in s.owners if o.chunks]
        fault_end = (s.sc.get('outage') or s.sc.get('storm') or (0, None))[1]
        rec = None
        if fault_end:
            bad = [t for t, n in s.stalled if t > fault_end and n > 0]
            rec = (max(bad) - fault_end) if bad else 0.0
            if s.stalled[-1][1] > 0: rec = -1.0  # not recovered by end of run
        return dict(lat=s.lat, lags=s.lags, reqs=s.reqs, landed=s.landed, wasted=s.wasted,
                    peak=s.peak, v=s.v, st=s.st, unlanded_owners=len(ends),
                    max_age_unlanded=max(ends, default=0.0), rec=rec,
                    bytes_unlanded=sum(o.produced - o.landed_to for o in s.owners))

def q(v, p):
    v = sorted(v); return v[min(len(v) - 1, int(p * len(v)))] if v else float('nan')

def main():
    nseeds = int(sys.argv[1]) if len(sys.argv) > 1 else 12
    seeds = [9000 + i for i in range(nseeds)]
    t0 = time.time(); res = {}
    for sc in SCEN:
        for ap in APP:
            runs = [Sim(sd, sc, ap).run() for sd in seeds]
            lat = [x for r in runs for x in r['lat']]; lag = [x for r in runs for x in r['lags']]
            landed = sum(r['landed'] for r in runs)
            recs = [r['rec'] for r in runs if r['rec'] is not None]
            sumd = lambda k, f: sum(r[f][k] for r in runs)
            res[f'{sc}/{ap}'] = dict(
                runs=len(runs), landed=landed,
                commit_p50=q(lat, .5), commit_p99=q(lat, .99), lag_p99=q(lag, .99),
                lag_max=max(max(r['lags'], default=0) for r in runs),
                req_per_landed=sum(r['reqs'] for r in runs) / max(1, landed),
                wasted_per_landed=sum(r['wasted'] for r in runs) / max(1, landed),
                peak_rps=max(r['peak'] for r in runs),
                rec_median=(sorted(recs)[len(recs) // 2] if recs else None),
                rec_max=(max(recs) if recs else None), rec_not_recovered=sum(1 for x in recs if x < 0),
                unlanded_owner_runs=sum(1 for r in runs if r['unlanded_owners']),
                max_age_unlanded=max(r['max_age_unlanded'] for r in runs),
                viol={k: sumd(k, 'v') for k in runs[0]['v']},
                stats={k: sumd(k, 'st') for k in runs[0]['st']})
    meta = dict(seeds=seeds, n_owners=N, python=platform.python_version(), wall_s=round(time.time() - t0, 1),
                params=dict(PUT_MED=PUT_MED, PUT_SIG=PUT_SIG, GET_MED=GET_MED, GET_SIG=GET_SIG, TIMEOUT=TIMEOUT, CAP_NORMAL=CAP_NORMAL))
    json.dump(dict(meta=meta, results=res), open('sim-results.json', 'w'), indent=1)
    print('wall', meta['wall_s'])
main()
