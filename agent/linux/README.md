# driveagent

Standalone, report-only tool that compares two drives for divergence. See
[`specs/drive-comparison-agent.md`](../../specs/drive-comparison-agent.md)
for the full design.

It never writes to either drive — output is a report (console/JSON/HTML)
for you to act on manually.

## Build

```bash
go build -o driveagent ./cmd/driveagent
```

## Usage

Scan each drive independently (resumable — safe to re-run after an
interruption, already-hashed unchanged files are skipped):

```bash
./driveagent scan --drive-id seagate2 --path /mnt/seagate2/Jyo/Backup
./driveagent scan --drive-id seagate1 --path /media/jyothri/Seagate1/Jyo
```

Then compare the two recorded scans:

```bash
./driveagent compare --drive-a seagate2 --drive-b seagate1 --report-out ./report
```

This writes `./report/report.json` and `./report/report.html`, and prints a
console summary. Open `report.html` in a browser to sort/filter results by
category (common / diverged / relocated / missing / scan errors).

By default the checkpoint database lives at `~/.driveagent/state.db`
(override with `--state-dir`), so it survives either drive being
unmounted or disconnected mid-scan.

Run `./driveagent --help` for the full flag list.
