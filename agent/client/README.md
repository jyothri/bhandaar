# driveagent

Standalone tool that tracks and compares two drives for divergence. See
[`docs/specs/drive-comparison-agent.md`](../../docs/specs/drive-comparison-agent.md)
for the full design.

It never writes to either drive. Three subcommands, each with one job:

- `scan` walks and hashes one drive's files (the only command that needs the drive mounted).
- `compare` computes and persists comparison status for scoped paths (reads the checkpoint DB, no drive access).
- `report` renders already-computed status as console/JSON/HTML — no computation, no drive access, fully offline.

## Install

Releases are built for Linux (amd64) and macOS (Intel and Apple Silicon) and
published as GitHub Releases tagged `driveagent/v<version>`:

```bash
# Linux amd64 (use darwin_arm64 for Apple Silicon, darwin_amd64 for Intel Macs)
asset=driveagent_linux_amd64.tar.gz
curl -fsSLO "https://github.com/jyothri/bhandaar/releases/latest/download/$asset"
curl -fsSLO "https://github.com/jyothri/bhandaar/releases/latest/download/SHA256SUMS"
sha256sum --check --ignore-missing SHA256SUMS      # macOS: shasum -a 256 --check --ignore-missing SHA256SUMS
tar -xzf "$asset" driveagent && install -m 0755 driveagent ~/.local/bin/
driveagent version
```

**macOS:** the binaries aren't signed or notarized. Downloaded with `curl` as
above they run as-is; downloaded through a browser, Gatekeeper blocks them
until you run `xattr -d com.apple.quarantine driveagent` once.

After upgrading, check `driveagent version` and remove older copies from your
`PATH`: from the release that adds remote sync's change feed on, an older
binary must not run against the upgraded `state.db`.

## Build

```bash
go build -o driveagent ./cmd/driveagent
go test ./...                               # tests
./scripts/version-check_test.sh             # the release version check
```

Any change to the agent (or to `agent/wire`) must bump `Version` in
`internal/version/version.go`; CI checks this, and merging to `main`
releases that version. Changes to Markdown, tests, `testdata/` and `scripts/`
don't need a bump.

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

A state dir belongs to **one machine**: never copy `~/.driveagent` to another
machine (Migration Assistant, `rsync` to a new box) and keep using both. Once
scans upload to the server, both copies would share one agent identity and
keep wiping each other's upload. Give each machine its own state dir, created
by its own `driveagent`. See the remote-sync overview's
[operational rules](../../docs/specs/remote-sync.md#operational-rules).

## The remote server

`driveagent` can log in to the Bhandaar server (`agentserver`, see
[`docs/specs/remote-sync.md`](../../docs/specs/remote-sync.md)), so that later
releases can upload scans. For now, scans stay local; logging in only prepares
for that.

```bash
driveagent login --username jyothri          # prompts for the password (no echo)
driveagent remote-status                     # reachability, the path used, handshake, login
driveagent logout
```

For scripts, `--password-stdin` reads the password from standard input (as
`docker login` does). There's deliberately no `--password` flag and no password
environment variable. The login is stored in `<state-dir>/credentials.json`
(mode 0600) and renews itself; `agent.json` holds this machine's agent id.

Settings, highest precedence first: flags, then `DRIVEAGENT_REMOTE_URL` /
`DRIVEAGENT_LAN_ADDR`, then `<state-dir>/config.json`, then the default
(`https://sm.jkurapati.com`):

```json
{"remote_url": "https://sm.jkurapati.com", "lan_addr": "192.168.1.118:443"}
```

`lan_addr` is for machines on the home LAN, where the public name only works
through the router's unreliable NAT hairpin. The agent connects to that address
first, still verifying the certificate for the server's name, and falls back to
DNS when it doesn't answer (away from home it's skipped for 5 minutes after a
miss). On the server box itself use `127.0.0.1:443`. `remote-status` shows
which path was used.

A state dir logs in to, and will sync with, exactly one server. To try another
server, use a copy of the state dir (`--state-dir`).

Exit codes: 0 ok, 1 local error, 2 usage, 3 server unreachable or login
needed, 4 this `driveagent` is too old for the server (upgrade).

Run `./driveagent --help` for the full flag list.
