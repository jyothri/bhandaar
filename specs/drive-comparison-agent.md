# Drive Comparison Agent — Spec

## Implementation Status

_Last updated 2026-09-20._ Code lives at `agent/linux/` (module `github.com/jyothri/bhandaar/agent/linux`).

| Section | Status | Notes |
|---|---|---|
| §2 Language & Runtime | ✅ Implemented | Go 1.27.1, no CGO (`modernc.org/sqlite`, `lukechampine.com/blake3`). |
| §3 Architecture | ✅ Implemented | Package layout matches: `cmd/driveagent`, `internal/{scan,store,compare,report}`. |
| §4 Checkpoint / Resume | ✅ Implemented | SQLite WAL schema (`drives`/`files`/`scan_runs`), idempotent re-walk resume, per-file error handling, root-disappearance detection. Verified on real drives: re-running `scan` after a completed run skipped 100% of files (103/103 and 163/163) with zero re-hashing. |
| §4.2 Concurrent Scans | ✅ Implemented | `PRAGMA busy_timeout=5000` lets two `driveagent scan` processes safely share one `state.db`. Verified: two concurrent processes (1,500 files each, different `drive_id`s) both exited 0 with zero lock errors and all rows landed. |
| §5 Hashing Strategy | ⚠️ Implemented, with a nuance | Every new/changed file gets a full streamed BLAKE3 hash during `scan`. The `(size, mtime)` check is used to skip re-hashing on repeat scans (resume) rather than as a separate cross-drive pre-filter applied during `compare` — `compare` only ever reads hashes already computed and stored by `scan`, it doesn't hash anything itself. |
| §6 Comparison Algorithm | ✅ Implemented | Exact-path matching for common/diverged; multiset content-hash matching (oldest-mtime-first tie-break) for relocated/missing. |
| §6.1 macOS Metadata Exclusion | ✅ Implemented | `--include-mac-metadata` flag on `compare` (default: excludes `._*`/`.DS_Store` from classification; rows remain in the checkpoint DB either way). |
| §7 Report Output — console & JSON | ✅ Implemented | As specified. |
| §7 Report Output — HTML (flat table) | ✅ Implemented | Sortable/filterable single table, human-readable sizes (KB/MB/GB). |
| §7.1 HTML Folder Rollup View | ✅ Implemented | `internal/report/tree.go` builds the recursive tree (Relocated excluded, as specified) and `report.go` renders it as native `<details>/<summary>` elements — fully collapsed by default, no JS needed. The root itself is a rollup node too: a fully-matching scan collapses to a single `./ common` line rather than listing every top-level folder. Verified: a synthetic mixed fixture correctly rolled a folder with 1 diverged + 2 missing + 1 common file up to a `diverged` badge with breakdown `(1 diverged, 2 missing, 1 common)`, while a nested fully-common folder two levels deep rolled up to `common` at every level. |
| §8 CLI Design | ✅ Implemented | `scan`/`compare` subcommands match the sketch, plus one flag beyond it: `--include-mac-metadata` on `compare` (§6.1). |
| §9 Out of Scope | ✅ Honored | No writes to either drive; symlinks/special files skipped+logged; no ACL/xattr/network-drive support. |
| §10 Open Items | ⚠️ Partially addressed | The `drive_id`/scan-root scoping gap is now fixed (§4.1). Still open: guided repair mode, symlink comparison semantics beyond skip-and-log, scheduled periodic re-scans. |
| §11 Consolidated Aggregate Report | 📋 Planned, not started | Three-command split: `scan` (unchanged, plus `dir_listings` population), `compare` (scoped comparison + always-global relocated pass, persists `files.comparison_status` and incrementally propagates a persisted `folder_status` rollup up to drive root), `report` (new command, pure DB lookups, zero computation, fully offline). Existing checkpoint data will be discarded and re-scanned under the new schema — confirmed acceptable, no code changes yet. |

**Real-drive verification so far:**
- `interview` folder (`/mnt/seagate2/Jyo/Backup/interview` vs `/media/jyothri/Seagate1/Jyo/interview`, 1.6GB, 103 vs 163 files): 103 common, 0 diverged, 0 relocated, 0 missing, 60 files excluded as macOS metadata. Re-running `scan` afterward skipped 100% of files (idempotent resume confirmed).
- `all photos from icloud - Jul 28 2019` folder (136GB, 14,677 files per drive — the largest real folder tried so far): scanned both drives in the background (drive labels `seagate2-icloud`/`seagate1-icloud`, 57m25s and 41m55s respectively, 0 scan errors on either side), then compared: **14,677 common, 0 diverged, 0 relocated, 0 missing.** HTML report correctly collapsed to a single `./ common` line.
- The full drives (`Jyo/Backup` vs `Jyo` roots in their entirety) have **not** been scanned yet — only these two subfolders.
- Root-conflict fix (§4.1): verified a fresh `drive_id` scanned at root A, then rescanned at root B — refused by default with a clear message; with `--replace-root`, root A's checkpoint rows were deleted and root B's took their place; rescanning root B again afterward correctly resumed (skipped, no false conflict).

