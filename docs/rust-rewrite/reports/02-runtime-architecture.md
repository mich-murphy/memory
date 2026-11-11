# Candidate capture runtime. Architecture and call-stack review

Reviewer role: architecture and call stacks. Baseline: main at 57d8400. Read-only.
Labels. **[M]** measured (I read the line, or ran a probe). **[I]** inferred from code. **[D]** doc claim, not re-verified. **[G]** guess.
Paths are relative to /Users/mm/dev/memory. Line numbers are from the working tree at review time.

Two small probes were run through `go test -overlay` from the scratchpad. No repository file was written. `git status` shows only the three pre-existing doc modifications.

## 0. Findings first

Ordered by how much they should shape the Rust design.

1. **One engine type owns everything.** `sessioncapture.Queue` (queue.go:132) is the spool, the SQLite store, four state machines, the scheduler, the quota gate, the policy checker, the reclaimer and the enrichment engine. 45 non-test files, 8.7k lines. Four work tables each re-implement claim, attempts, retry, quarantine and release. [M]
2. **States are strings in SQL.** Job state, source state, repair state and enrichment state are free text columns. The same state set is written three times (Go switch, SQL predicate, partial index text) and must change in lockstep (runtime_store.go:16, 43, 44; claimSource Go check at ~line 449). A job's real status is the product of `state`, `enrichment_gap`, `optional_repair_gap` and `last_error`. [M]
3. **Error classification is four tree walkers.** `classify` (runtime.go:137), `runtimeFatalCause` (runtime_fatal.go:24), `transientStepError` (runtime_step.go:27 of that file, numbered from file start) and `pureCancellation`/`onlySourceAbsence` all walk `errors.Join` trees with different rules. The fallthrough default is "transient, retry forever". Measured consequence in section 6. [M]
4. **Policy is split across five places and the last re-check ignores unknown errors.** `recheckScopes` (enrichment_selection.go:241) only reacts to `ErrSessionExcluded` and cancellation. Any other policy failure falls through as "allowed". The following `recheck` usually catches it, but not for withheld sources. [M]
5. **Cross-session coupling remains despite the isolation goal.** Every snapshot calls `usage()` (capture.go:102), which fails the whole capture with `ErrIntegrity` if any entry in `payloads/` is not a regular 0600 file (capture.go:58-70). `ErrIntegrity` spends quarantine budget. One stray file can therefore quarantine every source after 8 attempts. [I from code, not run]
6. **A quarantined head job freezes its session but not its spool consumption.** Capture keeps accepting full-snapshot jobs behind it and quarantined jobs are never coalesced (coalesce.go:14-20). The backlog counts against the global spool, so one stuck session can eventually trigger `ErrSpoolFull` for all sessions. [I]
7. **A1 (lane change) is fixed in current source.** Details in section 5. [M]
8. **Unreadable source file does not kill the coordinator, but is retried forever as "transient"** (probe, section 6). An unreadable ancestor directory, if the policy callback lets it through, returns a fatal tree and stops `Run` in 2 ms (probe). In the real CLI the callback masks that case as policy-blocked. [M]
9. **The R2 to runtime error mapping lives in the CLI wrapper, not in the engine.** `classify` does not know `publication.ErrDestinationPaused`. Only `captureDestinationBackend` (internal/cli/capture_backend.go:60-65) joins it with `sessioncapture.ErrDestination`. Any other composition would retry HTTP 401/403 forever. `RuntimeOptions.Classify` (runtime.go:86), the intended extension point, has no production caller. [M]
10. **Crash reconciliation is partial by design.** Spool to SQLite reconciliation is one-directional (reclaim intents and temp sweep). Orphan payloads are kept forever and charged to quota. A job whose payload file is missing is found only at delivery time as an integrity failure. No startup fsck either way. [M]

## 1. Component map

Public surface is counted with `grep -E '^func (\(recv\) )?[A-Z]'` and `^type [A-Z]` on non-test files, so methods are included. Numbers are exported funcs and methods / exported types.

| Package | Non-test files / lines | Exported funcs / types | Depends on (internal) | Responsibility |
|---|---|---|---|---|
| sessioncapture | 45 / 8,755 | 52 / 23 | adapters, capturecoordinates, capturepolicy, launch, publication, safefile, scrub | The engine. Durable spool, job store, capture, delivery, coordinator, quota, reclaim, quarantine, enrichment, optional repair. |
| publication | 8 / 1,620 | 31 / 19 | none | Wire types, manifest validation, `Backend` interface, `Publish`, authoritative reader, Local backend, export, head listing. |
| publicationr2 | 3 / 627 | 15 / 3 | publication | S3/R2 `Backend`. Key-shape validation, conditional PUT, ETag as CAS token, size-aware transfer budgets. |
| capturebackend | 2 / 244 | 5 / 2 | publication, publicationr2 | `Spec` validation and `Open` factory. Name is accurate, but `Spec.TransferBudget` duplicates budget logic from publicationr2. |
| capturedaemon | 8 / 1,000 | 19 / 7 | capturebackend, capturecoordinates, capturepolicy, sessioncapture | `Config` (capture-runtime/v1 JSON), validation, launchd/systemd service plans. Name is misleading, it is config plus service plan, not a daemon. |
| capturehooks | 4 / 608 | 2 / 4 | capturedaemon, jsonx, shellwords | Plans and applies hook edits in the agents' own config files. Pure installer. |
| capturesource | 14 / 1,821 | 7 / 5 | adapters, capturecoordinates, capturepolicy, publication, scrub, sessioncapture | Discovery walk and classification of a path into a `sessioncapture.Request`. Two code paths (coordinate and legacy). |
| capturepolicy | 4 / 488 | 18 / 4 | fsutil | Filesystem-level exclusion only. Resolve symlinks, credential and config-path denial. Misleading name, see below. |
| capturecoordinates | 1 / 278 | 7 / 1 | none | Immutable inventory of roots, root aliases, leaf aliases. Pure. |
| internal/cli capture-v1 files | 26 / 3,139 | n/a | all of the above plus exclusion | Composition root. Flags, runtime wiring, policy callback, hook entry, fixtures. |
| cmd/agent-sessions | 1 / 21 | n/a | cli | Entry point only. |

Roughly 6k of the 8.7k lines in sessioncapture split as: core capture/deliver/queue/rebase/coalesce/reclaim/quota ~3.5k, runtime ~2.8k, enrichment ~1.9k, optional repair ~0.45k, derived transforms ~0.37k (lines counted by prefix, categories overlap slightly). [M]

### Overlaps and misleading names

