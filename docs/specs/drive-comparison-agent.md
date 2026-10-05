# Drive Comparison Agent

A standalone Go CLI (`agent/client/`, binary `driveagent`) that tracks and compares two drives for divergence: which files are identical, which have changed, which moved, which are missing from one side — and, per folder, how much of the drive has even been looked at. It never writes to either drive.

For the full design-decision history (alternatives considered, why they were rejected), see [`../archive/drive-comparison-agent-history.md`](../archive/drive-comparison-agent-history.md). This document describes only the current implementation.

## Why a separate scan root and backup root

Two drives meant to mirror each other rarely have identical top-level layouts — e.g. one drive's mirrored content lives at `/mnt/seagate2/Jyo/Backup`, the other's at `/media/jyothri/Seagate1/Jyo`, with unrelated folders (`Suneetha`, `$RECYCLE.BIN`, …) alongside on both. Two concepts, set per drive, handle this:

- **`drive_root`** — a stable anchor (in practice, the mount point) that every relative path for that drive is computed against. Set once; scanning a different subfolder later doesn't move it, so multiple incremental scans of the same drive accumulate instead of colliding.
- **`backup_root`** — a path relative to `drive_root` marking where the mirrored content actually starts (e.g. `Jyo/Backup` vs `Jyo`). `compare` strips each drive's own `backup_root` prefix before matching paths across drives, so the two drives' content lines up despite the differing layout above it.

Content outside `backup_root` (if any) is never compared — it's tracked (so it can be shown as a real, `unscanned` folder) but not matched against the other drive.

## Architecture: three single-purpose commands

| Command | Needs drive mounted? | Does |
|---|---|---|
| `scan` | Yes | Walks one drive, hashes new/changed files, records directory structure. Knows nothing about the other drive. |
| `compare` | No | Reads the checkpoint DB for both drives, classifies files, persists the result. Writes nothing to the drives. |
| `report` | No | Reads already-computed status and renders it. No computation, fully offline. |

All three share one local SQLite checkpoint database (default `~/.driveagent/state.db`, override with `--state-dir`). It's disposable — safe to delete and rebuild via `scan`.

### `scan`

```
driveagent scan --drive-id <id> --path <folder> [--drive-root <dir>] [--backup-root <rel-path>] [--workers N] [--replace-root] [--accept-identity-change] [--state-dir <dir>]
                [--remote-timeout 2m] [--remote-url <url>] [--lan-addr <host:port>]
```