## 1. Purpose

Two external drives (`/dev/sdb1` mounted at `/mnt/seagate2`, `/dev/sdc1` mounted at `/media/jyothri/Seagate1`) are supposed to hold mirror copies of the same backup content but are suspected to have diverged over time. The actual backup content root is **not** the mount point itself on either drive — each drive has its own top-level layout above the common content:

| Drive | Mount point | Backup root (scan root) |
|---|---|---|
| Seagate2 (`/dev/sdb1`) | `/mnt/seagate2` | `/mnt/seagate2/Jyo/Backup` |
| Seagate1 (`/dev/sdc1`) | `/media/jyothri/Seagate1` | `/media/jyothri/Seagate1/Jyo` |

The tool therefore scans from a **user-supplied root path per drive**, not the mount point — the mount point and the scan root are independent inputs, and there is no assumption that the two drives share the same top-level folder name (e.g. `Backup` exists under one root but not the other). All `relative_path` values recorded in the checkpoint DB (§4) are relative to that drive's configured scan root, so that a file at `/mnt/seagate2/Jyo/Backup/Photos/a.jpg` and one at `/media/jyothri/Seagate1/Jyo/Photos/a.jpg` both normalize to the same `relative_path` (`Photos/a.jpg`) and compare correctly despite the differing top-level layout.

This tool scans both roots and produces a report classifying every file into one of:

- **a) Common** — same relative path on both drives, content identical.
- **b) Diverged** — same relative path on both drives, but content differs (candidate for corruption/manual repair). The tool cannot algorithmically prove *corruption* vs. an intentional edit — it flags the mismatch and leaves the repair decision to the user.
- **c) Missing** — present on one drive, absent (by path and by content) on the other.
- **d) Relocated** — same content (hash match) present on both drives, but at a different relative path.

This is a **standalone, report-only** tool: it never writes to either drive. It lives at `agent/linux/` in this repository, independent of the Bhandaar web app/Postgres backend (no shared runtime dependency), so it can run directly against mounted drives on a Linux box.

## 2. Language & Runtime

**Go.** Rationale (recorded from clarification round):
- The task is I/O-bound (spinning-disk reads dominate), so cross-language performance differences are secondary to correctness and operational simplicity.
- `be/collect/local.go` already establishes a Go pattern for recursive filesystem walking + hashing in this repo; this agent can follow the same idioms without sharing runtime state.
- Single static binary — easy to run standalone against mounted drives, no JVM/interpreter needed.
- Goroutines + channels give a natural worker-pool model for concurrent hashing, with easy tuning down for spinning-disk seek costs.

Target: Go 1.22+, no CGO dependency (use a pure-Go SQLite driver, e.g. `modernc.org/sqlite`) so the binary stays portable and doesn't require a C toolchain to build.

## 3. High-Level Architecture

```
agent/linux/
  cmd/
    driveagent/         # main package, CLI entrypoint
  internal/
    scan/               # filesystem walk + hashing
    store/               # checkpoint DB (SQLite) access layer
    compare/             # comparison engine (a/b/c/d classification)
    report/              # report generation (text/JSON/HTML)
  go.mod
```

### Phases

1. **Scan phase** (run once per drive, independently resumable):
   - Walk the drive's directory tree.
   - For each regular file, record `(relative_path, size, mtime, mode)`.
   - Compute a **quick signature** cheaply; only compute a **strong hash** when needed (see §5).
   - Upsert results into a local checkpoint database (§4), keyed by `(drive_id, relative_path)`.

2. **Compare phase** (runs after both drives have a complete scan record):
   - Load both drives' file records from the checkpoint DB.
   - Classify every file into common / diverged / missing / relocated (§6).
   - Emit a report (§7).

The two phases are independent CLI subcommands so a scan can be re-run/resumed without repeating a full re-compare, and a compare can be re-run cheaply after a scan is refreshed.

## 4. Checkpoint / Resume System

**Storage:** a local SQLite database (pure-Go driver, WAL mode), stored **off the external drives** by default (e.g. `~/.driveagent/state.db`, overridable with `--state-dir`) so that it survives a drive being unmounted, disconnected, or replaced mid-run.

**Schema (conceptual):**

```sql
CREATE TABLE drives (
  drive_id     TEXT PRIMARY KEY,   -- user-supplied label, e.g. "seagate2"
  scan_root    TEXT NOT NULL,      -- e.g. "/mnt/seagate2/Jyo/Backup" — content root, NOT the mount point
  last_scan_started_at  TIMESTAMP,
  last_scan_completed_at TIMESTAMP
);

CREATE TABLE files (
  drive_id      TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  size          INTEGER NOT NULL,
  mtime_unix    INTEGER NOT NULL,
  mode          INTEGER NOT NULL,
  quick_sig     TEXT,              -- cheap pre-filter signature
  content_hash  TEXT,              -- strong hash, nullable until computed
  hash_algo     TEXT,              -- e.g. "blake3"
  status        TEXT NOT NULL,     -- 'pending' | 'hashed' | 'error'
  error_message TEXT,
  scanned_at    TIMESTAMP NOT NULL,
  PRIMARY KEY (drive_id, relative_path)
);

CREATE TABLE scan_runs (
  drive_id   TEXT NOT NULL,
  started_at TIMESTAMP NOT NULL,
  finished_at TIMESTAMP,
  files_seen  INTEGER,
  bytes_hashed INTEGER,
  interrupted BOOLEAN
);
```

