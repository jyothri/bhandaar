# Drive Comparison Agent

A standalone Go CLI (`agent/linux/`, binary `driveagent`) that tracks and compares two drives for divergence: which files are identical, which have changed, which moved, which are missing from one side — and, per folder, how much of the drive has even been looked at. It never writes to either drive.

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
driveagent scan --drive-id <id> --path <folder> [--drive-root <dir>] [--backup-root <rel-path>] [--workers N] [--replace-root] [--state-dir <dir>]
```

- `--path` is the specific folder walked *this* invocation; `--drive-root` (defaults to `--path`) is the stable anchor described above. `--backup-root` sets it once and is otherwise left alone on later scans.
- Resumable: a file is re-hashed only if its size or mtime changed since last recorded; unchanged files are skipped. Safe to re-run after an interruption (Ctrl-C, drive disconnect, crash) with no special recovery step.
- Repointing an existing `--drive-id` at a different `--drive-root` is refused by default (it would silently change what every existing relative path means) — pass `--replace-root` to discard that drive's checkpoint data and start over.
- Records the real directory structure it observes (both from the recursive walk and cheap single-level reads of every ancestor between `--path` and `--drive-root`), so sibling folders that were never scanned still show up as real, named, `unscanned` folders rather than being invisible.
- **Deletion detection**: after a walk that hit zero unreadable files or directories, `scan` removes checkpoint rows for anything under `--path` that no longer exists on disk (including cascading into an entire deleted subdirectory). If *anything* was unreadable during the walk, deletion detection is skipped for that run entirely — better to leave a stale row than wrongly delete one hidden behind a permission error.
- Two `scan` processes (e.g. one per physical drive) can safely run at the same time against the same checkpoint DB.

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
