# Read-only analytics with DuckDB

## Publication dashboard candidate (explicit opt-in, wire v2)

The **existing** dashboard and query entrypoints now accept `--source legacy|publications`.
`legacy` remains the default with every existing database/report path unchanged.
The candidate is an offline consumer, not deployment or complete capture acceptance.
It reads only a separately imported `session-reader/v2` / `session-publication/v2`
projection with `publication_binding.version = 2`; legacy and older publication DBs
are refused read-only **before the HTTP socket is bound**, without migration/reset.

```sh
# Serve an already imported, separate publication projection; no report file required.
.analytics-venv/bin/python tools/analytics/serve.py --source publications --port 8765
# Default candidate DB: .analytics/publications.duckdb (never the legacy events DB).
.analytics-venv/bin/python tools/analytics/query.py --source publications --sql \
  'SELECT archive_id,machine_id,agent,session_id,publication_id FROM sessions'
# Either command also accepts --database /private/path/publications.duckdb.
```

Import explicitly using `publications.py import EXPORT --database DB`, with a pinned
export produced by the authoritative Go reader; see
[analytics projection](../../docs/session-analytics-v1.md). No GET/Refresh triggers an
import, reader, storage request, classifier, or other enrichment. `--report` is rejected
in publications mode. Candidate query mode requires explicit local read-only SELECT SQL;
it never runs legacy `report.sql`. SQL is an operator interface, not an HTTP API.

`publication_dashboard.PublicationDashboard(database)` exposes `report()`,
`sessions(query)`, `trace(query)`, `event(query)`, and `issues(query)` where query values
are lists like `parse_qs`. HTTP paths remain `/api/report`, `/api/sessions`, `/api/trace`,
`/api/event`, `/api/issues`. Lists retain `items,total,offset`; trace also retains
`file,focus_line`. Sessions lists **source epochs joined to their selected session**,
not invented persisted legacy `files`. `trace_id` is the importer's archive/session/
effective-source-hash/epoch identity. Source, epoch, role, revision, publication, machine,
archive and withholding coordinates are explicit; project is unknown, never guessed.
The report is the actual `publications.report` v2 result copied with
`dashboard_wired=true` **only by this adapter**. Direct `publications.report` remains false.
Nanoseconds, sequences, revisions and body sizes are exact decimal strings in HTTP
JSON. The UI converts nanoseconds explicitly for display, treating zero as unknown.

The narrow UI shows coverage, independent freshness, import/normalization observations,
and selected-source event/raw exploration. Findings, classifications and reviews are
explicitly **unavailable**, not a green empty result. Historical research, classifications,
frozen reports and the existing browser review key are untouched and not rebound to this
archive. The legacy views/history remain available with `--source legacy` (or no flag).

Raw access reads only the currently selected `sources.path` in the importer's adjacent
`DB.bodies/<sha256>` owned cache, verifies full selected bytes on the same descriptor,
and slices byte coordinates (including malformed Unicode JSONL). It never opens
`original_path`, transient exports, or old retained bodies for withheld coordinates.
Opaque artifacts have no transcript raw-line view. Raw lines are capped at 1 MiB;
raw verification is capped at 1 GiB per request and streams 1-MiB blocks. Larger evidence
remains in the projection/owned cache but is unavailable through this raw API. Source/
event pages cap at 200, observation pages at 500; offset caps at 10,000,000. Loopback,
Host/cross-site checks, fixed assets, CSP/security headers and text-only rendering are
shared unchanged with legacy mode. Keep the projection and owned cache private.

Focused synthetic tests (no real sources/credentials/services):

```sh
nix develop --command env PYTHONDONTWRITEBYTECODE=1 .analytics-venv/bin/python \
  -m unittest discover -s tools/analytics -p 'test_publication_dashboard*.py' -v
```

### Current offline evidence, not release acceptance

The [2026-10-24 evidence](../../docs/session-capture-offline-evidence.md) records
all **11 actual-process journeys** and complete analytics discovery
(**73 tests / 23.744s**) passing, alongside fresh full check/test/fuzz gates.
Wire/reader and `session-analytics-report` are **v2**; the Go CLI retains the
`capture-v1` prefix. Explicit SDK R2 construction, native context, independent
enrichment and aliases are implemented candidates, not missing factory/caller work.
Actual conditional R2, native agents, installed paths on both machines and
representative resource/freshness acceptance remain separate. Defaults remain legacy;
no cutover, historical research migration or classifier activation occurred.

## Legacy research pipeline (historical reference)