**Resume strategy — idempotent re-walk, not positional resume.**
Rather than trying to remember "the exact file we were on" (fragile across crashes, and directory iteration order isn't guaranteed stable across runs), a scan simply **re-walks the entire tree** every time it's (re)started, and for each file:

- If a record already exists for `(drive_id, relative_path)` with matching `size` and `mtime_unix`, the file is treated as already processed and **skipped** (no re-read, no re-hash).
- Otherwise (new file, or size/mtime changed since last recorded), the file is (re-)hashed and the row is upserted.

This makes interruption-safety free: a kill -9, power loss, drive disconnect, or `ctrl-c` at any point just means "next run picks up where it left off" with no special recovery logic, and it also naturally supports periodic re-scans later if the drives keep changing. Each file's row is committed in small batches (e.g. every N files or every few seconds) inside a transaction, so a crash loses at most that small batch of in-flight work.

**Interruption handling:**
- I/O errors reading a specific file (bad sectors, permission denied) are caught, logged, the file marked `status='error'` with the OS error message, and the walk continues — a single unreadable file must not abort the whole scan.
- If the drive disconnects entirely mid-scan (mount path disappears), the scan stops cleanly, marks the `scan_runs` row `interrupted=true`, and exits with a clear message telling the user to reconnect and re-run.
- SQLite WAL mode + small transactions bound the damage from an unclean process kill to the current in-flight batch.

### 4.1 `drive_id` / Scan-Root Scoping

`files` rows are keyed by `(drive_id, relative_path)` only — not by scan root. Found during real-drive testing: scanning a *different* root under a `drive_id` that was already used for an earlier root left the old root's rows in place forever (their relative paths just don't correspond to anything under the new root), silently polluting any later `compare` that reused the label with stale, orphaned entries.