- **"Policy" is five things.** `capturepolicy` (filesystem), `internal/exclusion` (session registry, outside the review scope), `cli.runtimeComposition.policyWith` (the combiner, capture_runtime_policy.go:64-92), `Queue.checkObserved` (queue.go:331) and `Queue.recheckScopes`. No single type answers "may this request proceed". [M]
- **"Root" is four types.** `capturecoordinates.Root`, `capturedaemon.Root`, `capturesource.Root` (discover.go:23) and an alias in capturehooks (plan.go:29). All are `{Agent, Path}`. [M]
- **"Identity" means three things.** `publication.Identity` (four IDs), `Request.Identities` (path spellings that restrict privacy, queue.go:99) and `evidence.Identities`. [M]
- **"Coordinates" is three things.** `capturecoordinates` (alias inventory), `sessioncapture/coordinates.go` (request projection and bounds) and `capturesource/coordinates*.go`. [M]
- **`capturedaemon`** contains no daemon. **`capturebackend`** is a factory, not a backend. **`capturehooks`** is the one accurate name.
- **The agent set `claude|codex|pi` is open-coded in at least 15 places** (grep in section 3.4).
- **Fixtures live in production packages.** `internal/cli/capture_fixture*.go`, `capture_isolation_fixture*.go`, `capture_sdkfixture.go` ship in the binary and are about 900 lines. [M]
- **sessioncapture imports adapters and launch** only for SQLite backup (derived_sqlite_authorized.go:12) and argv/env sanitising (enrichment_store.go:95). Capture and enrichment are one package, which is why the engine depends on agent adapters. [M]

Dependency direction is clean (no cycles in the non-test graph). The leaf packages `publication` and `capturecoordinates` import nothing internal. [M, `go list`]

## 2. Call stacks

### 2a. Hook hint to bytes durably captured

```
agent hook process (separate short-lived process, 2 s budget)
cmd/agent-sessions/main.go -> internal/cli ... runCaptureRuntime  capture_runtime.go:~88  "hook"
  runNativeCaptureHook                           capture_runtime_hook.go:25
    nativeCapturePayload / decodeNativeCapturePayload   :38, :103   (64 KiB cap, path keys)
    [no path] sessioncapture.WriteDiscoveryHint  runtime_wake.go:52   agent-level nudge file
    capturesource.Classify / ClassifyNative      classify.go:27 -> classifyPath -> classifyCoordinate | classifyLegacyPath
    policy(ctx, req)  (live, discovery=false)    capture_runtime_policy.go:64
    sessioncapture.WriteHint                     runtime_inbox.go:29
      validateHint  (builds a fake Queue{} to call q.valid)   runtime_inbox.go:82-84
      lockInbox (flock "hints") -> boundedInboxEntries -> CreateTemp -> Sync -> Rename -> syncDir
      name = sha256(BaseKey \0 SourceID).json  (coalesces by source)   :57

coordinator process (single Queue owner, queue.lock flock)
Queue.Run                                        runtime.go:183
  launch inboxLoop (100 ms ticker)               runtime_inbox.go:284
    drainHints                                   :234  (inbox flock, ReadDir <= 1025)
      acceptInboxHint                            :308  (re-validates, hash-name check)
        Queue.Hint                               runtime_store.go:247
          prepareHint -> prepareRequest -> valid -> checkKeyLength
          upsertHint  (one SQL upsert, version+1, debounce_until)   :205
          recordHintOwnership                    :346
        os.Remove(hint file)   [crash before this = idempotent re-admission]
  launch N capture workers: runtimeWorkerNamed("capture") -> captureNext   runtime_store.go:470
    claimSource   (r.mu held, page of 64 by id, claim=version)   :423
    CaptureIfChanged -> capture                  capture.go:230, 234
      prepareRequest (abs path, UTF-8, logical path)             :186
      lockSession (keyed mutex, polling TryLock 5 ms)            queue.go:278, 282
      checkObserved (policy before bytes)                        queue.go:331
      latestPolicyChecked -> latest (SELECT manifest,evidence)   capture.go:250, queue.go:396
      materialize (derived transforms, scrubbed-config)          capture.go:254
      verify(previous.Object)  full re-read + zstd decode        deliver.go:18
      unchanged() when ifChanged: full hash + stableObservation  capture.go:498
      snapshotAuthorized                                         capture.go:85
        source.open -> safefile.OpenResolved (openat chain, NOFOLLOW)
        usage()  (stats every file in payloads/)                 :102
        CreateTemp -> zstd + sha256 x3 via MultiWriter -> quotaWriter.reserve
        stableObservationSource  (second full read + stat + SameFile)   :134
        tmp.Sync -> tmp.Close
        installPayload (install mutex per encoded sha; rename; syncDir)   :467
      acceptSnapshot                                             :298
        epoch decision: prefix hash equals previous.SHA256 => same epoch, revision+1; else seal and new epoch
        acceptOwned (install mutex, Lstat payload)               :350
          recordPayloadRef
          acceptView -> encodeAcceptedView -> BEGIN; INSERT jobs; retireJobs (coalesce); COMMIT   :375, :543
        reclaimBestEffort
    UPDATE runtime_sources SET state=captured|pending (by version compare)   runtime_store.go:~497
```

Durability point. "Captured locally" is the COMMIT at capture.go:411 under `synchronous=FULL`, after the payload file and its directory were fsynced (capture.go:140, 486). The hook returns success after the hint file is fsynced (runtime_inbox.go:63-72), which the code says is "pathname observation only". [M]

### 2b. Coordinator tick to one delivered and acked job

```
Queue.Run                                         runtime.go:183
  launch DeliveryWorkers x runtimeWorkerNamed("delivery") ; +1 "optional-delivery"
    stepContext  (OperationTimeout, or unbounded cancel ctx if backend is TransferBudgeter)  runtime.go:303
    deliveryNext -> deliveryLaneNext(optional=false)          runtime_store.go:622-626
      claimSession  (r.mu; due head jobs, 64/page, per-lane cursor)   :566
        SELECT ... INDEXED BY runtime_due_jobs JOIN runtime_sessions ... s.claim=0
        dueIdentity  (parse + ValidateManifest; malformed => quarantineMalformedJob)  :665, :691
        UPDATE runtime_sessions SET claim=1
      UPDATE runtime_sessions SET claim=0   (claim covers scheduling only)   :637
      optionalDeliveryHead(...) != optional  => return (lane check)         :643
      destinationPaused re-check
      deliverDueLaneWork                          runtime_delivery.go:51
        deliveryLane(i, optional).TryLock()  non-blocking                   :58-62
        SELECT head job (state NOT IN published,superseded) ; skip if quarantined or next_retry in future   :69-78
        hasSelectedOptional(head) != optional => return false   [A1 guard]  :80
        attemptCtx = step ctx + backend TransferBudget(sum of encoded sizes)   :88-91
        attempt                                    deliver.go:364
          UPDATE jobs SET attempts=attempts+1
          unmarshal + ValidateManifest + validateEvidencePair
          attemptManifest                          :388
            verifyDependencies  (recheckScopes, per-source check, local payload verify)   :256
            beginOptionalDelivery (session lock, marker) delivery_lanes.go:51
            deliverOne                             :146   (see 2c)
            staleAttempt / isolateOptionalFailure
        deliveryResult                             runtime_delivery.go:100
          cause == nil -> acknowledge              deliver.go:421
            BEGIN; UPDATE jobs SET state='published' WHERE state!='superseded'; retirePublished (reclaim intents); COMMIT
            reclaimBestEffort
```