> **Not supported for the current collector or its replacement.** `r2_metadata.py`,
> `bodies.py`, and the body → events → report pipeline below were built against
> retired schema-3 **manifest payloads** and direct file commits. The `v3/`
> namespace and schema-3 publication envelope do not make current session payloads
> (schemas 4/5) compatible. Do not use these refresh/rebuild commands to claim
> current capture coverage or freshness.
>
> Direction: writer → **one authoritative storage reader** → actual metadata/body
> importer → events/report. See [the contract](../../docs/session-capture-contract.md)
> and [the review](../../docs/research/session-capture-contract-review.md).
> The separate opt-in v2 candidate above now implements that reader/export consumer,
> archive binding and source/epoch identities. The legacy pipeline below does not gain
> those capabilities and must not be used as a replacement ingestion path.
>
> Existing local historical research remains accessible: use **Open the existing
> report** below, or query the local database, without refreshing or rebuilding.
> Preserve local bodies, citations, review/classification artifacts and reports;
> their timestamps describe historical processing, not current collector health.

## Legacy import setup (not current-format ingestion)

From the repository root (Python 3.10+ and `uv`):

```sh
uv venv --python python3 .analytics-venv
uv pip install --python .analytics-venv/bin/python -r tools/analytics/requirements.txt
.analytics-venv/bin/python tools/analytics/r2_metadata.py
.analytics-venv/bin/python tools/analytics/query.py
```

The importer reads `~/.config/agent-sessions/config.env` by default. Use
`--config .env` for this repository's local credentials, or supply `R2_ENDPOINT`,
`R2_BUCKET`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY` via the environment
(environment overrides file values). Prefer a bucket-scoped **read-only** token.
No credentials are stored in DuckDB or printed. Config is parsed as simple
`KEY=value`, not executed as shell code.

The only R2 operations are paginated LIST and GET of v3 **manifest commits**.
No transcript bodies, file commits, snapshots or launch commits are downloaded.
The persistent DuckDB database is `.analytics/sessions.duckdb`. Raw immutable
manifest bodies are cached locally for repeat imports, with SHA-256 checked on
all reads. The importer requires a complete, unique parent chain per session;
it refuses conflicts/gaps instead of choosing the largest timestamp/sequence.
A transaction replaces the tables only after every chain passes. Eight download
workers are used; the initial import makes one GET per historical manifest.
Later runs still LIST the bucket but reuse immutable cached manifests.

Before any local writes or GETs, the importer rejects inventories containing
`/records/session/…/commits/` paths, including mixed legacy/session inventories.
It exits nonzero with an explicit unsupported-contract diagnostic and leaves the
existing database, refresh timestamp and caches untouched. Unsupported record or
manifest payload schemas also fail explicitly before database replacement. This
is a narrow guard against known incompatible publications, **not** a resolver,
full inventory validator or automatic detection of every future contract. A truly
empty bucket remains a valid empty legacy refresh (and replaces previous metadata);
an empty result must not be interpreted as proof of current capture completeness.

`.analytics/` and `.analytics-venv/` are gitignored. Data files are created with
0600 permissions and directories with 0700. Manifests can contain sensitive
paths, git information, and launch payloads: keep this directory private. This
is not a sanitized/public dataset. Do not enable SDK debug logging.

## Query the local database

```sh
.analytics-venv/bin/python tools/analytics/query.py --sql \
  'SELECT agent, count(*) AS sessions FROM sessions GROUP BY agent'

.analytics-venv/bin/python tools/analytics/query.py --sql \
  "SELECT agent, model, count(*) AS files FROM transcripts WHERE role = 'main' GROUP BY ALL ORDER BY files DESC"
```

Tables/views:

- `inventory`: object keys, stored sizes, modification timestamps from LIST.
- `manifest_commits`: all downloaded committed manifest generations as JSON.
- `current_manifests`: one chain-selected manifest per machine/agent/session.
- `sessions`: identity, info/git JSON, sync observations, launch count.
- `transcripts`: current manifest entries, roles, decoded sizes, hashes, lines,
  model and bounded head/tail timestamps. This includes associated artifacts
  such as tool results/file history; use `role` filters for transcript-only work.
- `refresh_info`: local import completion time.

`report.sql` contains the bundled analytics. `views.sql` defines the projections.
Run the offline regression tests with:

```sh
.analytics-venv/bin/python -m unittest discover -s tools/analytics -v
```

## Interpretation and limits

- Session counts mean **machine/agent/session manifests**, not distinct human
  conversations, successful runs or sessions with an archived main transcript.
  The same session on two machines counts twice. Bodies may also contain replayed
  events; JSONL line counts are not message/tool-call counts.
- Model names are source strings from bounded metadata observations, not a
  normalized model catalog, per-message usage, token counts or spend.
- Start/event observations are not exact durations. The timestamp projections
  parse ISO strings only, preserving numeric source timestamps separately rather
  than guessing units. Report rendering uses UTC.
- LIST is not an atomic bucket snapshot. Hooks can publish during an import.
  If a chain is incomplete, retry. Manifest views can lag newer file commits;
  no dependency/body verification or full archive audit is claimed.
- Stored bytes include compressed blobs, shared content, history and duplicated
  plan/commit metadata. Current decoded logical bytes are **not** a compression
  ratio denominator for total bucket bytes.
- No explicit backfill marker is assumed: this reports the available archive,
  including subsequent hook captures. Legacy v2 data is not imported; it still
  appears in the inventory if present.
- Dependency versions are pinned at the top level in `requirements.txt`; these
  tools are optional and do not change the collector or its bucket contract.

DuckDB setup follows its [Python API documentation](https://duckdb.org/docs/current/clients/python/overview).

## Private session observatory

The optional tools below go beyond the metadata-only importer above. They reconstruct
**current main and subagent transcript bodies**, build a separate event index, and serve
a local HTML investigation dashboard. They do not change the collector, archive format,
or anything in R2. A metadata refresh alone still does **not** download transcripts.

### Open the existing report

From the repository root:

```sh
.analytics-venv/bin/python tools/analytics/serve.py --port 8765
```

Open **http://127.0.0.1:8765**. If the port is occupied by an existing dashboard, open
that address rather than starting another copy, or choose another `--port`.
The frontend is `site/index.html`, `site/app.js`, and `site/style.css`; it needs the
local server for paginated trace access, so opening `index.html` with `file://` is
not the supported mode. No frontend build, CDN, external fonts or telemetry is used.

