# Remote Sync: `driveagent` Client

**Status:** implemented (0.4.1; the locks and machine binding of [agent hardening](../archive/agent-hardening.md) in 0.5.0). This is the compact as-built reference for `agent/client/`. The original spec, with its rationale, measurements and test list, is in [`archive/remote-sync/remote-sync-agent.md`](../archive/remote-sync/remote-sync-agent.md). The overview is [`remote-sync.md`](remote-sync.md), the server API [`remote-sync-server.md`](remote-sync-server.md).

## Packages

```
agent/client/internal/
  version/   Version (bumped per release), Commit (stamped by CI), Protocols ([]int{1})
  remote/    HTTP client (health, handshake, auth, drives, changes), the LAN-first dialer, error classification
  creds/     agent.json and credentials.json, the token Session (refresh under a file lock)
  identity/  drive identity (Linux udev/mountinfo, macOS diskutil/ioreg) and the wrong-drive guard
  store/     state.db: the change feed, the synced marker's tables and primitives
  syncer/    the feed reader, marker rules, batches, the uploader, the upload lock
```

`internal/scan` doesn't import `syncer`; `cmd/driveagent` wires them together. Dependencies added for remote sync: `golang.org/x/term`, `github.com/gofrs/flock`, `github.com/google/uuid`, and `agent/wire` through `replace … => ../wire`.

### Platforms

Releases are built for `linux/amd64`, `darwin/amd64` and `darwin/arm64`, all with `CGO_ENABLED=0`. On macOS the state dir is still `~/.driveagent/`, drives mount under `/Volumes/<name>`, and the binaries are unsigned (`curl` downloads run as-is; a browser download needs `xattr -d com.apple.quarantine driveagent` once).

## What each command uploads

A drive's pending entries are of two kinds:
- **Current scan data:** what the running `scan` writes, i.e. every entry of the drive above the scan's start version `S` ([Upload during `scan`](#upload-during-scan)).
- **History:** everything else pending: rows from scans whose upload didn't finish, data from before remote sync (the migration made it all pending), anything the server lost.

| Command | Uploads |
|---|---|
| `scan` | Current scan data only, always; the command fails if the upload does. At startup it prints `note: N drive(s) have history not yet uploaded; run "driveagent sync"` when there is some |
| `sync` | History, for every drive (or `--drive-id a,b`), with no drive access |
| `compare`, `report`, `version` | Nothing; they never contact the server |

A file unchanged since a scan that never uploaded isn't in a later scan's data (unchanged files are skipped), so it reaches the server only through `sync`.

## Remote is required

`scan` runs in this order:

0. **Join the instance, handshake, and take the disk's lock**:
   - The instance ([agent hardening](../archive/agent-hardening.md)): another state dir or version running exits 5.
   - Health (5 s timeout, 2 retries 1 s apart), then the handshake. If the server names a newer version, driveagent [updates itself](agent-auto-update.md) and runs the scan again on the new binary. Failure exits 3, or 4 for an upgrade. This comes before the disk lock (from 0.7.0), so a scan with the server unreachable fails at once, rather than after waiting for a busy disk.
   - The disk's lock: another scan reading the same disk exits 5, unless `--wait`.