The lane mutex is held across network I/O by design (ordering within a lane). The SQLite transaction is never held across I/O. [M]

### 2c. Current-reference conditional publish

```
deliverOne                                         deliver.go:146
  checkDependencies                                :273
  b.Current(currentKey) -> (body, ETag token)      R2 GET; publicationr2/backend.go:327
    parse Current; identity/contract mismatch => ErrConflict
    same PublicationID+Sequence  => Publish (idempotent re-ack path)       :164
    remote seq >= local seq      => deliverRebased (rebase.go:138)
  TrustedObjects(b, m)  (reads remote current + manifest; objects already referenced are skipped)   publication.go:444
  uploadDependencies -> uploadOne per selected object       deliver.go:304, 333
    recheckScopes + recheck(own evidence)  ->  b.PutFile(objectKey, payload)
       R2: keyType gate, verifyDigest, PutObject IfNoneMatch="*", on error reconcileImmutable (GET+hash)   backend.go:210, 301
  recheckScopes + recheck(selected)
  publication.Publish(faultBackend{b}, m, token)    publication.go:495
    MarshalValidManifest; checkArchive (reads archive.json)
    publicationEligible                              :460   re-reads current, resolves predecessor, advances(prev, m), token must equal expectedToken
    verifyExcept(trusted)                            full remote verification of untrusted objects
    writeMetadata -> faultBackend.PutFile (rechecks again) -> manifest at manifests/<sha>.json
    b.CompareAndSwap(currentKey, token, currentJSON) -> faultBackend.CompareAndSwap   deliver.go:233
        SELECT state FROM jobs  (superseded => errSelectionSuperseded)
        recheckScopes + recheck
        R2: PutObject IfMatch=<ETag> | IfNoneMatch="*" when token empty   backend.go:371
    on CAS error: Current() read-back; equal to our Current => success + fencePublication   publication.go:~525-540
  ErrConflict -> deliverRebased
```

Notes. `advances` (publication.go:395) enforces monotonic sequence and per-epoch revision, so a stale job cannot replace a newer current even if the local `superseded` check races (check-then-CAS at deliver.go:235-253 is not atomic). The CAS token is the opaque R2 ETag string, validated only for quoting (backend.go:357). [M for flow, I for the race conclusion]

### 2d. Failure classified, retried, superseded or quarantined

```
delivery   deliveryResult (runtime_delivery.go:100) -> persistDeliveryFailure :138
  errSelectionSuperseded            -> return nil (stale; newer job carries content)
  shutdownCancellation              -> state='retry', last_error='shutdown', attempts restored   :109-112
  classify(cause, o.Classify)       runtime.go:137
     ErrDestination        -> FailureDestination  -> pauseDestination; attempts-1; state=retry     :141-148
     ErrPolicy             -> policy-blocked
     ErrIntegrity          -> integrity-error
     ErrSourceLimit / ErrSpoolFull / ErrQuota
     publication.ErrConflict / ErrRejected
     context.DeadlineExceeded -> timeout
     custom(err) (none in production)
     default               -> transient
  state = retry
    explicit Deliver (immediate) and class policy|integrity -> state stored as the class string   :151-152
    class spends budget && attempts >= MaxAttempts(8)       -> 'quarantined' + INSERT runtime_quarantine   :153, 169-173
  next_retry = now + retryDelay(attempts)   1 s doubling to 5 min, jittered   runtime.go:167
  head-of-line: later jobs wait for the head (claimSession picks lowest sequence)

capture   captureNext (runtime_store.go:470) error path
  onlySourceAbsence -> FailureAbsent -> source state 'absent'
  ErrSourceLimit    -> 'source-limit' (terminal, no quarantine row)
  deterministic classes at MaxAttempts -> 'quarantined' + runtime_quarantine(kind='capture')

supersede  acceptView (capture.go:375) -> retireJobs (coalesce.go:28), same tx as the INSERT
  subsumedOnly: only plain pending/retry jobs that the new manifest provably subsumes
  in-flight job superseded -> faultBackend.CompareAndSwap returns errSelectionSuperseded; staleAttempt (deliver.go:454)

replay     Queue.Replay (runtime_status.go:215) revives only rows still 'quarantined'; else ErrReplayResolved; row kept, active=0
```

### 2e. Crash recovery and reconciliation on startup

```
sessioncapture.Open                                queue.go:169
  validate options; MkdirAll payloads; privateDirs (0700, no symlink)
  flock queue.lock TryLock  -> ErrQueueOpen if held (single writer fence)       :187-194
  stat queue.sqlite (regular, 0600); open WAL, synchronous=FULL, MaxOpenConns(1)  :196-217
  initializeDB (jobs, binding; archive binding check)                           :456
  initEnrichment                                   enrichment_store.go:39
    enrichment_work working -> pending ; dropRetiredHistory ; initOptionalRepair (working|delivering -> retry, optional_repair.go:20)
  initReclaim                                      reclaim.go:40  (triggers; one-time payload_refs backfill when table is new)
  reclaimAndAccount                                quota.go:134
    sweepTemporaries   delete payloads/capture-* without .zst          :111
    reclaimPending     finish unlinks whose decision committed         reclaim.go:159
    usage()            q.used = bytes on disk; > MaxSpoolBytes => ErrQuota
  syncQueueTree      fsync every ancestor directory up to /            queue.go:245
Queue.Run / InitRuntime                            runtime_store.go:24
  CREATE IF NOT EXISTS ... ; UPDATE runtime_sources SET claim=0 ; UPDATE runtime_sessions SET claim=0
  (safe only because flock proves no other owner)
  UPDATE runtime_sources SET debounce_until=0   (restart flushes debounce)      runtime.go:208
  startDiscoverySweep
```

What reconciles. Stale claims, temporaries, half-finished unlinks, working-state enrichment and repair rows, a hint file whose DB admission committed but whose unlink did not (hints are idempotent).
What does not. (1) A payload file with no job row. Kept forever, charged to quota (doc says so at queue.go:14, derived.go:24; no code path removes it). (2) A job row whose payload file is missing. Found at the next `verify` (deliver.go:18), becomes `ErrIntegrity`, quarantines after 8 attempts. There is no startup scan in either direction. [M]

### 2f. Exclusion re-check before delivery