Fixed as follows: `scan` now checks, before doing any work, whether `drive_id` already has a recorded `scan_root` that differs from the one just given (comparing resolved absolute paths, so trivial formatting differences like a trailing slash don't false-trigger).

- If it differs and `--replace-root` was **not** passed: `scan` refuses immediately (before touching the checkpoint DB or doing any I/O) with an error naming both roots and explaining the two ways forward — pass `--replace-root`, or use a different `--drive-id`. Exit code is non-zero; no scan output is printed, since nothing happened.
- If `--replace-root` **was** passed: all `files` and `scan_runs` rows for that `drive_id` are deleted first, then the scan proceeds against the new root as if it were a brand-new label. The `drives` row itself is updated in place to the new `scan_root`.
- Rescanning the **same** root as before (the common, everyday case) is unaffected — no conflict is detected, and resume/skip behavior (§4) works exactly as before.

This makes `drive_id` behave as intended — a stable label for "whatever is currently at this scan root" — without requiring the user to manually track which labels have been used for which roots (the workaround used earlier in this project, before this fix, was giving each distinct scan root its own suffixed label, e.g. `seagate2-icloud`).

### 4.2 Concurrent Scans Across Physical Drives

Two separate `driveagent scan` processes — one per physical drive, run as independent background jobs — are supported and safe, sharing the same checkpoint DB (`state.db`) by default. This is a deliberate capability, not just an accident of the data model: distinct `drive_id`s never share rows, so there's no logical conflict, and two genuinely separate physical drives don't compete for disk I/O the way concurrent workers on the *same* spinning drive do (§5) — the main benefit is wall-clock speedup, roughly `max(time_A, time_B)` instead of `time_A + time_B` for a sequential run.

The one real risk was at the SQLite layer: each process opens its own connection to the same `state.db` file, and without a busy-timeout, one process's write-batch commit landing at the same moment as the other's would fail immediately with "database is locked" rather than simply waiting. Fixed by setting `PRAGMA busy_timeout=5000` in `store.Open` — a writer now waits up to 5s for the other process's brief (sub-second) transaction to finish instead of erroring. `SetMaxOpenConns(1)` alone (already in place) only serializes writes *within* one process; it does nothing for two separate OS processes.

Verified: two `driveagent scan` processes launched simultaneously against the same `state.db` (1,500 files each, different `drive_id`s) both completed with exit code 0, zero lock/busy errors, and all 3,000 rows landed correctly.

**Remaining caveat (not addressed by this fix, and not really fixable in software):** if both drives share a USB hub or host controller, they compete for aggregate USB bandwidth regardless of the SQLite fix, and the expected speedup shrinks or disappears. Whether that applies depends on how the drives are physically connected.

## 5. Hashing Strategy

Two-tier approach, chosen to avoid full-file reads for files that are obviously unchanged/distinct:

1. **Quick pre-filter:** `(size, mtime)` — if two candidate files being compared differ in size, they cannot be identical; skip strong hashing for the identity check entirely where possible.
2. **Strong content hash:** computed once per file during the scan phase (not during compare) and stored, so re-comparisons never re-hash. Algorithm: **BLAKE3** (via a pure-Go/Cgo-optional library) — much faster than MD5/SHA-256 on modern CPUs while providing strong collision resistance for corruption detection, and independent of the MD5 already used elsewhere in this repo (`be/collect/local.go`) since this tool doesn't share state with that pipeline.
3. Hash the full file content (streamed, not loaded fully into memory) — for corruption detection, a partial/sampled hash is not acceptable since corruption can occur anywhere in the file.

**Concurrency:** a bounded worker pool per drive for hashing. Since these are external spinning-disk drives (USB HDDs), high concurrency *hurts* throughput (seek thrashing) rather than helping. Default: **2 concurrent hashing workers per drive** (independent pools for each drive, since they're separate physical devices), configurable via `--workers`.

## 6. Comparison Algorithm

Given the fully-scanned file sets `A` (drive A) and `B` (drive B), each row is `(relative_path, size, content_hash)`:

1. **Exact path match:** for every `relative_path` present in both `A` and `B`:
   - `hash(A) == hash(B)` → **Common**.
   - `hash(A) != hash(B)` → **Diverged**.
2. For paths remaining only in `A` or only in `B` (no exact path match on the other side), attempt **content matching** against the other drive's *remaining, unmatched* files:
   - If a file with the same `content_hash` exists among the other drive's unmatched files → **Relocated** (record both paths). Matching is done as a multiset (duplicate content on one side maps to duplicates on the other, oldest-path-first tie-break) to handle multiple copies of identical content correctly.
   - If no content match is found → **Missing** (present on this drive only).

All matching happens in the compare phase directly against the checkpoint DB (SQL joins keyed on `relative_path` and `content_hash`), not by re-reading the drives.

### 6.1 macOS Metadata Exclusion

Copying files from/to a Mac leaves behind AppleDouble resource-fork sidecar files (`._<name>`) and Finder's per-folder `.DS_Store`, which are copy artifacts, not real content divergence — left unfiltered, they dominate the Missing category (confirmed in practice: 56 of 60 "missing" files in an initial real-drive trial were `._*`/`.DS_Store`).

By default, the compare phase excludes files matching `._*` or `.DS_Store` (by basename) from classification entirely — they are not counted as Common, Diverged, Missing, or Relocated, and don't appear in any report. A `--include-mac-metadata` flag restores them into classification for the rare case they're wanted. In both cases, the checkpoint DB is unaffected — these files are still scanned and hashed normally by the scan phase; the exclusion is a compare-phase/report-time filter only.

## 7. Report Output

Generated at the end of the compare phase, three forms (all from the same underlying result set):

- **Console summary** — counts per category, top N diverged/missing examples, elapsed time.
- **JSON report** — full machine-readable listing of every file in every category (for scripting/manual repair later). This shape does **not** change with the folder rollup in §7.1 — it stays the flat per-category lists described here, so scripts/tooling built against it keep working.
- **HTML report** — a folder-rollup tree view (§7.1) rather than one flat table, for manual review — useful given this may run into the thousands of rows. File sizes are shown in human-readable units (KB/MB/GB, binary/1024-based) rather than raw byte counts.

No automated repair action is taken by this version; the report is the deliverable, and any copying/deletion to fix divergences is a manual, separate step the user performs after reviewing the report.

### 7.1 HTML Report: Folder Rollup View

Rather than one flat table of every file, the HTML report presents a **recursive folder tree** so the common case — an entire subtree matches — collapses to a single line instead of thousands of identical-looking rows.

**Status rollup, computed bottom-up per folder:**

- A folder's status is **Common** only if every file directly in it, and every subfolder's status, is Common.
- Otherwise the folder's status is the **worst** status found anywhere in its subtree, using this precedence (worst first): **Scan error** > **Diverged** > **Missing** > **Common**. (Relocated files are excluded from tree placement — see below.)
- Alongside the worst-status badge, each non-common folder shows a **breakdown of counts** by category across its whole subtree, e.g. `Diverged (2 diverged, 1 missing, 47 common)`, computed once at report-generation time (not lazily) so the number is accurate before any expansion.
- Files excluded from classification (§6.1, macOS metadata) don't affect a folder's Common/non-Common status and aren't shown in the counts, consistent with their exclusion from the flat report today.

**Interaction:**

- The tree starts **fully collapsed** at the root, regardless of status — the user drills down manually, using the badges to decide where to look. (No auto-expansion of problem folders.)
- Clicking a folder row expands exactly one level (its direct children — files and subfolders), showing each child's own rolled-up badge; clicking a child folder expands it in turn. Clicking a leaf file row (or a diverged/missing file reached this way) shows its detail (sizes/hashes/which drive), same detail already shown in the current flat table.
- A folder that already contains only Common children can still be expanded manually to inspect the file listing, even though its badge gives no reason to.

**Relocated files — kept out of the tree:**

A relocated file has two different folder locations (one per drive) with no single natural "home" in a unified tree, so relocations are **not** folded into the tree structure or counted in any folder's rollup/badge. They remain a **separate flat "Relocated" list/section** in the HTML report, exactly as today, shown alongside the tree rather than inside it.

**Tree construction:** built once in-memory from the same `compare.Result` used for the flat JSON/console output — grouping `Common`/`Diverged`/`Missing`/`ScanErrors` entries by their relative path's directory segments into a nested structure, computing each node's status/counts bottom-up, then serializing that tree into the HTML/JS the browser renders. No new data is read from the checkpoint DB for this — it's a presentation transform over the existing classification.

## 8. CLI Design (sketch)

```
driveagent scan   --drive-id seagate2 --path /mnt/seagate2/Jyo/Backup --state-dir ~/.driveagent
driveagent scan   --drive-id seagate1 --path /media/jyothri/Seagate1/Jyo --state-dir ~/.driveagent
driveagent compare --drive-a seagate2 --drive-b seagate1 --state-dir ~/.driveagent \
                    --report-out ./report --format html,json
```

Note `--path` is the **content root** to scan, independent of where the drive happens to be mounted — it may sit several directories below the mount point, and the two drives' roots need not share the same top-level folder name (§1).

- `scan` is safe to re-run any number of times (idempotent, resumable per §4).
- `compare` only reads from the checkpoint DB — it's cheap to re-run repeatedly (e.g. after fixing a few files) without rescanning untouched files.
- `scan` also accepts `--replace-root`, needed only when deliberately repointing a `drive_id` at a different root than it was previously scanned at (§4.1) — omitted here since it doesn't apply to the everyday case.

## 9. Explicitly Out of Scope (v1)

- Any write/modify/delete action on either drive (no auto-repair, no auto-sync).
- Handling of non-regular files beyond logging (device files, sockets, FIFOs are skipped and logged, not compared).
- Cross-filesystem metadata like extended attributes, ACLs, or ownership — only path, size, mtime, and content are compared.
- Network/remote drives — this spec assumes both sources are locally mounted block devices.

## 10. Open Items for Future Iteration

- Guided repair mode (interactive copy-to-fix), previously deferred per user's choice of report-only for v1.
- Symlink comparison semantics (currently: skip and log).
- Scheduling periodic re-scans to catch drift over time.
- ~~`drive_id` / scan-root scoping~~ — fixed, see §4.1.

## 11. Consolidated Aggregate Report (Planned — Not Yet Implemented)

**Status: plan only, for review.** Nothing in this section is built yet. **This supersedes two earlier drafts of §11.** The first computed everything live at `compare` time and folded the overview into `compare`'s own HTML output. The second moved to a three-command split (`scan`/`compare`/`report`) with `report` recomputing a fresh in-memory rollup from raw per-file status on every render ("lightweight," no persisted folder-level rollup). This third version keeps the three-command split but persists the folder-level rollup after all, maintained incrementally by `compare` via parent pointers — the "heavyweight" option originally rejected — once a concrete example (§11.5) showed the "lightweight" recompute gives the *wrong* answer for a case that comes up naturally (a folder whose children were scanned separately still reading `partial` after every child is actually `common`), and a bounded, targeted propagation strategy resolved the original objection to "heavyweight" (relocated detection can't be scoped) without reopening it. Confirmed with the user: existing checkpoint data (`interview`, `all photos from icloud - Jul 28 2019` trials) will be lost and re-scanned from scratch under the new schema — no migration is planned.

### 11.1 Motivation

Today, `compare` both computes classification *and* renders a report, in one invocation, scoped to whatever narrow subfolder each side happened to be scanned at (`interview`, `all photos from icloud - Jul 28 2019`, etc., each under its own suffixed `drive_id` to avoid the §4.1 conflict). There's no single place that shows, for a whole physical drive, which of its real top-level folders have been looked at, which haven't, and what the comparison found for the ones that have — and generating any report requires redoing the comparison computation from scratch every time.

The revised design splits this into three single-purpose commands:

- **`scan`** — walks and hashes one drive's files. Unchanged in spirit from today; stays completely unaware of any other drive or of pairing.
- **`compare`** — given two drives and specific paths to check, computes comparison status for files under those paths and **persists it to the checkpoint DB**. Explicitly scoped and invoked, not automatic.
- **`report`** — reads whatever comparison status is already persisted and renders it (HTML/JSON/etc.). Touches no drive, works fully offline, and can be re-run any number of times for free.

### 11.2 New Concept: `drive_root` vs `backup_root`

Unchanged from the prior draft, still needed for the same reason: `relative_path` needs an anchor stable across multiple incremental scans of the same drive (so `interview` today and `all photos from icloud...` tomorrow accumulate under one `drive_id` instead of conflicting per §4.1), but the two physical drives don't share a top-level layout (§1 — Seagate2's backup root is `Jyo/Backup`, Seagate1's is `Jyo`).

- **`drive_root`** — stable anchor for a drive's own relative paths, in practice its mount point (e.g. `/media/jyothri/Seagate1`). All of a `drive_id`'s `relative_path` values become relative to this, e.g. `Jyo/interview/Docs/common.txt`. Set once per `drive_id`; changing it is a conflict guarded the same way §4.1 guards today's single scan root, via the same `--replace-root` escape hatch.
- **`backup_root`** — a path relative to `drive_root` marking where the mirrored content actually starts (e.g. `Jyo/Backup` for Seagate2, `Jyo` for Seagate1). `compare` strips each drive's own `backup_root` prefix before aligning relative paths across drives for matching — implicit today (because `--path` already pointed straight at the backup root), made explicit so it can coexist with a broader `drive_root`.

### 11.3 Schema Changes

No `ALTER TABLE` migration path — per §11.8, existing checkpoint data is being discarded, so this ships as a clean `DROP TABLE` + `CREATE TABLE` of the full schema below, not a set of incremental alterations to the existing one.

```sql
DROP TABLE IF EXISTS drives;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS scan_runs;
DROP TABLE IF EXISTS dir_listings;
DROP TABLE IF EXISTS folder_status;

CREATE TABLE drives (
  drive_id               TEXT PRIMARY KEY,
  drive_root             TEXT NOT NULL,   -- stable anchor for this drive's relative paths, e.g. the mount point (§11.2)
  backup_root            TEXT,            -- relative to drive_root; NULL = no stripping (§11.2)
  last_scan_started_at   TIMESTAMP,
  last_scan_completed_at TIMESTAMP
);

CREATE TABLE files (
  drive_id                  TEXT NOT NULL,
  relative_path             TEXT NOT NULL,   -- relative to drives.drive_root
  size                      INTEGER NOT NULL,
  mtime_unix                INTEGER NOT NULL,
  mode                      INTEGER NOT NULL,
  quick_sig                 TEXT,
  content_hash              TEXT,
  hash_algo                 TEXT,
  status                    TEXT NOT NULL,   -- 'hashed' | 'error' (scan-time status, §4)
  error_message             TEXT,
  scanned_at                TIMESTAMP NOT NULL,
  comparison_status         TEXT,            -- NULL (never compared) | common | diverged | missing | relocated (§11.5)
  compared_against_drive_id TEXT,            -- which drive_id this status is relative to
  counterpart_relative_path TEXT,            -- only set for relocated (the matched path on the other drive)
  compared_at               TIMESTAMP,       -- when comparison_status was last (re)computed
  PRIMARY KEY (drive_id, relative_path)
);
CREATE INDEX idx_files_drive_hash ON files(drive_id, content_hash);
CREATE INDEX idx_files_drive_comparison_status ON files(drive_id, comparison_status);

CREATE TABLE scan_runs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  drive_id     TEXT NOT NULL,
  started_at   TIMESTAMP NOT NULL,
  finished_at  TIMESTAMP,
  files_seen   INTEGER,
  bytes_hashed INTEGER,
  interrupted  BOOLEAN
);

-- Every directory's known real children, at every depth ever touched by a scan — not
-- just top-level. This is what lets the tool know a folder is FULLY accounted for
-- (every real child has some status, even if that status is "unscanned") rather than
-- just "some of its files happen to be in the checkpoint DB."
CREATE TABLE dir_listings (
  drive_id      TEXT NOT NULL,
  relative_path TEXT NOT NULL,   -- the directory itself, relative to drive_root ('' for drive_root)
  child_name    TEXT NOT NULL,   -- immediate child's name only, not a full path
  is_dir        BOOLEAN NOT NULL,
  first_seen_at TIMESTAMP NOT NULL,
  last_seen_at  TIMESTAMP NOT NULL,
  PRIMARY KEY (drive_id, relative_path, child_name)
);

-- Persisted, incrementally-maintained rollup — one row per folder (at every depth),
-- with an explicit parent pointer so `compare` can walk upward without re-deriving paths.
CREATE TABLE folder_status (
  drive_id      TEXT NOT NULL,
  relative_path TEXT NOT NULL,   -- '' for drive_root itself
  parent_path   TEXT,            -- NULL only for drive_root; otherwise the immediate parent's relative_path
  status        TEXT NOT NULL,   -- unscanned | partial | common | diverged | relocated | missing
  counts_json   TEXT NOT NULL,   -- breakdown across the whole subtree, e.g. {"diverged":2,"missing":1,"common":47}
  updated_at    TIMESTAMP NOT NULL,
  PRIMARY KEY (drive_id, relative_path)
);
CREATE INDEX idx_folder_status_parent ON folder_status(drive_id, parent_path);
```

A folder with no `folder_status` row at all is implicitly `unscanned` (saves writing a row for every untouched folder) — `report` only needs to know the folder *exists* at all, via `dir_listings`, to display it as `unscanned`.

### 11.4 `scan`: Unchanged, and Deliberately Pairing-Agnostic

`scan` keeps doing exactly what it does today (walk, hash, checkpoint) plus populating `dir_listings`, in two parts:

1. **For every directory actually walked** while hashing `--path`'s contents: `filepath.WalkDir` already reads each directory's full immediate-children list as a side effect of descending into it — recording those names into `dir_listings` is free, no extra I/O.
2. **For every ancestor of `--path` up to (and including) `drive_root` that ISN'T itself walked** (because `--path` starts partway down, e.g. scanning `root/B` doesn't walk `root` itself): one extra cheap, non-recursive `os.ReadDir` at each such ancestor, recording just that one level's immediate children. This is what lets `report` later know that `root`'s real children are exactly `{A, B}` even though no single scan targeted `root` directly — closing the gap that caused the "lightweight" design (§ prior draft) to get a folder like this wrong.

