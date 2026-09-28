# Remote Sync: driveagent → sm.jkurapati.com

**Status:** implemented (0.4.1, 2026-09-27). This is the compact as-built reference. The original design, with the options weighed and the rollout, is in [`archive/remote-sync/`](../archive/remote-sync/remote-sync.md); open work is in [`remote-sync-followups.md`](remote-sync-followups.md).

`driveagent` ([`drive-comparison-agent.md`](drive-comparison-agent.md)) uploads its scan data to `agentserver` at `sm.jkurapati.com`, so a drive's contents are known centrally and not only in one machine's `~/.driveagent/state.db`.

| Document | Covers |
|---|---|
| this file | What it does, the rules for running it, the key decisions, the flow |
| [`remote-sync-agent.md`](remote-sync-agent.md) | `driveagent`: commands, configuration, identity, the change feed and synced marker, the uploader |
| [`remote-sync-server.md`](remote-sync-server.md) | `agentserver`: API, auth, schema, the apply algorithm, matching physical drives, housekeeping, deployment |
| [`remote-sync-ci.md`](remote-sync-ci.md) | The `agentserver` image and the `driveagent` releases |

## What it does

- **Every `driveagent scan` uploads what it writes**: new or changed files, deletions, listing changes and its scan-run row. The remote is required: a scan fails (exit 3, or 4 for an upgrade) if it can't upload.
- **`driveagent sync` uploads history**: everything recorded but not yet uploaded (from a scan that failed or was interrupted mid-upload, or from before remote sync existed). It needs no drive attached.
- Before anything is uploaded, the agent checks health, negotiates the version (handshake), then uses its login (access and refresh tokens).
- `state.db` records per drive what's on the server (the **synced marker**), advanced only after each acknowledged batch, so every run resumes after an interruption. Uploads are idempotent.
- `compare`, `report` and `version` never contact the server.

## Non-goals (v1)

- Uploading `compare` results or folder rollups: only scan data (drives, files, listings, scan runs).
- Agent data in the web UI, and server-side compare ([follow-ups](remote-sync-followups.md)).
- Self-service sign-up: an admin creates users with `agentserver user add`.
- History on the server: it mirrors each drive's *current* checkpoint, with no per-scan snapshots.
- Multi-writer drives: one agent (one `state.db`) owns a drive's stream. Copies of one physical drive from several machines are **linked**, not merged.
- Scanning without the remote (no offline or best-effort mode).
- Signed or notarized macOS binaries: `curl` installs run as-is, and a browser download needs `xattr -d com.apple.quarantine` once.

## Operational rules

1. **Never copy a state dir (`~/.driveagent`) to another machine and keep using both.** The copies share the `agent_id` and the stream ids, and reconciling makes them wipe each other's upload on the server, one full re-upload per alternation. Each machine gets its own state dir, and a state dir syncs to exactly one remote. From 0.5.0 the agent enforces this: `agent.json` records its machine, and on another machine the remote commands refuse until `driveagent login --new-agent` ([agent hardening](../archive/agent-hardening.md#goal-1-machine-binding-follow-up-2)).
2. **Never run a `driveagent` older than 0.2.0 on a migrated `state.db`.** It doesn't version its writes, so they're never uploaded, and nothing reports it. After upgrading, check `driveagent version` and remove old binaries from `PATH`. The agent doesn't detect this yet ([follow-up](remote-sync-followups.md)). From 0.5.0, two versions can't run at once on one machine, but that doesn't cover a binary older than 0.5.0.

## Decisions

- **D1. HTTP/1.1 + JSON, gzip, batched request/response.** Each batch is one request with one idempotency key and one version interval, which keeps idempotency and resume simple; gzip closes most of the size gap to protobuf. The wire types live in `agent/wire`, a standard-library-only Go module shared by both sides. Additive changes stay in `/agent/v1`; a breaking one would be `/agent/v2`.
- **D2. A separate Go service, `agentserver`** (`agent/server/`), not new routes in `be/`: `be`'s timeouts and body limit are wrong for bulk upload, the traffic and deploys stay isolated, and `agentserver` does its own auth where `be` sat behind nginx basic auth. (Since then `be` logs web users in with `agentserver`'s users too, and basic auth is gone; see [`architecture.md`](../architecture.md#web-authentication).) It shares `be`'s Postgres database but only uses its own `agent_*` tables and migrations.
- **D3. Its own tables, not `scandata`.** `scandata` holds a full snapshot per scan with absolute paths and MD5; the agent's checkpoint is incremental, relative to a drive root, BLAKE3-hashed and has deletions. `agent_files`, `agent_dir_listings` and `agent_scan_runs` mirror the agent's tables, keyed on SHA-256 of the raw path bytes (no length limit; exact for non-UTF-8 names).
- **D4. A versioned change feed, uploaded as ranges in any order.** Every scan-owned write in `state.db` stamps a global, increasing `row_version`; a deletion leaves a tombstone; each key appears once, at its latest version. A batch covers a version interval `(from, to]` of one drive; the server applies it, higher version wins (tombstones included), and merges the interval into the drive's **acked ranges**. `scan` uploads only what's above the clock value `S` it read before scanning; `sync` fills the gaps below. Each drive's feed has a `stream_id`, renewed whenever the drive's local data is reset (a new `state.db`, `--replace-root`, or either side found to have gone back in time), and a new stream makes the server start the drive over.
- **D5. Idempotency in two layers:** acked ranges (a batch inside one is a duplicate), and an `Idempotency-Key` on every mutating `POST`, derived deterministically for batches so it survives restarts.
- **D6. Auth:** username and password (argon2id) → a 15-minute JWT access token plus a rotating 30-day refresh token, with a 30 s grace for a lost refresh response and family revocation on reuse. Failed logins lock out per (username, client IP). The password is never stored, and never taken from a flag or the environment.
- **D7. nginx exempts `/agent/` from basic auth**; app auth and rate limits protect it. At home, the agent's `lan_addr` dials the server's LAN address first (verifying the public name's certificate), because the NAT hairpin through the public IP is unreliable.
- **D8. Drive identity: filesystem ID plus hardware serial, read only.** The server links a user's drive rows into one **physical drive** by filesystem ID, using the serial to tell a clone from its original. For FAT, exFAT and NTFS, Linux and macOS report different IDs, so those link only within one OS ([proposed fix](remote-sync-cross-os-linking.md)). Locally, the same identity is a guard against scanning the wrong drive under a familiar `--drive-id`.

## Flow

```mermaid
sequenceDiagram
    participant A as driveagent scan
    participant L as state.db
    participant S as agentserver
    participant P as Postgres

    A->>S: GET /agent/health, POST /agent/v1/handshake
    A->>S: token (refresh if due)
    A->>L: drive checks; S = sync_clock
    A->>S: PUT /agent/v1/drives/{id} {stream_id, roots, identity}
    S-->>A: {acked_ranges, reset, physical_drive}
    A->>L: reconcile the synced marker
    par scan
        A->>L: walk, hash, write rows (row_version > S)
    and upload
        loop until the scan is done and drained
            A->>S: POST /agent/v1/drives/{id}/changes (gzip, Idempotency-Key)
            S->>P: apply in one tx, merge the range
            S-->>A: {acked_ranges, rejected}
            A->>L: copy the ranges into the marker
        end
    end
```

`driveagent sync` follows the same preflight, then for each drive: open, reconcile, and upload each gap of history, oldest first. `driveagent login` does the preflight and the login once, storing the tokens; later runs only refresh them.