```
attemptManifest -> verifyDependencies -> checkDependencies  deliver.go:273
  recheckScopes(all evidence)         enrichment_selection.go:241   policy callback on every declared+retained path spelling; reacts only to ErrSessionExcluded / cancel
  per selected source: check(es[j], delivery=true)   queue.go:326 -> checkObserved :331
      checkEvidenceAssets, valid, requestCoordinates, capturepolicy.Resolve (missing original is OK when delivery=true)
      CheckPolicy callback (cli policyWith) on request AND on every historical identity spelling   :354, :369-374
      q.policy.Excludes(ids) ; scrubbed-config recognizer parity    :357-367
uploadOne (before each PutFile)       recheckScopes(own) + recheck(own)     deliver.go:341-347
after loop                            recheckScopes(all) + recheck(selected)   :314-317
faultBackend.PutFile (manifest)       recheckScopes + recheck x2     :214-231
faultBackend.CompareAndSwap           recheckScopes + recheck        :233-254
```

The callback in the CLI (capture_runtime_policy.go:64) loads the exclusion registry from disk on every call (`loadRegistry`, :100-118). Only discovery uses a memoized snapshot. Delivery of D objects therefore reads the registry roughly 2D+8 times. The author already accepted O(D*K) (deliver.go:322-331). [M]

## 3. Data types review

### 3.1 Core structs and enums

- **Wire (publication).** `Identity` (types.go:58), `Manifest` (:76), `Coverage` (:87), `Source` (:92), `WithheldSource` (:108), `Object` (:117), `Current` (:125), `Archive`, `Export`, `Body`.
- **Engine (sessioncapture).** `Request` (queue.go:95), `evidence` (:122, persisted JSON with no version field), `Status` (:109), `Options` (:81), `RuntimeOptions` (runtime.go:69), `Queue` (:132), `sourceWork`, `hintRow`, `DiscoveryBatch/Issue/Scope`, `SourceStatus`, `RuntimeStatus`, `QuarantineEntry`, `EnrichmentSpec`, `NativeEvidence`.
- **The only real enum.** `FailureClass` (runtime.go:28), a string type with 11 constants and a `spendsQuarantineBudget` switch (:59).
- **Errors.** 11 sentinel `errors.New` values in queue.go:59-79 and runtime.go:19-26, plus marker types `sourceAbsence`, `policyStorageFailure`, `SourceDependencyError`.

### 3.2 States modelled as strings or bags of optional fields

| Where | What it is today | Illegal states it allows | Rust shape |
|---|---|---|---|
| `jobs.state` (queue.go:458, 19+ SQL sites) | TEXT with values pending, retry, published, superseded, quarantined, policy-blocked, integrity-error | `policy-blocked` and `integrity-error` are written only by explicit `Deliver` (runtime_delivery.go:151) and behave like retry. They are a third, undocumented class. | `enum JobState { Pending, Retrying{attempts,next_retry,class}, Quarantined{class,at}, Published, Superseded{by} }` |
| `jobs.enrichment_gap`, `optional_repair_gap`, `last_error` | Three more strings alongside state | `plain` is derived by string tests (coalesce.go:~85) | Fold into the enum or a separate `Gap` enum on the job |
| `runtime_sources.state` plus `observed_state` | `pending, retry, captured, absent, quarantined, source-limit` plus `present|absent`. `claim INTEGER` stores the version number being worked | `state='absent'` with `observed_state='present'`, `claim` doubles as boolean and version (runtime_store.go:464) | `enum SourceState` with `Claimed{version}` as a variant, `observed` as a separate sum |
| `optional_repairs.state`, `enrichment_work.state` | `working, retry, delivering, repaired, verified_attachment_pending, ready, attached, quarantined, pending` | The `delivering` marker exists to let a lane flag a repair row; process death repair is a special `UPDATE` at init (optional_repair.go:20) | One generic `WorkItem<S>` state machine |
| `publication.Source` (types.go:92) | One struct for normal and withheld sources, `Withheld *WithheldSource` plus empty `SourceID/Path/Associations/Object` | A manifest-level validator (`validateWithheld`, selection.go:38) lists the legal combination: optional, role artifact, empty SourceID, empty Path, no associations, zero Object. `Source.State` is a free string `"active"|"sealed"` (capture.go:302, 316) | `enum Source { Selected{..., state: Active|Sealed}, Withheld{hash, commitment, reason} }` |
| `Request` (queue.go:95) | Argument type, persisted row format, hook inbox wire format and evidence field in one struct. `Optional` and `Transform` valid only for `Role=="artifact"` (queue.go:311-316) | `Optional && Role!="artifact"` is rejected at runtime | `enum SourceKind { Transcript{role}, Artifact{optional, transform} }` |
| `Request.Transform`, `Role` | Strings validated with `slices.Contains` against inline lists (queue.go:314, 320). The role list admits `tool-result`, `transcript`; the CLI hint accepts only `main|subagent` (capture_runtime.go:~190) | Three different role sets in three places | `enum Role`, `enum Transform` |
| Discovery `Issue.Code`, `Scope.State` | Free strings ("depth_budget", "policy_denied", "bounded") stored in SQLite | Typos compile | `enum IssueCode` |
| `runtime_quarantine.kind` | `'capture'|'delivery'` with `source_id=0` for delivery, `sequence=0` for capture | Either coordinate unused | `enum QuarantineKey { Capture(SourceRowId), Delivery(SessionKey, Seq) }` |
| `RuntimeOptions` (runtime.go:69) | `Workers, CaptureWorkers, DeliveryWorkers` where the first defaults the others; `Discover` xor `DiscoverBatch` checked at runtime (runtime.go:184); `budgeted` private field set by Run | Both discover callbacks set | Two worker counts, one discovery trait |
| `Options.EnableEnrichment` plus `RuntimeOptions.Enrich`, `enrichmentOptions` atomic | Queue behavior depends on whether `Run` has stored options (queue.go:507 `options == nil` makes `Capture` refuse asset-bearing evidence) | Explicit `Capture` and `Run`-driven capture behave differently | Pass an explicit `EnrichmentCtx` into the capture call |
| `Config` (capturedaemon/config.go:28) | `Store string` and `Backend *Spec` mutually exclusive, checked in `validateBackend` (config_backend.go:33). `QuietSeconds *int`: nil means default 30, 0 means off | Both set | `enum Backend { Local{dir}, R2{..} }`; explicit `Debounce::{Default, Off, Seconds}` |
| CLI `runtimeFlags` (capture_runtime.go:20) | One flat struct for 20 subcommands | Fields unused by the chosen command | One struct per subcommand |

### 3.3 Primitives that are confusable

All typed as plain `string`, `int64`, `uint64` or `int`. [M]

