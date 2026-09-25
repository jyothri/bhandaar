# driveagent

Standalone tool that tracks and compares two drives for divergence. See
[`docs/specs/drive-comparison-agent.md`](../../docs/specs/drive-comparison-agent.md)
for the full design.

It never writes to either drive. Three subcommands, each with one job:

- `scan` walks and hashes one drive's files (the only command that needs the drive mounted).
- `compare` computes and persists comparison status for scoped paths (reads the checkpoint DB, no drive access).
- `report` renders already-computed status as console/JSON/HTML — no computation, no drive access, fully offline.

## Build

```bash
go build -o driveagent ./cmd/driveagent
```

## Usage

Scan each drive independently (resumable — safe to re-run after an
interruption, already-hashed unchanged files are skipped). `--drive-root`
is the stable anchor for a drive's relative paths (in practice its mount
point) — set it once; later scans of other subfolders on the same drive
accumulate instead of conflicting. `--backup-root` (relative to
`--drive-root`) marks where the mirrored content actually starts, so two
drives with different top-level layouts still align for comparison:

```bash
./driveagent scan --drive-id seagate2 --drive-root /mnt/seagate2 --backup-root Jyo/Backup --path "/mnt/seagate2/Jyo/Backup/interview"
./driveagent scan --drive-id seagate1 --drive-root /media/jyothri/Seagate1 --backup-root Jyo --path "/media/jyothri/Seagate1/Jyo/interview"
```

Then compare the two drives — `--drive-a-paths`/`--drive-b-paths` (both
backup-root-relative, comma-separated) scope which paths get (re)checked
this run; omit both to recompute the whole drive:

```bash
./driveagent compare --drive-a seagate2 --drive-b seagate1 --drive-a-paths interview --drive-b-paths interview
```

Then render a report whenever you want — this step touches only the
checkpoint DB, so it works with neither drive plugged in:

```bash
./driveagent report --drives seagate1,seagate2 --report-out ./report
```

This writes `./report/report.json` and `./report/report.html`, and prints
a console summary. Open `report.html` in a browser — it has a tab per
drive, each showing that drive's own real folder structure (including
folders never scanned, tagged `unscanned`, and folders only partially
covered, tagged `partial`) as a collapsible tree, plus a flat list of
relocated files.

By default the checkpoint database lives at `~/.driveagent/state.db`
(override with `--state-dir`), so it survives either drive being
unmounted or disconnected mid-scan. Two `scan` processes for different
drives can safely share the same checkpoint DB at the same time.

Run `./driveagent --help` for the full flag list.