`scan` does **not** know about the other drive in the pair, does not compute any comparison status, and does not write anything to `files.comparison_status` or `folder_status`. That's entirely `compare`'s job, invoked separately and explicitly. (This intentionally drops an idea floated earlier in this discussion — `scan` auto-triggering a cross-drive report update — since the explicit three-command split makes that unnecessary and keeps `scan` simple.)

### 11.5 `compare`: Scoped Comparison-Status Updates

```
driveagent compare --drive-a seagate2 --drive-a-paths interview,"all photos from icloud - Jul 28 2019" \
                    --drive-b seagate1 --drive-b-paths interview,"all photos from icloud - Jul 28 2019"
```

- `--drive-a-paths` / `--drive-b-paths` (new): comma-separated lists of `backup_root`-relative paths to (re)check on each side. In the common case these are the same names on both sides (once `backup_root` has already normalized away the `Jyo/Backup` vs `Jyo` prefix). Omit both to recompute the whole drive (a full pass) — the natural choice for the first-ever `compare` on a pair, or for a periodic full reconciliation.
- For files under the given paths: exact-path/content-hash matching exactly as §6 describes, writing `comparison_status` (`common`/`diverged`) directly onto each side's `files` row.
- **Relocated detection always runs globally**, regardless of `--paths` scoping — but entirely against the checkpoint DB, never the physical drives (every file's `content_hash` was already computed by `scan` beforehand, so this step is a SQL query + in-memory hash match, no disk I/O). "Globally" means the candidate pool isn't limited to files under this invocation's `--paths`: it's a multiset match over every row on both drives whose `comparison_status` is currently `missing` — from this invocation's newly-unmatched leftovers *and* from any earlier `compare` run. This was a deliberate choice — that subset is normally small even at whole-drive scale, so there's no real cost to keeping it exact rather than scoping it, and scoping it would risk silently missing a relocation whose other end sits outside this invocation's paths. Files that come out of this pass unmatched get/keep `comparison_status = missing`; matched ones get `relocated` on both sides, with `counterpart_relative_path` pointing at each other.
- Nothing outside the given paths is touched by the path-scoped matching step — its previously-computed `comparison_status` is left as-is unless the global relocated pass happens to touch it.
- `compare` writes to the DB and prints a text summary; it does **not** render HTML/JSON reports anymore — that's `report`'s job (§11.6). The `--include-mac-metadata` filter (§6.1) moves from `compare` to `report`, since it's a presentation-time decision, not a ground-truth one — `compare` computes and stores status for every file it's given regardless.

**Ancestor rollup propagation.** After the two steps above, `compare` has a concrete set of *touched files* — every file whose `comparison_status` was just written, whether from the path-scoped matching or the global relocated pass. For each touched file, its containing folder's rolled-up status is now potentially stale, and so is that folder's parent, and so on up to `drive_root`. `compare` fixes this by:

1. Collecting the **deduplicated set of ancestor folder paths** across all touched files (each touched file contributes its immediate parent, that parent's parent, … up to `drive_root`) — so a folder with a thousand touched children underneath it gets recomputed once per `compare` run, not once per child.
2. Processing that set **deepest-first**, so that by the time an ancestor is recomputed, all of its own children (files and subfolders) already reflect this run's updates.
3. For each ancestor `F` in that order, **recomputing `F`'s status and counts from its direct children only** (not the whole subtree — each child folder already carries its own correct, up-to-date rollup from having been processed earlier in this same pass, or from an earlier `compare` run if untouched this time):
   - Look up `F`'s real children via `dir_listings`.
   - Each **file** child's status comes from `files.comparison_status` (missing/NULL → `unscanned`).
   - Each **folder** child's status comes from its `folder_status` row (no row → `unscanned`).
   - If every child is `unscanned` → `F` is `unscanned`.
   - Else if any child is `unscanned` or `partial` → `F` is `partial`.
   - Else → `F`'s status is the worst-of `diverged` / `relocated` / `missing` / `common` across all children (§7.1's precedence), and its `counts_json` is the sum of every child's own breakdown (a file child contributes 1 to its own category; a folder child contributes its whole `counts_json`).
   - **Both `status` and `counts_json` are written unconditionally** — no early-stop when the status label is unchanged, so counts never go stale (this was a deliberate choice over a cheaper "stop if status matches" version, to keep the breakdown numbers always exactly right).
4. Propagation always reaches `drive_root` — there's no early termination, since counts must stay accurate at every level regardless of whether any label changed along the way.

Worked example, tying back to the discussion that produced this design: `root` has children `A` (already `common`) and `B` (`unscanned`, from `dir_listings`, no `folder_status` row yet) — `root`'s `folder_status.status` is `partial`. Scanning and then comparing `B` writes `common` for its files; `compare` recomputes `B`'s own `folder_status` to `common`, then walks up to `root`, recomputes it fresh from its children `{A: common, B: common}` → `common`, overwriting the stale `partial`. `report` reads `root`'s `folder_status` row directly — no recomputation, no staleness.

### 11.6 `report`: Pure Rendering, No Drive Access

```
driveagent report --type html --drives seagate1,seagate2 --report-out ./report
```

Reads only from the checkpoint DB — `folder_status` (already-correct status + counts at every depth), `dir_listings` (real folder/file names that exist but have no `folder_status`/`files` row, i.e. truly `unscanned`), and `files` (per-file leaf detail — hash/size/counterpart — shown when drilling all the way down to an individual file) — and renders the requested format(s). **No computation happens here at all**, only lookups: no drive needs to be mounted, and this can be re-run any number of times for free, entirely offline.

For the selected drive(s), the report includes:

- **Overview** (new): a flat, top-level-only list of that drive's real top-level entries (from `dir_listings` for `relative_path = ''`), each tagged with its `folder_status.status` (or `unscanned` if it has no `folder_status` row at all).
  - Clicking an entry drills into the existing §7.1-style recursive tree, rooted at that folder — now also a pure read of `folder_status` at each level down to individual files, rather than a fresh rollup computation.
- The existing **Files** recursive tree (§7.1) and **Relocated** flat list, now backed by `folder_status`/`files.comparison_status` instead of being computed inline by `compare` as they are today.
- With `--drives` naming more than one drive, the report includes an Overview per drive (e.g. tabs or a selector) — each drive's own native top-level names, since they genuinely differ.

### 11.7 Design Decisions Carried Over / Confirmed

- **Heavyweight persisted rollup, reconsidered and adopted.** An earlier draft chose "lightweight" (recompute fresh at report time, no persisted folder-level table) specifically to avoid the complexity of incremental maintenance given relocated-detection's global scope. A concrete example (§11.5's worked example) showed that approach gives the *wrong* answer for a case that comes up naturally — a folder whose children were scanned/compared separately still reading `partial` after every child actually resolves to `common`, because "lightweight" computed coverage from `scan_runs` history rather than from actual per-child status. The fix (§11.5's ancestor-propagation algorithm, keyed off *which files actually changed* rather than *which folders were in scope*) doesn't reopen the relocated-detection objection: relocated detection still always runs globally and unscoped (unchanged), it just now also feeds its results into the same "which files changed" set that drives propagation, so the two mechanisms compose cleanly.
- **Counts always propagate, status has no early-stop.** Every ancestor up to `drive_root` gets both fields rewritten unconditionally on every `compare` run that touches anything underneath it — a cheaper version that stopped propagating once a level's status label stopped changing was considered and rejected specifically because it would let `counts_json` drift stale at higher levels while the label still looked right.
- **`partial` is now genuinely content-based, not a coverage-history proxy.** It falls directly out of the recursive definition in §11.5 (any child `unscanned`/`partial`, but not all) rather than depending on whether one single scan invocation's target happened to match a folder exactly — the exact gap the worked example exposed is now closed by construction.
- **No persisted drive pairing.** Every `compare` invocation names both drives and their paths explicitly; nothing is remembered between runs. This was considered and dropped once the three-command split made `scan` fully pairing-agnostic — there's no longer a command that would need to look up a remembered pair automatically.
- **Relocated-in-rollup inconsistency, resolved (not just accepted).** An earlier draft flagged relocated files being counted in folder rollups for the Overview but excluded from folder rollups in the deep §7.1 tree as a deliberate but awkward inconsistency between the two views. Since both views now read the same `folder_status` rows, and `relocated` is one of the four tiers in §11.5's rollup precedence, the inconsistency is gone by construction — both views agree. The separate flat "Relocated" list (§7.1) is kept alongside this for showing the actual source↔destination path pairing, which genuinely has no single folder home, but a folder's badge now consistently reflects relocated content in both views.

### 11.8 Compatibility

No migration: `files.relative_path` changes meaning (drive-root-relative instead of scan-path-relative) and `comparison_status` is a new column, so existing checkpoint data (`seagate2`/`seagate1` from the `interview` trial, `seagate2-icloud`/`seagate1-icloud` from the icloud-photos trial) is incompatible and will simply be re-scanned from scratch once this ships. Confirmed acceptable by the user specifically to keep the schema simple.