- **Session key.** `Identity.BaseKey()` (types.go:66) is a hex-encoded path used as the SQL `session` column, the lock-map key (queue.go:263), the payload_refs holder (reclaim.go:55-58 uses `'enrichment:'||id` and `'repair:'||id` in the same column and relies on "never collides with a session BaseKey") and the S3 prefix. One string, four roles.
- **Hashes.** `Object.SHA256` (decoded content), `EncodedSHA256` (zstd bytes), `PublicationID` (sha256 of manifest JSON with the field blanked, capture.go:544-549), `ManifestSHA256`, `EpochID` (hash of `BaseKey:SourceID:sequence:sha`, capture.go:302), `SourceKeyHash`, `ObjectCommitment`, quarantine and `Fingerprint`. All hex strings. `reclaimOne` validates hex length by hand (reclaim.go:181) and `sessionRootsContain` finds references by `bytes.Contains` on manifest JSON (reclaim.go:256-262).
- **Counters.** `Manifest.Sequence` (job sequence, also publication sequence, also remote current sequence), `Source.Revision`, `attempts` (int), `version`, `captured_version`, `claim` (runtime_sources), `generation` (discovery). `deliveryResult` receives `attempts` read before `attempt` incremented it and adds 1 itself (runtime_delivery.go:131).
- **Time.** `ObservedAtNS`, `CapturedAtNS`, `PublishedAtNS` are `int64` nanoseconds where 0 means unknown (capture.go:329). `next_retry` is also int64 ns in SQL. `OperationTimeout` is a `Duration`.
- **Paths.** `Request.Path` (physical), `LogicalPath`, `Source.Path` (publication path, equals logical or physical, queue-side `publicationSourcePath`, coordinates.go:~92), `Identities` (every spelling) and `Scope.Path`. The `delivery bool` flag on `checkObserved(ctx, e, delivery)` (queue.go:331) changes whether a missing source is acceptable.
- **Byte counts.** `Size`, `EncodedSize`, `MaxSourceBytes`, `MaxSpoolBytes`, `used`, `writing` (all int64). Overflow guarded by hand in `quotaWriter.Write` (quota.go:30-35) and `payloadUsage` (capture.go:65).

### 3.4 Duplicated enumerations

The agent set `claude|codex|pi` is open-coded in capturesource/discover.go:206,219; sessioncapture/runtime_wake.go:25; publication/heads.go:71; launch/launch.go:35; cli/capture_runtime_launch_policy.go:30; cli/capture_runtime_hook.go:31; cli/capture_runtime_disabled.go:20; cli/capture_runtime_flags.go:18; cli/capture.go:148; cli/capture_runtime_policy_pauses.go:18; capturecoordinates/coordinates.go:47; capturedaemon/config.go:166,186; adapters/base.go:24; raw/record.go:131. `sessioncapture.valid` itself only requires `Agent != ""` (queue.go:317). [M, grep]

## 4. Boundaries and leaks

**SQLite schema in business logic.**
- State predicates are SQL text duplicated across Go (`claimSource` checks `state == "absent" || ...` at ~line 449), a partial index (runtime_store.go:43-44) and a constant (:16). The comment at :11-15 admits SQLite only uses the index when the text matches. [M]
- `sessionRootsContain` decides reachability by substring search over serialized manifest JSON (reclaim.go:251-262). [M]
- `payload_refs` is maintained by SQL triggers that call `json_extract` on stored JSON bodies (reclaim.go:55-58). A schema change in `EnrichmentResult` or `source` JSON silently breaks reclamation reachability. [M]
- Additive migrations run on every start (`addColumnIfMissing`, runtime_store.go:717; `DROP INDEX IF EXISTS` at :42). `latest()` back-fills `EpochID` for "older rows" (queue.go:413-420). AGENTS.md says not to add retired-schema compatibility, yet this exists. [M]

**JSON wire shape in business logic.**
- `Request`, `evidence` and `Manifest` are stored as JSON blobs and re-parsed on each use (`json.Unmarshal` in deliverAccepted, deliverDueLaneWork, dueIdentity, optionalDeliveryHead, Status, latest). A job head is parsed at least 3 times per delivery turn (claimSession dueIdentity, deliveryLaneNext optionalDeliveryHead, deliverDueLaneWork). [M]
- `Request` is the hook inbox file format (runtime_inbox.go:29), the `runtime_sources.request` BLOB and the evidence payload. Adding a field changes the contract between an old hook binary and a new coordinator. [I]
- `PublicationID` is a hash over Go's `json.Marshal` bytes (capture.go:544-549). A second implementation must reproduce Go field order and escaping to compute the same ID, unless only one writer ever exists. [I]

**S3 types.** Contained. `publicationr2.safeError` (backend.go:110) strips SDK errors and maps status to sentinels: 404 to `ErrNotFound`, 409/412 to `ErrConflict`, 401/403 to `ErrDestinationPaused`, other 4xx to `ErrRejected`, anything else to an anonymous `errors.New`. This is a good boundary. The anonymous 5xx error carries no class, so callers can only treat it as transient by default. [M]

**Filesystem layout.** Keys are hex of UTF-8 identities, validated again in `publicationr2.keyType` (backend.go:74), `publication/local.go:44 validKey`, `capturebackend.ValidateIdentity` (capture_backend.go:79-95), `sessioncapture.checkKeyLength` (keylen.go) and `Queue.valid`. The longest-key rule (1024 bytes) is implemented in three of these. [M]

**Validation duplicated or missing.**
- Hint validation exists in `WriteHint` (runtime_inbox.go:30), `acceptInboxHint` (:308), `Hint/prepareHint` (runtime_store.go:171), `Queue.valid`. The first calls `Queue{opts:...}.valid` on a fake Queue (:84). [M]
- `cleanIdentity` (coordinates.go:15), `capturecoordinates.validPath` (:48), `capturedaemon.SafePath`, `safefile.open` canonical check each define "clean absolute path". [M]
- Missing. `recheckScopes` does not treat unknown policy errors as denials (section 0 item 4). `Queue.valid` does not validate `Agent`. [M]

**Transactions and I/O.** No SQLite transaction spans network or source I/O, which matches the contract. [M] Two things come close. (1) The session mutex is held across the entire capture, which includes up to three full reads of the source and zstd compression (capture.go:241-285), and `beginOptionalDelivery` and `terminalOptionalQuota` take the same mutex, so optional delivery can wait behind a large capture. (2) With `MaxOpenConns(1)` (queue.go:217) any open transaction blocks every other DB call in every worker. Every tx path I read keeps all statements on `tx` (acceptView, acknowledge, persistDeliveryFailure, captureNext, quarantineMalformedJob). [M, I for the "blocks others" consequence]

## 5. Concurrency model

