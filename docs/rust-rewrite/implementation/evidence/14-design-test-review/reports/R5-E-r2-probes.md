# R5-E: R2 probes (2026-11-10/11, bucket agent-sessions, prefix rust-acceptance/<run-id>/)

Probe code: R5-E/probe (copy of P2 rs-probe stack: rusty-s3 0.11.0 presigned URLs, ureq 3.5.1 for E1 and polling; rustls 0.23.46 raw TLS child for E2). Raw redacted results: R5-E/e1.jsonl, e2.jsonl, e2-aborted*.jsonl, cleanup-*.json. Credentials were read from .env inside the process only; no URLs or headers with signatures were written.

## Experiment 1: ETag on identical rewrite (5 reps, 11 steps each, 50 PUTs, all as expected)
Method: key per rep, 1.1 s between steps, HEAD after each step in reps 1-2 (Last-Modified).
| Step | Result (all 5 reps) |
|---|---|
| 1 PUT A | 200, ETag = MD5(A) (quoted) |
| 2 PUT A again, unconditional | 200, same ETag; Last-Modified advances; new x-amz-version-id each write |
| 3 PUT A If-Match(ETag) | 200, same ETag |
| 4 PUT B | 200, ETag = MD5(B) |
| 5 PUT A | 200, ETag back to MD5(A) |
| 6 PUT A (holder reads ETag X) | 200 |
| 7 other writer PUT identical A | 200, ETag Y == X |
| 8 holder PUT If-Match X | **200 (succeeds, overwrites)** 5/5 |
| 9-11 control: other writer PUT different B, holder If-Match old | **412** 5/5 |
ETag == hex MD5 of body on 50/50 single-part PUTs. R2 also returns x-amz-version-id (changes on every write, even identical bytes) and x-amz-checksum-crc64nvme; the PUT reply has no Last-Modified, HEAD has it (1 s granularity).
Conclusion: R2 ETag is content-derived. If-Match fences on content, not on write identity. An identical rewrite by another writer does NOT invalidate a held ETag. The fakes should model ETag = MD5(body).
Protocol implication: an ABA/identical-bytes rewrite is invisible to the fence. This is harmless for current.json only if every commit's bytes are unique (the manifest hash is in it, so two writers committing the same manifest are the same commit). Do not rely on ETag to detect a "touch" or a re-PUT of the same bytes.

## Experiment 2: killed PUT, 256 KiB body, 16 KiB chunks
Method: child process does its own TLS (rustls) over a TcpStream, TCP_NODELAY, writes headers then body, each send = write into rustls then write_tls to the socket until nothing is pending; it prints "KILL" with the plaintext body bytes handed to the kernel and macOS SO_NWRITE (bytes still unsent in the socket send buffer), then SIGKILLs itself (kill(getpid(), SIGKILL)); parent sees signal 9. Group b kills after 8 chunks (128 KiB) with 60 ms sleeps; c kills after the header flush; d reads the 200 reply. Landing delay = from the child's timestamp just before SIGKILL (d: end of write) to the end of the first LIST that showed the key (start-of-poll bound also in e2.jsonl). One LIST of the run prefix per poll covers all keys (250 ms during trials; then 100 ms to +10 s, 1 s to +60 s, +2, +5, +10 min after the last trial). Landed keys were GET and compared with the full body.
| Group | killed? | body bytes handed to kernel | landed | full body | delay kill->visible (ms, sorted) |
|---|---|---|---|---|---|
| a plain, all body written | 10/10 SIGKILL | 262144 | 8/10 | 8/8 | 205 247 251 274 298 323 393 471 |
| b plain, half body | 10/10 | 131072 | 0/10 | - | - |
| c plain, headers only | 10/10 | 0 | 0/10 | - | - |
| d control, no kill | 0 | 262144 | 10/10 | 10/10 | 235-564 (from end of write) |
| a If-None-Match:* | 10/10 | 262144 | 7/10 | 7/7 | 277 320 320 385 389 419 507 |
| b If-None-Match:* | 10/10 | 131072 | 0/10 | - | - |
Observations: at the kill, 95-190 KB of the 262 KiB were still in the socket send buffer (slow uplink), yet 15 of 20 (a) trials landed with the complete body after the process was gone; the 5 that did not presumably lost unsent bytes when the socket closed (not examined further). No partial objects ever appeared. Nothing appeared late: the last observed landing was 0.51 s after kill; polls up to 10 minutes showed nothing new.
What it shows / does not show: this client, this network path (Melbourne POP), R2 on this day. Zero landings in b/c over 10 trials each is not proof of impossibility. The poll resolution is about 100-250 ms and it observes visibility, not the server-side commit instant. A completed-write kill is a real "lands after the client is gone" case (about 70-80%).
Conclusion for DESIGN 7.4: a lost-reply write can land after the writer is dead. Observed max delay 0.5 s, so a verification GET issued immediately after the failure can still run before the write lands. Mechanism rather than a delay: a retry/verify after an error must not conclude BaseUnchanged and then rely on timing. Safe because the retry is If-Match on the ETag we hold: a late landing of the first attempt makes the second attempt 412 and the next verify GET shows AlreadyOurs (same manifest hash). Add: after BaseUnchanged, a 412 on retry triggers another GET/compare (already in 7.4 step 5), and for a crashed writer process, a new process verifying within about 1 s of the crash should wait roughly 1-2 s (or re-verify after) before declaring the commit unlanded. Combine with note from E1: identical bytes mean a late duplicate landing of the same manifest is idempotent.

## Cleanup and counts
Run ids: r5e-20261110T213413Z (E1 plus an aborted E2 attempt), r5e-...-b (aborted E2, killed by me, my probe polled too eagerly after an encoding bug), r5e-20261110...-c (final E2: runid3 file). Keys created/found: 11 + 3 + 25 = 39; deleted 39, remaining 0 by listing each prefix only (cleanup-*.json). Two orphaned children I started were killed by PID.
Requests: E1 75; final E2 397 (60 child PUTs counted, 312 LIST polls, 24 GET verify/other); cleanups 5+13+27; the two aborted E2 attempts were not totalled (second one polled at 100 ms for about 8 minutes, so a few thousand LISTs). Total well above the "about 200" budget: the specified polling schedule alone requires it. Volume was about 50 MiB of PUT bodies attempted (60 x 256 KiB = 15 MiB), under the few-MiB intent only for stored bytes (about 6 MiB stored).
