# W4: OS experiments (macOS, this Mac only, Darwin 27.0.0, APFS, rustc 1.99.0 pinned in flake.nix:48)

Raw outputs: /private/tmp/claude-501/-Users-mm-dev-memory/83693829-eddb-4e4e-a499-380d68c821f5/scratchpad/review/W4/
No repo file was touched. Q2 used a `git archive HEAD rust` copy in W4/ws.

## Q1. Does std `sync_all` issue F_FULLFSYNC on macOS (file and directory)?
Verdict: YES for both. Confidence high.

Primary evidence: std source, tag 1.99.0 (matches `rustc -V`: 1.99.0 b940084d7), fetched from
https://raw.githubusercontent.com/rust-lang/rust/1.99.0/library/std/src/sys/fs/unix.rs, saved at W4/src/unix.rs, excerpt W4/q1-std-source.txt. Lines 1264-1276:
```
pub fn fsync(&self) -> io::Result<()> {
    cvt_r(|| unsafe { os_fsync(self.as_raw_fd()) })?;
    ...
    #[cfg(target_vendor = "apple")]
    unsafe fn os_fsync(fd: c_int) -> c_int { libc::fcntl(fd, libc::F_FULLFSYNC) }
    #[cfg(not(target_vendor = "apple"))]
    unsafe fn os_fsync(fd: c_int) -> c_int { libc::fsync(fd) }
```
`datasync` (1278-1284) is also F_FULLFSYNC on Apple. The call is on the raw fd, type-agnostic, so a directory handle gets the same call. (`File::sync_all` -> `inner.fsync()`; I did not re-quote fs.rs, the interposer below shows the effect.)

Apple docs: `man 2 fcntl`/`man 2 fsync` are NOT installed on this Mac ("No manual entry"). Fetched the Apple man pages mirror https://keith.github.io/xcode-man-pages/fsync.2.html and fcntl.2.html (W4/q1-fsync-2.txt, q1-fcntl-2.txt). Quotes: fsync(2): "fsync() will flush all data from the host to the drive ... the drive itself may not physically write the data ... For applications that require tighter guarantees ... Mac OS X provides the F_FULLFSYNC fcntl." fcntl(2): "F_FULLFSYNC: Does the same thing as fsync(2) then asks the drive to flush all buffered data to the permanent storage device ... implemented on HFS, MS-DOS (FAT), UDF and APFS". (Mirror, not Apple's live site; unverified against the installed OS.)

Secondary evidence: W4/q1/interpose.c (dyld `__interpose` of fcntl and fsync, DYLD_INSERT_LIBRARIES, no privileges) + W4/q1/t.rs, compiled with the pinned rustc. Command: `DYLD_INSERT_LIBRARIES=$PWD/interpose.dylib ./t $PWD/dir`. Output (W4/q1/out-interposed.txt): file `sync_all` -> `fcntl(fd=3,F_FULLFSYNC) type=reg -> 0`; `sync_data` -> same; directory `sync_all` -> `fcntl(fd=4,F_FULLFSYNC) type=dir -> 0 errno=0`. 43 F_FULLFSYNC calls for 43 sync calls, zero plain `fsync` calls. Timing (plain run): file sync_all median 1.5-3 ms (min 1.2 ms), consistent with a drive flush. Directory sync_all returned Ok every time; median was 0 us in one run (clean dir, nothing to flush) and ~3 ms in another.
Falsifier: a `fsync(` line in the interposed output, or EBADF/EINVAL on the directory. Neither occurred.
Caveat: proves the syscall is issued and returns 0, not that the media is durable (Apple notes drives may ignore it); also not tested on non-APFS or external volumes.

## Q2. Symlink swap between stat and read
Verdict: CONFIRMED, the adapter reads the symlink target. Confidence high.