Pages:

- **Overview:** explicitly labelled global counts, activity timeline, agent activity,
  candidate themes and the processing chain. Activity is not an agent quality ranking.
- **Findings:** analyst-reviewed interpretations with exact source quotes and proposed
  practice/prompt/persistence actions. Analyst review is not the user's approval.
- **Corrections:** classifier candidates, the preceding context, complete questions,
  submitted state, returned probabilities/model/usage and review notes.
- **Discovery & code map:** reviewed recurring-question groups, their full-trajectory
  tool counts, recorded read references, path-reference heatmap and exact tables.
- **Trace explorer:** agent/project/session filters, text search, message-role filters,
  source-line citations, surrounding events, tool arguments/results, and raw source.
  Text views include **middle**, head, tail, and full. Omissions are labelled; raw
  events remain accessible even for malformed lines with no normalised component.
- **Capture health:** absent main references, malformed versus unsupported source records,
  unmatched tool IDs, reconstruction status and inference boundaries.
- **Pipeline & prompts:** stages, exact classifier questions, selection policy, model
  and served version, cache/usage, iterations, spot checks and limitations.

Approvals/rejections and notes live only in this browser's localStorage for this origin.
Download feedback JSON to retain/share them; it contains private findings. Nothing here
installs a memory, edits CLAUDE.md/AGENTS.md, or claims that approving a proposal improves
outcomes. Feedback is an input for a future reviewed iteration, not an automatic loop.

**Private data:** raw transcripts can contain secrets and personal/business information.
The server binds only `127.0.0.1`, rejects non-loopback Host/cross-site browser requests,
serves a fixed asset allowlist, and exposes neither arbitrary SQL nor filesystem paths
as read parameters. It has no user authentication: do not expose/forward its port,
publish the report/cache, or use it on a shared untrusted machine. Credentials are
used only by the importer/model runtime, never included in report configuration.

### Rebuild the evidence and classifications (legacy historical inputs only)

**The remote refresh/body steps below are not a current collector rebuild path.**
Do not rerun this chain against a replacement archive or silently reuse historical
citations/classifications for newly published data. Offline report access above
requires none of these rebuild steps.

Additional prerequisites: `zstd` executable, Node 22+, installed Pi SDK and working
TypeSafe Jev credentials already configured in Pi. Python dependencies are unchanged.
The classifier explicitly selects TypeSafe Jev; it never silently substitutes another
provider/model. `--probe --output <new-private-file>` lists usable classifiers and makes
one benign request. Jev is a model service, not an executable named `jev`.

```sh
umask 077
.analytics-venv/bin/python tools/analytics/r2_metadata.py
.analytics-venv/bin/python tools/analytics/bodies.py --workers 8
.analytics-venv/bin/python tools/analytics/events.py
.analytics-venv/bin/python tools/analytics/analyze.py prepare --max-items 650

# Set this to the installed Pi dist/index.js if not resolvable as a local package.
export PI_CLASSIFY_SDK=/opt/homebrew/Cellar/pi-coding-agent/1.2.0/libexec/lib/node_modules/@earendil-works/pi-coding-agent/dist/index.js
node tools/analytics/classify.mjs \
  --input .analytics/report/inputs.json \
  --questions .analytics/report/questions.json \
  --output .analytics/report/classifications.json \
  --cache-dir .analytics/report/jev-cache \
  --max-items 650 --concurrency 4

.analytics-venv/bin/python tools/analytics/analyze.py build
.analytics-venv/bin/python tools/analytics/serve.py
```