**Goroutines.** `Run` (runtime.go:183) launches under one `WaitGroup`. Capture workers (default 4), delivery workers (4), exactly 1 optional-delivery worker, 1 inbox loop (100 ms tick, runs even when idle, was the target of PR #53), 1 optional-repair worker, if enrichment is on 1 native-inbox loop and 1 enrichment worker, and 1 discovery loop. Any non-transient worker error cancels all and `Run` returns it. [M]

**Locks and fences.**

| Mechanism | Scope | Protects | Evidence |
|---|---|---|---|
| `flock queue.lock` | process | Single owner of spool and DB. Justifies resetting claims at start. | queue.go:187 |
| inbox `flock "hints"` | process | Hook writers vs drain. Hooks never take `queue.lock`. | runtime_inbox.go:119 |
| `sessions` sync.Map of mutexes keyed by BaseKey | per session | Capture ordering, optional-delivery marker | queue.go:263, 267 |
| `deliveries` keyed by BaseKey + "/raw" or "/optional" | per session lane | One delivery per lane at a time, held across network I/O | delivery_lanes.go:40 |
| `installs` keyed by encoded sha | per payload | Install and reclaim race on same content-addressed file | capture.go:467, reclaim.go:184 |
| `payloadMu` RWMutex | spool dir | Directory scans vs rename/remove only | queue.go:139 |
| `Queue.mu` | counters | `used`, `writing` | queue.go:138 |
| `runtimeLocal.mu` | scheduler | Cursors, init flag, wake maps. Held across SQL in claimSource/claimSession. | runtime.go:93, runtime_store.go:423 |
| `runtime_sources.claim` = version, `runtime_sessions.claim` | SQL rows | Worker ownership of a source or session turn. Released by guarded UPDATE keyed by version. | runtime_store.go:464, step.go release funcs |
| SQLite `MaxOpenConns(1)` + WAL + FULL | process | All metadata, serialises every DB call | queue.go:217 |
| `jobs` PK(session,sequence), guarded `UPDATE ... WHERE state NOT IN (...)` | rows | Ack never overrides `superseded`; attempts bookkeeping | deliver.go:430 |
| Remote CAS (ETag) and `advances` | destination | Newer current never overwritten | publication.go:395, backend.go:371 |

Lock order observed. lane, then session, then install. Capture takes session then install. I found no inversion. [I, from reading call sites, not model checked]

**Timeouts.** Step context 60 s (`OperationTimeout`). Delivery adds `TransferBudget` per selected object (runtime_delivery.go:88-91). R2 flat 30 s for metadata, size-aware body budget, 10 s dial, 20 s header (capturebackend.go:143, 168-174). Discovery batch and admission have separate 60 s contexts (runtime_discovery.go:~205-215). Result bookkeeping uses `context.WithoutCancel` plus its own timeout so a cancelled step still persists state (runtime_delivery.go:101, runtime_step.go:~61-66). [M]

**Shared mutable state and its protection.** Keyed mutex maps are never pruned. `grep` finds no `Delete` on `sessions`, `deliveries` or `installs`. The maps grow per session, per lane and per unique payload for the life of the process. [M]

**A1 lane finding.** Fixed. The 2026-10-30 review (docs/architecture-review-20261030.md:38) described a worker that selected a lane, then re-selected independently, and chose a mutex from one head while reading another after locking. Current code does the following. [M]
- Worker checks `optionalDeliveryHead(...) != optional` before delegating (runtime_store.go:643).
- `deliverDueLaneWork` takes the lane mutex with non-blocking `TryLock`, re-reads the head after locking and returns without an attempt if `hasSelectedOptional(head) != optional` (runtime_delivery.go:58-80). Explicit `Deliver` does the same after locking (deliver.go:127).
- Residual. The guard is skipped if the head manifest fails to unmarshal (`json.Unmarshal(body,&head) == nil &&`, runtime_delivery.go:80). That is deliberate and leads to the quarantine path. No remaining window found, but I did not re-run the barrier tests. [I]

**Fragile spots found.**
1. Single-connection pool plus per-commit fsync means the slowest metadata write paces every worker, and a stuck transaction would stall the coordinator. [I]
2. `runtimeLocal.mu` is held across DB reads in `claimSource` and `claimSession` (runtime_store.go:423, 566), so scheduling is serialised by design but adds a second lock on top of the DB lock. [M]
3. `lockContext` polls with a 5 ms timer (queue.go:282-298). No goroutine per waiter, but latency and wasted wakeups under contention. [M]
4. `check-then-CAS` on local job state is not atomic (deliver.go:235-253). Safe only because the remote `advances` rule and the sequence guard cover it. [I]
5. The `usage()` full-directory stat on every snapshot (capture.go:40-56, 102) is documented as a known limitation (docs/session-capture-phase3.md "Known independence limitation") and is also a failure-coupling point (section 0 item 5). [M]

## 6. Error handling

**Mapping.** Sentinels and wraps. Engine sentinels (`ErrPolicy`, `ErrIntegrity`, `ErrQuota` family, `ErrSourceLimit`, `ErrDestination`), publication sentinels (`ErrConflict`, `ErrRejected`, `ErrNotFound`, `ErrInvalidMetadata`, `ErrDurability`), context errors, SQLite and OS errors. Disposition comes from four walkers.

| Walker | Question | Rule |
|---|---|---|
| `classify` (runtime.go:137) | What class is this for retry and quarantine? | First `errors.Is` hit in a fixed order; custom hook; default transient |
| `runtimeFatalCause` (runtime_fatal.go:24) | Must the coordinator stop? | Any leaf that is a `*os.PathError`, `*os.LinkError`, non-interrupt SQLite error, ENOENT/EIO/ENOSPC/EROFS, `ErrDurability`, `ErrPolicyUnavailable`, `context.Canceled`, closed DB |
| `transientStepError` (runtime_step.go) | May a worker skip this step? | All leaves are deadline, SQLITE_BUSY/LOCKED, or interrupt after op expiry |
| `pureCancellation` / `onlySourceAbsence` (runtime.go:368, runtime_fatal.go:85) | Clean shutdown? Source gone? | All leaves are exactly `context.Canceled` / `*sourceAbsence` |

`sourceAbsence` deliberately hides the underlying `PathError` so that source loss is not confused with a fatal local `PathError` (runtime_fatal.go:51-54). That workaround exists because the classifier keys on Go type identity of leaves.

**Fall-through to wrong defaults.**

1. **Default is retry-forever.** Anything unrecognised becomes `FailureTransient`, which never spends quarantine budget (runtime.go:59-66). Examples that are deterministic but land here. `errors.New("invalid session identity")` and `"archive identity mismatch"` from `checkArchive` (publication.go:147-160). `ErrInvalidMetadata` from a corrupt remote current. JSON decode errors of a remote `Current` (deliver.go:158). An unreadable source file (probe below). [M for the default, I for each example]
2. **Probe, unreadable source file.** File mode 000, permissive policy callback, `Run` for 3 s with a hint pending. Result. `Run` stayed up and returned nil. The error is a bare `syscall.EACCES` from `unix.Openat` (safefile/open.go:~60), not a `*PathError`, so `runtimeFatalCause` says "not fatal" and `classify` says transient. Never quarantined, retried at up to 5 min intervals for ever. [M]
3. **Probe, unreadable ancestor directory.** Parent dir mode 000. `Capture` returned `capturepolicy: resolve: lstat ...: permission denied`, `runtimeFatalCause != nil` and `classify == transient`. `Run` returned that error after 2 ms, i.e. the whole coordinator stopped. In production `policyWith` runs `Resolve` first and returns a plain error that `checkCallback` turns into `ErrPolicy`, so the case becomes `policy-blocked` instead (queue.go:354, capture_runtime_policy.go:~165-170). The fatal route is reachable only if the directory changes after the callback ran, or if a different CheckPolicy is supplied. [M for probe, I for production masking]
4. **`recheckScopes` ignores unknown errors** (enrichment_selection.go:252-259). The contract says unknown exclusion state fails closed (docs/session-capture-contract.md section 3). `recheck` right after it closes the hole for selected sources through `checkCallback` (queue.go:517-535), but `checkDependencies` skips `check` for non-selected sources (deliver.go:284-286) and `recheckScopes` is their only check. [M]
5. **HTTP 401/403 only pauses through the CLI wrapper** (section 0 item 9). In `captureDestinationBackend.bind` the pause also covers `ErrNotFound` and `ErrInvalidMetadata` on `archive.json` (capture_backend.go:~101), so a corrupt or missing archive document pauses the whole destination. That matches the contract's "pause that destination" row. [M]
6. **Timeout vs cancel.** `context.DeadlineExceeded` maps to timeout, `context.Canceled` outside shutdown (for example a parent cancelled with a joined other leaf) falls to transient or to fatal depending on whether a `PathError` is also in the tree. [I]
7. **Immediate Deliver diverges from Run.** `deliverAccepted` uses `runtimeDefaults(RuntimeOptions{})` (deliver.go:140), ignoring the options passed to `Run` (MaxAttempts, Classify, Jitter). The operator command and the daemon can disagree. [M]
8. **Policy callback errors are mostly opaque.** The CLI returns `errors.New("source excluded by filesystem policy")` for several different causes (capture_runtime_policy.go:~255-270). Distinguishing "policy store unavailable" from "excluded" depends on a separate sentinel `ErrPolicyUnavailable` and a hidden `policyStorageFailure` marker type (runtime_fatal.go:94). [M]
9. **Classification is order-dependent.** `ErrDestination` is tested before `ErrPolicy`, `ErrSourceLimit` and `ErrSpoolFull` before `ErrQuota`, because the latter wrap the former. The CLI relies on this to join `ErrDestination` with `ErrIntegrity` for an archive-ID mismatch (capture_backend.go:~104). A reordering changes behavior silently. [M]
10. **`FailureAbsent` is classified only at capture** (runtime_store.go:~501). A `FailureAbsent` that reaches `classify` from delivery is possible only via the unused custom hook. [M]

## 7. Lessons for the Rust rewrite

Each lesson cites the Go evidence. Nothing here is a fix request.

**Keep**
1. **Durable spool plus SQLite with the metadata decision before the unlink.** `acknowledge` and `retireJobs` record reclaim candidates in the same tx, the unlink happens later and `Open` finishes it (deliver.go:439, reclaim.go:149-230, quota.go:146). This convergent shape is the best part of the design.
2. **Hints carry paths, not bytes, and are idempotent.** `WriteHint` names the file by `sha256(key, source)` (runtime_inbox.go:57), drain is "admit then remove". Safe on every crash point.
3. **Guarded writes plus monotone remote rule.** `advances` (publication.go:395) and the ETag CAS make a stale job harmless even when local checks race.
4. **Immutable objects, create-only PUT, read-back reconciliation.** `PutFile` with `IfNoneMatch="*"` then `reconcileImmutable` (backend.go:210-325). Idempotent on lost ack.
5. **Small Backend trait.** `Open, PutFile, Current, CompareAndSwap` (+ optional durability fence) is a real seam (types.go:43-56). Keep it. Make `Current` return a typed `Version` token, not a raw string.
6. **Sentinel-stripping at the S3 edge** (`safeError`, backend.go:110). Rust should do the same and return a closed error enum, not a string.
7. **flock single-writer, claims reset at start.** Justified and simple (queue.go:187, runtime_store.go:~50).
8. **Per-source versioned claim.** Compare-and-set on `version` stops a completion from erasing a newer observation (runtime_store.go:497).

**Drop or collapse**
1. **Fault injection by string** (`q.fault("payload-before-sync")`, 28 distinct names, queue.go:300). Use a trait with a typed `FaultPoint` enum compiled only under a test feature.
2. **Test-only knobs on the production struct** (`discoveryLimit`, `settledLimit`, `sourceLimit`, `enrichmentCap`, `freeBytes`, queue.go:150-164). Inject through constructor parameters.
3. **`RuntimeOptions.Classify`, `Discover`, `Workers`.** No production use (grep). Dead extension points.
4. **Fixtures in the shipped binary** (cli/capture_fixture*.go, capture_isolation_fixture*.go, capture_sdkfixture.go). Move to a test-support crate.
5. **Legacy classify path.** `classifyLegacyPath` vs `classifyCoordinate` (capturesource/classify.go:41-65) is two implementations for one question; the rewrite should have only the coordinate form.
6. **`latest()` epoch back-fill and `addColumnIfMissing`** (queue.go:413-420, runtime_store.go:717). The operator stated retired-schema compatibility is out of scope.
7. **Optional repair and enrichment inside the capture engine.** About 2.4k lines, three more work tables, special SQL triggers. Treat enrichment as a separate consumer of "accepted sources" with its own store, so raw capture cannot be affected by its failures. The contract says enrichment failure is independent of safe capture (contract section 3).
8. **The Rust version should not store the manifest as JSON and re-parse it in the hot path.** Store the few scheduling fields (`session, seq, lane, state, next_retry`) as columns and the manifest as an opaque blob read only when delivering.

**Sum types and newtypes (each prevents a historical bug)**
1. `JobState` as an enum with payloads. Would have prevented the undocumented `policy-blocked`/`integrity-error` states (runtime_delivery.go:151) and the three-column job status (`state`, `enrichment_gap`, `optional_repair_gap`).
2. `Source` as `Selected | Withheld`. Removes `validateWithheld`'s seven-way conjunction (selection.go:38-45) and the `IsSelected` helper called in 20+ places. The `rehydrateSource` integrity checks (enrichment_selection.go:266-285) exist only to re-establish a pairing the types could carry.
3. `Lane` enum (`Raw`, `Optional`) instead of `optional bool` threaded through 10 functions and a string suffix `"/raw"` or `"/optional"` in the lock key (delivery_lanes.go:40-45). A1 was a lane identity mismatch. A `Lane` carried in the claim token and checked by the type of the lock guard makes a wrong-lane attempt unrepresentable.
4. Typestate for the source claim. `Claimed<Version>` returned from `claim_source` and consumed by `complete` or `release`. Today `releaseSourceClaim` re-implements it with `WHERE claim=?` (runtime_step.go:~77).
5. `AuthorizedSource` is already a typestate in spirit (`observePrivate` vs `checkObserved`, authorized_source.go:11-43). In Rust make the only constructor of the readable handle take a `PolicyDecision::Allowed` value, so "read bytes before policy" does not compile. Also replace the `original bool` flag with two types.
6. Newtypes. `SessionKey` (hex base key), `SourceId`, `EpochId`, `Sha256Raw`, `Sha256Encoded`, `PublicationId`, `JobSeq`, `Revision`, `Attempts`, `ObservedAt/CapturedAt` (`Option<Instant>` for the 0 = unknown rule, capture.go:329), `PhysicalPath`, `LogicalPath`, `PolicyIdentity`. The `payload_refs.session` column that mixes `BaseKey`, `'enrichment:'+id` and `'repair:'+id` (reclaim.go:55-58) is exactly what a `RefHolder` enum prevents.
7. `Agent` and `Role` enums with a single `FromStr` parser at each boundary. 15 hand-written agent checks (section 3.4) become zero.
8. Error enums. One `Disposition` enum computed once at the boundary where the error is created, for example `Disposition::{Retry, RetrySlow, PauseDestination, Quarantine, FatalLocal, ShutdownClean}`. Local failures carry it by construction. This replaces four tree walkers and the `sourceAbsence` hiding trick. Probe results in section 6 show the Go default silently picks `Retry`; in Rust the match must be exhaustive.
9. `PolicyDecision::{Allowed, Excluded{scope}, Unknown}`. The callback returns this, and `Unknown` is treated as `Excluded` by one function. That closes the `recheckScopes` fall-through (enrichment_selection.go:252-259).
10. `Epoch` identity. Make `EpochId` content-derived only. Today it includes the local job `sequence` (capture.go:302), so a rebuilt spool that re-captures the same bytes creates a different epoch id and rebase must reconcile or conflict (rebase.go, docs/session-capture-phase3.md "Rebase"). [I]

**Explicit traits**
- `Destination` (Backend). Keep, return `Version`, `Result<_, DestinationError>` with a closed enum including `Unauthorized` (pause), `Throttled`, `Rejected`, `Conflict`, `NotFound`, `Unavailable`.
- `PolicyOracle { fn decide(&self, req: &SourceRef) -> PolicyDecision }`. Today it is a `func(ctx, Request) error` callback whose meaning is encoded in error identity and in which wrapper function was called (`checkCallback`, `recheckScopes`, `check`).
- `Clock`, `Faults`, `Rng` (jitter). Passing them explicitly removes `Options.Now`, `Fault`, and the global `runtimeLog` / `syncDirHook` / `inboxTreeSyncHook` variables (runtime_step.go:~15, queue.go:383, runtime_inbox.go:~140).
- `WorkStore` as a trait over the job and source state machines, so the capture scheduler is testable without SQLite. Today all scheduling queries are SQL strings (runtime_store.go:19, 566).

**Where a typestate or newtype would have prevented a historical bug**
- A1 lane window (docs/architecture-review-20261030.md:38). `Lane` newtype on the lock and on the claim.
- Lost-ack and stale ack (`acknowledge` guard `state!='superseded'`, deliver.go:430). `Job<Superseded>` cannot be acked.
- Epoch back-fill for old rows (queue.go:413). Versioned evidence type, or no old rows.
- Boolean `delivery` on `checkObserved` (queue.go:331). Two entry points, `check_for_capture` and `check_for_delivery`, with different return types.
- `attempts` read-then-add-one in `deliveryResult` (runtime_delivery.go:131). `Attempt` token produced by `begin_attempt` that carries the incremented count.

**Concurrency design for Rust**
- Replace `MaxOpenConns(1)` plus mutexes with one writer task that owns the SQLite connection and receives commands over a channel. Workers hold no DB handles. This keeps the single-writer property explicit and removes `runtimeLocal.mu` held across SQL.
- Prune lock maps on release (`Weak` entries or a keyed lock table with drop-on-last).
- Cap an in-flight capture per session by a semaphore of one rather than polling `try_lock` every 5 ms.
- Decide `usage()` accounting once. A counter reconciled at startup and by a background pass removes the per-snapshot directory scan and the global failure coupling.

## 8. Open questions and unverified items

### Open questions for the operator
1. Should a quarantined head job stop **new full snapshots** for that session (stop accepting, mark degraded) or keep accepting them? Today the spool absorbs them (section 0 item 6).
2. Is orphan payload retention (crash between payload install and job commit) acceptable forever, or should the rewrite reconcile files to rows at startup? The contract asks for "crash reconciliation between filesystem payloads and database work records" (contract section 2).
3. Should an unreadable source (EACCES) be a deterministic class that quarantines, or transient forever? Today it is transient (probe).
4. Is it acceptable for the Rust writer and the Go reader (or old Go publications) to need the same `PublicationID` and `EpochID` derivation, or may the Rust format start fresh? The R2 archive is allowed to be rebuilt, so a clean ID scheme is possible.
5. Should enrichment and optional repair remain part of the capture process in the rewrite, or run as a second consumer with its own store?
6. Is one process owning the spool with `queue.lock` still the model, or should hooks write directly into a durable queue the daemon drains (which already exists as the inbox, with a separate flock)?

### Things I could not verify
- Failure coupling through `usage()` (section 0 item 5) is from reading `payloadUsage` and `snapshotAuthorized`. I did not plant a stray file and run it.
- Unbounded spool growth behind a quarantined head (item 6) is inferred from `acceptSnapshot` and `retireJobs` and not exercised.
- I did not execute the A1 barrier tests; the "fixed" verdict is from reading the guards.
- Lock ordering (lane, session, install) was read, not model-checked or run under the race detector.
- I did not read in full: enrichment_*.go (~1.9k lines), optional_repair*.go, derived*.go, capturesource discovery walk internals, capturehooks apply, `exclusion` and `adapters` packages, publication reader/export and heads. Statements about those are limited to what the cited call sites show.
- Capture of a source near the 1 GiB bound under the 60 s `OperationTimeout` involves three full reads plus compression (capture.go:123, 134, 264-269, 498). Whether that fits on BC-01 is unmeasured. [G]
- `runtimeLocal.wakeSeen` / `wakeSnapshot` map protection (runtime.go:98-99) was not traced.
- Probes ran on macOS with a permissive policy callback and local backend. They do not exercise the CLI composition or R2.