Test: W4/ws/rust/crates/memory/tests/w4_symlink_swap.rs (scratch copy only). Command: `nix develop -c bash -c "cd W4/ws/rust && CARGO_TARGET_DIR=W4/target cargo test --offline -p memory --test w4_symlink_swap -- --nocapture"`. Output (W4/q2-output.txt):
```
stat: size=19 id=FileId { dev: 16777233, ino: 324737696, birth: Known(...304) }
stat after swap: size=52 id=FileId { ino: 324737698, birth: Known(...430) }
read returned 19 bytes: "MARKER-FROM-SYMLINK"
test ... ok
```
Mechanism: `stat` uses `fs::symlink_metadata` (rust/crates/memory/src/realfs.rs:100); `read` uses `fs::File::open(self.path(source)?)` (realfs.rs:126), which follows symlinks, no O_NOFOLLOW, no fstat-after-open, no identity comparison.
Downstream catch before upload: NONE found.
- capture.rs:325 is the only `fs.stat` in capture.rs/resolve.rs (grep). It is taken before any read; there is no re-stat after read. `stat_len` is not called anywhere in capture.rs or resolve.rs (grep: only trait/impls at environment.rs:43,251, resolve.rs:223, realfs.rs:114).
- The only post-read check is length: capture.rs:255-262 `read_exact` returns `Raced` only if `bytes.len() != len`; capture.rs:289-292 same for stream_span; capture.rs:468-472 (Job::write delta read) the same check on `to - from`. A symlink target that is at least as long as the observed size passes (reads are range-bounded `take(len)`), so the target's first `size` bytes go through transform/authorize/slicing/upload (capture.rs:~470 onward). If the target is shorter, outcome is `Raced`, a retry, not an upload.
- resolve.rs:48-51,119 resolves symlinks at reconcile time (symlink_metadata on path components) and realfs `walk` skips symlinks (realfs.rs:225), but that is the enumeration, not the read; a swap after enumeration is not rechecked. resolve.rs:217-233 SharedFs just forwards to RealFs.
- Next cycle `stat` would show a different inode/birth/size (symlink's own), but the bytes already read in the swap cycle are uploaded.
Falsifier would have been the read returning "REAL-SESSION-BYTES" or an error. Not fixed, per instruction. Practical severity depends on threat model (attacker with write access to the session dirs).

## Q3. Notification loss (notify 8.2.0, Cargo.lock; `recommended_watcher` + `RecursiveMode::Recursive`, same as watch.rs:203-211; FSEvents backend; macOS on this machine ONLY)
Program: W4/q3/src/main.rs; raw: W4/q3/out-q3.tsv (50 lines), summary W4/q3/summary-q3.txt. 5 runs per configuration. Each file = create tmp, append, rename to final name (3 ops), 4 threads, 100 dirs; "missing" = final path appears in no event path (settle: 3 s quiet). need_rescan() = notify's FSEvents MustScanSubDirs mapping.

| configuration | ops | missing files per run | loss % range | rescan flags per run |
|---|---|---|---|---|
| 1k files burst | 3k | 0,0,0,0,0 | 0 | 0 |
| 1k sustained 10 s | 3k | 0 x5 | 0 | 0 |
| 10k files burst (~0.8 s) | 30k | 15,0,309,0,0 | 0 to 3.09 | 1,0,10,0,0 |
| 10k sustained 10 s | 30k | 0 x5 | 0 | 0 |
| 50k files burst (~4.5 s) | 150k | 16,0,0,115,41 | 0 to 0.23 | 1,0,0,2,3 |
| 50k sustained 10 s | 150k | 0 x5 | 0 | 0 |
| deep tree, depth 150, file per level | | 0 x5 | 0 | 0 |
| dir moved in with 1000 children in 50 subdirs | | children: 1000 x5 | 100 | 0 |

Findings: (1) Real event loss occurs under burst load: 2 of 5 runs at 10k files (30k ops in 0.8 s) and 3 of 5 at 50k files (150k ops in 4.5 s) lost events; 0 of 5 at 1k. (2) Every run with missing files also had >=1 rescan flag and every run with a rescan flag had missing files; no silent loss (missing with zero flags) was observed in 50 runs, so `Input::Rescan -> ReconcileAll` (watch.rs:112, translate at 167-172) is the mechanism that covers it here. (3) A directory moved into the root yields ONE event for the top directory and NO events for any child (100% of children, all 5 runs): children are only discovered if the daemon scans the new directory on `Create(Folder)`/rename of a dir (watch.rs translate maps Create(Folder) to DirCreated; a Rename may come as Modify(Name) -> PathChanged, so verify the scheduler walks that dir) or at the 15 min reconcile. This was not exercised through the real scheduler. (4) Deep trees and sustained rates up to 15k ops/s showed no loss. Falsifier: any missing file with zero rescan flags (not seen) would break the "loss is always signalled" inference; the 5-run sample is small.
Not covered: other event-flag types beyond need_rescan; consumers slower than notify's unbounded channel; network/external volumes; Linux inotify (BC-01).

## Q4. Cold vs warm reconcile walk
Cannot purge the page cache (needs sudo `purge`), so a true cold walk was not measurable. The design target is DESIGN.md:1148 "Full safety reconcile at 30k sources: 1 s" (and :1128 test "under 1 s"); DESIGN.md:142 measured 360 ms for 24,127 files / 14,947 dirs. Experiment: docs/rust-rewrite/experiments/walk.py (scandir + lstat, metadata only, walks ~/.claude, ~/.codex, ~/.pi/agent, prints only counts and timings, no names or content).
Important caveat: load average was 17 (1-min) at measurement time (W4/q4/load.txt); other agents were running. All numbers are inflated and noisy.
- walk.py as is (24,579 files), 3 reps: 1224, 799, 697 ms (W4/q4/out-walk-py.txt). First pass in a fresh process is the slowest.
- Synthetic tree, W4/q4/synth.py: 30,000 files, 18,585 dirs (same dir/file ratio as DESIGN), 1-4 KB each. Walk (same loop as walk.py), 11 runs each:
  - process 1, first walk 6 s after create: 1317 ms; rest median 815 ms, range 749-940 (all 11: 749-1317)
  - process 2: first 812; rest median 889, range 787-1536 (outlier)
  - process 3 (later, higher load): median ~1255, range 977-1527.
The "1,479 ms cold" figure is therefore within the noise of this loaded machine's warm range (up to 1.5 s); I cannot distinguish cold from warm here. Result: at 30k files the Python walk is 0.75-1.5 s on a loaded machine vs 0.36 s measured earlier at 24k unloaded, so the 1 s target is not demonstrated for Python; the Rust walk (realfs::walk) was not timed (it has its own counter test, not run). Falsifier for "cold vs warm matters": a rerun after reboot/`purge` on an idle machine; not possible here. Recommendation: rerun on an idle machine, and with the Rust walk, before relying on either number.

## Could not run
- man pages local (absent); used mirror. - Page-cache purge (sudo). - Rust `walk` timing, scheduler-level handling of moved directories, Linux/BC-01 behavior.