Before rerunning classification, archive the existing `inputs.json`, `questions.json`,
`selection.json`, and `classifications.json` together in a private run directory.
The classifier refuses to overwrite an output file. Failures have nonzero exit status
and remain in the output; inspect them before an explicit rerun with a new output path.
Successful records are content-hash cached using model metadata, questions and state;
failed records are not cached. An explicit rerun can reuse successes and retry failures.
Keep the failed run too. A mutable `jev-latest` cache retains the originally served
version: use a new cache directory when intentionally reevaluating an upgraded alias.

The metadata/body importers accept `--config`; by default they read the existing
collector config (environment overrides it). They only LIST/GET, with no remote mutation.
`bodies.py` checks the committed manifest/file identities, descriptor checksums, ordered
piece offsets/lengths, encoded and decoded piece checksums, zstd completion and whole-file
checksum. Valid decoded bodies are reusable. Corrupt cache content is verified before
reuse. `--limit` is diagnostic only and marks the resulting index incomplete.

`events.py` rechecks whole-body hashes, preserves exact byte offsets and native IDs,
streams bounded batches into a temporary DuckDB, and atomically replaces the index only
on completion. `bodies/index.json` records coverage/failures; `events.duckdb` holds
`files`, `events`, `issues`, and `build_info`. Raw bodies remain under `bodies/files/`.
New private data files are 0600, directories 0700. Expect several GB for a full backfill.

`analyze.py prepare` makes a deterministic, per-transcript-capped stratified selection
of human correction signals, nonmatching human controls, subagent discovery assignments
and assistant-message fact candidates. It is **not a census or random population sample**.
Full text stays local; long classifier context uses labelled head/middle/tail windows.
Narrow token/bearer redaction is defence in depth before model requests, not universal
DLP. Chosen state/questions are sent to the explicitly selected classifier service.

`analyze.py build` combines measurements, classifications and private review artifacts:
`reviewed.json`, `discovery-groups.json`, `review-notes.json`, and `evaluation.json`.
These are separate analyst-curated inputs; rebuilding does not fabricate or overwrite
reviewed findings. Exact evidence quotes carry trace/line/character offsets and source
hashes. The initial private report includes a reproducible local curation script;
new datasets require renewed evidence review, not blind reuse of its conclusions.

### Interpretation matters

- Counts refer to current archived source components/identities. A single JSONL line
  can contain several tool calls, text blocks or results. Human headline counts use
  distinct source lines; tool result/error counts are components, not failure rates.
- User-role is not human identity. Known tool results, subagent assignments, handbacks,
  skill/environment injections, internal transcript markers and image-only markers are
  excluded from human-text attribution. The policy is heuristic and tested, not perfect.
  Source content is retained regardless of the attribution label.
- Known native-ID duplicates and Codex user-message mirrors are flagged within a trace.
  Cross-machine copies, inherited fork context and all possible replay formats are not
  globally deduplicated. Exact long-prompt repetition is a separate diagnostic, not
  proof of independent support or a universal content-deduplication rule.
- Full branch DAGs are retained through native parent IDs, not reduced to only the final
  active branch. Recorded messages are not necessarily the complete assembled API prompt;
  snapshot/hook reconstruction is required to establish historical instruction exposure.
- Only explicit error flags count. Missing/unknown status is not success, and expected
  test/grep failures are not automatically agent mistakes. Some Codex result encodings
  do not supply the flags consumed by this parser. Do not compare agents by this count.
- Unsupported control records and malformed JSON are separate observations. A verified
  malformed line was present in captured source bytes; it is not a hash mismatch.
- Main/subagent bodies exclude separately stored tool-result artifacts, snapshots,
  superseded generations and other auxiliary bodies. Missing-main references are a
  reconciliation queue, not a claim of collector loss or a complete archive audit.
- Jev probabilities are uncalibrated screening signals. Self-corrections, new requirements
  and approvals can be false positives. The spot check is not a held-out evaluation or
  population precision/recall claim. No outcome improvement has been measured.
- The requested alias and returned served model are retained. Zero SDK catalog cost for
  direct TypeSafe does **not** establish free service; actual dollar cost is unknown.

### Tests

```sh
.analytics-venv/bin/python -m unittest discover -s tools/analytics -v
node --test tools/analytics/test_classify.mjs
node --check tools/analytics/site/app.js
make check
```

Synthetic offline tests cover reconstruction tampering/order/repeated pieces, origin
attribution/mirrors, Unicode byte offsets, atomic replacement, classifier cache/errors,
loopback/API boundaries, malformed-source access, and evidence-centred pagination.
The historical legacy dashboard was also checked in headless Chromium against the actual local report,
including filters, deep links, tool calls/results, full/middle/raw views, local feedback,
mobile layout and absence of external requests.