- **Every scan uploads what it writes** to `agentserver` (from 0.4.0), and needs the remote and a login (`driveagent login`): if the server can't be reached, refuses this version, or there's no login, `scan` exits 3 (or 4 for an upgrade) before touching the drive or `state.db`. What an earlier scan recorded without uploading is left for `driveagent sync`; `scan` prints a hint when there is some. Details: [`remote-sync-agent.md`](remote-sync-agent.md#remote-is-required).

- `--path` is the specific folder walked *this* invocation; `--drive-root` (defaults to `--path`) is the stable anchor described above. `--backup-root` sets it once and is otherwise left alone on later scans.
- Each file is read once, for both its BLAKE3 hash (`content_hash`, what comparisons use) and, from 0.6.0, its MD5 (`files.md5`, uploaded so drives can be matched with Google Drive and Cloud Storage; [duplicates.md](../archive/duplicates.md#md5-in-driveagent)).
- Resumable: a file is re-hashed only if its size or mtime changed since last recorded, or it has no MD5 yet (a file hashed before 0.6.0, re-read once by the first 0.6.0 scan, keeping its comparison results); unchanged files are skipped. Safe to re-run after an interruption (Ctrl-C, drive disconnect, crash) with no special recovery step.
- Order: the instance and disk locks first (another scan reading the same disk exits 5, or with `--wait` is waited for; [agent hardening](../archive/agent-hardening.md)), then the drive's upload lock (waiting while a `driveagent sync` uploads that drive), then the remote preflight (health, handshake, login), then the drive checks, before anything is walked: the `--drive-root` check (and, with `--replace-root`, clearing the drive's old data), recording the drive (`scan.Prepare`), and the wrong-drive guard. Then the drive is opened on the server, the uploader starts, and the folder is walked and hashed (`scan.Run`) while the uploader sends what the scan writes; at the end it uploads the rest.
- **Wrong-drive guard** (from 0.2.0): `scan` reads the filesystem ID and disk serial of the drive holding `--drive-root` (on Linux from the partition's udev record; on macOS from `diskutil`/`ioreg`; read only, no root) and compares them with what's recorded for `--drive-id`. A different filesystem ID is refused (exit 1) before anything is walked: pass `--accept-identity-change` if the drive was reformatted, or `--replace-root` to start it over. A changed serial only warns (moving a disk to another USB dock changes it). Details: [`remote-sync-agent.md`](remote-sync-agent.md#drive-identity).
- Exit codes: 0 when the scan completed and the server acknowledged everything it wrote; 1 for a local error, including a drive that became inaccessible mid-scan; 2 for a usage error; 3 when the remote is unavailable (for `--remote-timeout`, default 2m, with no successful request), the login no longer works, or the upload fails, in which case the scan stops with its checkpoint intact; 4 when this `driveagent` needs upgrading; **130** after Ctrl-C (SIGINT) and **143** after SIGTERM. The first signal stops the walk (and the hashing of the current file), records what was seen and uploads it, then prints the resume hint; a second signal aborts that upload at once, with the same exit code. Before 0.2.0 an interrupted scan exited 0, or, for a Ctrl-C during the walk, printed `error: context canceled` and exited 1.
- Repointing an existing `--drive-id` at a different `--drive-root` is refused by default (it would silently change what every existing relative path means) — pass `--replace-root` to discard that drive's checkpoint data and start over.
- Records the real directory structure it observes (both from the recursive walk and cheap single-level reads of every ancestor between `--path` and `--drive-root`), so sibling folders that were never scanned still show up as real, named, `unscanned` folders rather than being invisible.
- **Deletion detection**: after a walk that hit zero unreadable files or directories, `scan` removes checkpoint rows for anything under `--path` that no longer exists on disk (including cascading into an entire deleted subdirectory). If *anything* was unreadable during the walk, deletion detection is skipped for that run entirely — better to leave a stale row than wrongly delete one hidden behind a permission error.
- Two `scan` processes (e.g. one per physical drive) can safely run at the same time against the same checkpoint DB. A write waits up to 60 s for the other's write lock (a big drive's end-of-scan listing sync holds it for many seconds), and a batch of results that still finds it locked is retried for up to 5 minutes. A batch that can't be recorded stops the scan (exit 1, the run marked interrupted) rather than being dropped: before 0.7.1 it was dropped and the scan carried on, so those files were hashed but never recorded or uploaded, until a re-run picked them up.

### `compare`

```
driveagent compare --drive-a <id> --drive-b <id> [--drive-a-paths <rel,rel,...>] [--drive-b-paths <rel,rel,...>] [--state-dir <dir>]
```

- `--drive-a-paths`/`--drive-b-paths` (backup-root-relative, comma-separated) scope which paths get (re)classified this run; omit both to recompute the whole drive.
- Classifies files by comparing content hashes at matching backup-root-relative paths: identical hash → `common`, same path different hash → `diverged`, present on only one side → `missing`.
- **Relocated detection always runs globally**, regardless of path scoping: it's a hash-based multiset match over every file currently marked `missing` on either drive (not a re-read of the drives — the hashes are already in the checkpoint DB from `scan`). Matches become `relocated` on both sides, each pointing at the other's path. This stays unscoped because a relocation's two ends can land outside whatever paths a given `compare` run was scoped to.
- Every file's comparison result is persisted (`comparison_status` and, for relocated files, the counterpart path) — `compare` does not render a report.
- After classifying, `compare` recomputes the rolled-up status for every ancestor folder of every file it touched, up to the drive root, and persists that too (see below). This keeps `report` a pure lookup.

### `report`

```
driveagent report --drives <id,id,...> [--type text,json,html] [--report-out <dir>] [--include-mac-metadata] [--state-dir <dir>]
```

- Pure reader — no drive access, no computation. Renders one section per requested drive.
- HTML output is a collapsible tree per drive (folders collapsed by default; click to expand), rooted at `.`, with a flat list of relocated files alongside it.
- `--include-mac-metadata` (default off) controls whether AppleDouble (`._*`) and `.DS_Store` files appear as visible leaves in the tree. This is a display-only filter — those files are still scanned, hashed, and classified normally; only their presence in the *rendered* folder listing (not the persisted rollup counts) is affected.

## Folder status rollup

Every folder (at every depth, not just the top level) has a persisted status, computed bottom-up from its direct children:

- **`unscanned`** — nothing under this folder has ever been checkpointed.
- **`partial`** — some children are resolved, but at least one isn't (`unscanned` or itself `partial`).
- Otherwise, the folder's status is the worst of its children's statuses, in this order (worst first): **`diverged` > `relocated` > `missing` > `common`**.

A folder tagged `partial` or non-`common` shows a breakdown of counts across its whole subtree (e.g. `diverged (2 diverged, 1 missing, 47 common)`) and can be expanded to see exactly which descendants caused it.

## Out of scope

- No writes to either drive — no auto-repair, no auto-sync. Divergences are reported for the user to act on manually.
- Symlinks, devices, sockets, and FIFOs are skipped and logged, not compared.
- No extended attributes, ACLs, ownership, or network/remote drive support — only path, size, mtime, and content are considered.
- No persisted pairing between drives — every `compare` invocation names both drives explicitly.
