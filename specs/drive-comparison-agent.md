# Drive Comparison Agent — Spec

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

## 7. Report Output

Generated at the end of the compare phase, three forms (all from the same underlying result set):

- **Console summary** — counts per category, top N diverged/missing examples, elapsed time.
- **JSON report** — full machine-readable listing of every file in every category (for scripting/manual repair later).
- **HTML report** — static, single-file, browsable table (sortable/filterable by category) for manual review — useful given this may run into the thousands of rows.

No automated repair action is taken by this version; the report is the deliverable, and any copying/deletion to fix divergences is a manual, separate step the user performs after reviewing the report.

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

## 9. Explicitly Out of Scope (v1)

- Any write/modify/delete action on either drive (no auto-repair, no auto-sync).
- Handling of non-regular files beyond logging (device files, sockets, FIFOs are skipped and logged, not compared).
- Cross-filesystem metadata like extended attributes, ACLs, or ownership — only path, size, mtime, and content are compared.
- Network/remote drives — this spec assumes both sources are locally mounted block devices.

## 10. Open Items for Future Iteration

- Guided repair mode (interactive copy-to-fix), previously deferred per user's choice of report-only for v1.
- Symlink comparison semantics (currently: skip and log).
- Scheduling periodic re-scans to catch drift over time.