1. **Take the drive's [upload lock](#upload-lock)**, waiting (with a message) while a `sync` holds it.
2. **The token**, before the drive or `state.db` is touched. Failure exits 3.
3. **Drive checks** (`scan.Prepare`): the `--drive-root` check (`--replace-root` clears the drive here, so it gets a new stream before anything is uploaded), recording the drive, then the [wrong-drive guard](#wrong-drive-guard).
4. **Read `S`**, open the drive (`PUT`) and [reconcile](#reconciling); work out where the session starts.
5. **Scan and upload side by side.** The uploader is woken after each of the scan's flushes, and by a 2 s tick. A slow remote never slows the scan (the queue is `state.db` itself).
6. **Drain** what's left after the scan. Exit 0 only once the server has acknowledged everything the scan wrote.

A transient failure is retried with backoff (1 s, doubling to 30 s, ±25% jitter, honouring `Retry-After`) until `--remote-timeout` (default `2m`) has passed since the current run of failures began; idle time doesn't count. Then the scan stops, its checkpoint intact, with exit 3 and a message pointing at re-running the scan and `driveagent sync`. A permanent failure (426, a refused login, `400 INVALID_BATCH`) stops it at once.

**Signals:** the first Ctrl-C (or `SIGTERM`) stops the walk, then drains what the scan wrote under the same timeout, and exits 130 (143). A second signal aborts the drain, with the same exit code. What a failed or interrupted scan didn't upload becomes history, for `sync`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success: the server acknowledged everything the command wrote |
| 1 | Local error (including a drive that went away mid-scan) |
| 2 | Usage error |
| 3 | Remote unavailable, login needed, or upload failed |
| 4 | This `driveagent` needs upgrading, and couldn't [update itself](agent-auto-update.md) |
| 5 | Busy: another `driveagent` runs with another state dir or version, or scans the same disk ([agent hardening](../archive/agent-hardening.md)) |
| 130 / 143 | Interrupted by `SIGINT` / `SIGTERM` |

## Configuration

Precedence: flag > environment > `<state-dir>/config.json` > default.

| Setting | Flag | Env | Default |
|---|---|---|---|
| Remote URL | `--remote-url` | `DRIVEAGENT_REMOTE_URL` | `https://sm.jkurapati.com` |
| LAN address to try first | `--lan-addr` | `DRIVEAGENT_LAN_ADDR` | none |
| Give-up budget for a failing remote | `--remote-timeout` (`scan`, `sync`) | | `2m` |
| No automatic updates | | `DRIVEAGENT_NO_AUTO_UPDATE` | updates on |
| Where releases come from | | `DRIVEAGENT_UPDATE_URL` | `https://github.com/jyothri/bhandaar/releases/download` |

```json
{"remote_url": "https://sm.jkurapati.com", "lan_addr": "192.168.1.118:443"}
```

`http://` is refused except for `localhost` and `127.0.0.1` (a local `agentserver` on port 8091).

### Reaching the server from the LAN

From the home LAN, `sm.jkurapati.com` resolves to the public IP, and the NAT hairpin through the gateway is unreliable (4 of 7 requests timed out when measured). With `lan_addr` set, each new connection first dials that address (1 s timeout) and does TLS **for the remote URL's host name**, verifying the certificate as usual. If the connect or the handshake fails, it falls back to DNS, and skips `lan_addr` for 5 minutes. Nothing is sent to a host whose certificate doesn't verify. `remote-status` shows the path used: `reachable via LAN 192.168.1.118:443` or `via DNS <public IP>:443`. On the prod box itself, `127.0.0.1:443` works.

## Identity and credentials

In the state dir, mode 0600:
- `agent.json`: `{"agent_id": "<uuid v4>", "machine_id": "…"}`, created on first use; one state dir is one agent, on one machine. On another machine (`/etc/machine-id` or `IOPlatformUUID` differs), every command that talks to the server refuses, until `login --new-agent` ([machine binding](../archive/agent-hardening.md#goal-1-machine-binding-follow-up-2)).
- `credentials.json`: remote URL, username, access and refresh tokens with their expiries, written atomically. Tokens are bound to the remote URL; another URL means not logged in.

The access token is refreshed when it has under 60 s left, or after a `401 TOKEN_EXPIRED`/`INVALID_TOKEN` (once per request; a second 401 means `driveagent login`). Refresh runs under an exclusive `flock` on `credentials.lock`, re-reading `credentials.json` first, so concurrent processes rotate once. A lost refresh response is covered by the server's 30 s grace.

A state dir syncs to exactly one remote, and must never be copied to another machine and used on both ([operational rules](remote-sync.md#operational-rules)); from 0.5.0 the agent notices.

## Drive identity

At the start of every scan, the agent reads the identity of the drive holding `--drive-root`, without writing to it and without root:

| | Linux | macOS |
|---|---|---|
| Filesystem ID (`fs_uuid`), type (`fs_type`), source (`linux`/`macos`) | `stat(root)` gives the device number; the partition's udev record `/run/udev/data/b<maj>:<min>` gives `ID_FS_UUID` and `ID_FS_TYPE`. Without udev (a container), `/proc/self/mountinfo` on the same device number gives the type only, normalised (`ntfs3`, `fuseblk` → `ntfs`) | `diskutil info -plist <root>`: `VolumeUUID`, `FilesystemType` |
| Disk serial (`hw_serial`) | `ID_SERIAL_SHORT` from the same udev record | `ioreg -a -l -r -c IOUSBHostDevice`: the USB device whose subtree holds the disk's `BSD Name` (`-l` is needed; 0.4.0 lacked it) |

IDs are normalised (uppercase, no dashes); generic serials on a denylist count as missing; either identifier can be missing. It's stored in the `drives` table and sent with every `PUT /drives/{id}` (`sync` sends the stored values).

**Across Linux and macOS**, ext4, APFS and HFS+ report the same ID. FAT, exFAT and NTFS don't: macOS's `VolumeUUID` isn't the volume serial, and for NTFS it can be missing (seen with seagate2; seagate1's is a GUID, the same on two Macs with different NTFS drivers). So those drives link only between agents on the same OS ([proposed fix](remote-sync-cross-os-linking.md)); a drive with no filesystem ID isn't matched at all.

### Wrong-drive guard

Before walking, `scan` compares what it found with what's stored for the `--drive-id`:

| Stored vs found | Result |
|---|---|
| Both have a filesystem ID from the same source, and they differ | **Refuse**, exit 1: `… Wrong drive? If it was reformatted, re-run with --accept-identity-change` |
| Same filesystem ID, different serial | Warn, record the new serial (docks report their own serials) |
| Nothing stored yet | Record |
| Stored, but not found now | Warn, keep the stored identity |

`--accept-identity-change` records the new identity and keeps the checkpoint; `--replace-root` starts the drive over instead.

## Commands

```
driveagent login          [--username <name>] [--password-stdin] [--new-agent] [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
driveagent logout         [--state-dir <dir>]
driveagent sync           [--drive-id <id,id,...>] [--remote-timeout 2m] [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
driveagent remote-status  [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
driveagent update         [--check] [--version <V>] [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
driveagent version
```

- **`login`**: preflight (updating driveagent first, as `scan` does, unless `--new-agent`), then username and password (no echo), then `/auth/login`. `--password-stdin` for scripts; there's no password flag or environment variable. `--new-agent` first makes the state dir a new agent on this machine: every drive's stream and marker are forgotten, the old login is deleted without revoking it, and `agent.json` gets a new id; it needs the state dir to itself.
- **`logout`**: revokes on the server (best effort) and deletes `credentials.json`.
- **`update`**: updates driveagent now, to the server's latest version or `--version`'s, without re-running anything; `--check` only says whether one is available ([agent-auto-update.md](agent-auto-update.md#commands)).
- **`remote-status`**: reachability and path, the handshake, what an update would do (it never updates), the login, and per drive its root, identity, the synced marker, pending count (from the `sync_summary` view), the server's acked ranges, linked copies on other machines, and up to 10 rejected entries. It reconciles a drive only if its upload lock is free (otherwise it shows the server's list), and never opens a drive that was never uploaded.
- `scan` also takes `--remote-url`, `--lan-addr`, `--remote-timeout`, `--accept-identity-change` and `--wait`.

### `driveagent sync`

1. Preflight (health, handshake, token), updating driveagent first if the server names a newer version, before `state.db` is opened. Nothing is uploaded unless it succeeds; failure exits 3 or 4.
2. Per drive, in `drive_id` order: try the upload lock (a drive a scan holds is skipped, with a note); open and reconcile; upload each [gap](#gaps) up to the clock as it stood after opening, oldest first (an empty gap is closed with an empty batch); release.
3. One line per drive (`seagate2: uploaded 18,532 changes, fully synced`, or `… N still pending`), a running total every 5 s. It stops at the first failure, naming the drives not attempted, and exits 3 (4) or 1 for a local error.

### Upload during `scan`

After the drive checks and before `scan.Run`, the agent reads `S = sync_clock.v`. One scan runs per drive at a time and versions commit in order, so every entry of the drive above `S` (the scan-run row included) is the scan's own. The session covers `(from, ∞)`: let `e` be the end of the highest acked range at or below `S`; `from = e` if the drive has no entry in `(e, S]` (one `EXISTS`), so the session continues that range, otherwise `from = S`, leaving the history below for `sync`. A history entry the scan supersedes gets a new version above `S`, so it's uploaded as current data. After a re-open (404/409), the session continues from its cursor if the server still covers `(from, cursor]`, otherwise it restarts at `from`.

## Change feed in `state.db`

Schema version 2 (a `schema_version` table tracks Go migrations after the original `CREATE … IF NOT EXISTS` list). Version 2 (0.6.0) adds `files.md5`; a driveagent before 0.6.0 refuses a state.db at version 2. Version 1 added:
- `sync_clock (id = 1, v)`: the global version clock.
- `drives`: `sync_stream_id` (NULL until first uploaded), `synced_version` (the watermark), `synced_at`, and the identity columns `fs_uuid`, `fs_type`, `fs_uuid_source`, `hw_serial`, `identity_seen_at`.
- `row_version` on `files`, `dir_listings`, `scan_runs`, with `(drive_id, row_version)` indexes.
- `sync_ranges (drive_id, from_version, to_version)`: synced ranges above the watermark.
- `sync_tombstones (drive_id, kind, relative_path, child_name, row_version)`: deletions.
- `sync_rejected (drive_id, row_version, kind, relative_path, child_name, reason, rejected_at)`.
- Views for inspection by hand: `sync_feed_status` (every entry with a `synced` flag) and `sync_summary` (per drive: watermark, ranges, pending). `sync_summary` reads every entry of a drive, so only explicit status commands use it.

The migration backfilled every existing row (in `rowid` order, printing progress), so existing checkpoints became history for the first `sync`.

**Versions.** Each write transaction reserves versions with `UPDATE sync_clock SET v = v + n RETURNING v`. Every feed writer starts with `BEGIN IMMEDIATE` (`_txlock=immediate`), because `SyncDirListings` reads before it writes, and a deferred transaction upgrading to a writer after another process committed would fail with `SQLITE_BUSY_SNAPSHOT`. With one writer at a time, versions become visible in commit order, which is what makes a batch's coverage claim and `S` sound, across processes too.

| Writer | Feed effect |
|---|---|
| `UpsertFiles` | A new version per row (new or changed files, and files still in `error`); removes the key's tombstone |
| `DeleteFiles` | Deletes the row; a `file` tombstone at a new version |
| `SyncDirListings` | A new version on insert or an `is_dir` change, **not** on a `last_seen_at` touch; a `dir_child` tombstone per removed row (stale deletes and `cascadePurge`) |
| `StartScanRun`, `FinishScanRun` | A new version on the `scan_runs` row |
| `ClearDrive` (`--replace-root`) | One transaction: deletes the drive's rows, tombstones, ranges and rejected entries, and clears its stream and marker, so the next upload starts a new stream |
| `UpdateComparisonStatuses`, `UpsertFolderStatus` | None; compare data isn't uploaded |

Each key is in the feed at most once, at its latest version, as a row or a tombstone, so history and current data can be uploaded in any order.

## Synced marker

An entry is **synced** if its version is at or below the drive's **watermark** (`drives.synced_version`) or inside one of its `sync_ranges`; otherwise it's pending. The marker is a **copy of the server's acked ranges** as of the last acknowledgement, never merged locally, and it moves only after an acknowledgement. Every marker write is conditioned on the drive's stream id, so a concurrent `ClearDrive` can't be overwritten.

**Recording an acknowledgement**, in one transaction:
1. The answer's ranges must still cover the old marker and the batch's `(from, to]`. If not, the server went back in time mid-session: the marker is left alone, and the drive is re-opened, which starts a new stream.
2. The marker becomes the answer's ranges (the one from 0 is the watermark).
3. Rejected entries are recorded ([below](#rejected-entries)).
4. `synced_at` is set, and synced tombstones are pruned.

### Gaps

A gap is an interval the marker doesn't cover: between the watermark or a range's end and the next range's start, or the clock after the last range. `sync` uploads gaps oldest first. The last batch of a gap is sent with `to_version` = the gap's end (so the ranges meet and merge on the server), and a gap with no pending entries gets one empty batch; a batch never has `from = to`.

### Reconciling

Whenever a drive is opened (`PUT /drives/{id}`), the answer is compared with the local marker and the local clock:

| Check | Meaning | Action |
|---|---|---|
| `reset: true` | The server started the stream over | Clear the marker and `sync_rejected` |
| The server's highest acked version is above the local clock | `state.db` was restored from an older copy | **New stream**: a new `sync_stream_id`, marker and `sync_rejected` cleared, `PUT` again; the drive is re-uploaded |
| The server's ranges don't cover the local marker | The server was restored (its lost deletions can't be re-sent, since synced tombstones are pruned) | **New stream**, as above |
| The server covers more, within the local clock | A crash between the server's commit and the local update | Adopt the server's ranges |
| Same ranges | Normal | Nothing |

A drive's first upload creates its stream id lazily. Resuming needs nothing else: after an interruption, everything past the last acknowledged batch is pending, and at most the batch in flight is sent again.

## Uploader

### Upload lock

An exclusive `flock` on `<state-dir>/upload-<sha256(drive_id)[:16]>.lock` while uploading a drive. `scan` takes it (after the instance and disk locks of [agent hardening](../archive/agent-hardening.md)) and waits for it; `sync` tries it and skips a busy drive. It keeps a scan and a `sync` from interleaving writes to one drive's marker, and two scans of one drive id apart even on different disks; two scans of one disk are kept apart by the disk lock. Correctness of the uploaded data doesn't depend on it.

### Reading the feed

A separate read-only connection (`mode=ro`), so reads never block the store's writer (WAL). A page of `(cursor, upper]` is read in one read transaction: one query per table (`files`, `dir_listings`, `scan_runs`, `sync_tombstones`) on the `(drive_id, row_version)` indexes, merged by version, up to the page size.

### Batches

1. A page of up to `min(1000, limits.max_changes_per_batch)` entries, cut once the JSON (before gzip) would pass `limits.max_batch_bytes` (at least one entry).
2. `from_version` = the cursor; `to_version` = the last entry's version, or the gap's end when a `sync` page reaches it.
3. `Idempotency-Key = hex(sha256(agent_id|drive_id|stream_id|from|to))`.
4. Encoded and gzipped **once**: every retry sends the same bytes. After a restart the page is re-read, which is safe because the server checks coverage before the key.
5. On success, record the acknowledgement and move the cursor to `to_version`.

Responses: `409 STREAM_MISMATCH` or `404 DRIVE_NOT_OPEN` re-open the drive (up to 3 times per run); `413` (nginx's HTML one too) halves the page size, down to 1; everything else follows the [server's classification](remote-sync-server.md#status-and-error-codes), by HTTP status first.

### Rejected entries

The server acknowledges a batch's range even if some entries fail its validation, listing them as `rejected`. The agent records them in `sync_rejected` and `remote-status` shows them; they count as synced. A row is deleted once the same key is stored at a newer version, and the table is cleared with a new stream or `ClearDrive`. The server keeps a rejected key as missing (a tombstone), not as its stale older row.

## Wire mapping

| Local | Wire (`agent/wire`) |
|---|---|
| `files` row | `{"kind":"file","op":"upsert","v","path","size","mtime_unix","mode","content_hash","hash_algo","md5","status","error_message","scanned_at"}`; `md5` (lowercase hex) from 0.6.0, omitted while a file has none |
| `file` tombstone | `{"kind":"file","op":"delete","v","path"}` |
| `dir_listings` row | `{"kind":"dir_child","op":"upsert","v","path","child","is_dir","first_seen_at"}` |
| `dir_child` tombstone | `{"kind":"dir_child","op":"delete","v","path","child"}` |
| `scan_runs` row | `{"kind":"scan_run","op":"upsert","v","run_id","started_at","finished_at","files_seen","bytes_hashed","interrupted"}` |

Names that aren't valid UTF-8 go as `path_b64`/`child_b64` (standard base64 of the bytes). Timestamps are UTC. The golden fixtures in `agent/wire/testdata/` are the contract; the agent's tests check the feed maps equivalent rows to them exactly.
