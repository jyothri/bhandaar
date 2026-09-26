# Remote Sync: `agentsync` Server

**Status:** proposed (not implemented). The overview and decisions are in [`remote-sync.md`](remote-sync.md).

`agentsync` is a standalone Go service that receives scan data from `driveagent` and stores it in the Bhandaar Postgres database. It sits next to `be/`, and neither service calls the other.

## Layout

```
agentsync/                     module github.com/jyothri/bhandaar/agentsync
  cmd/agentsync/main.go        `serve` (the server) + `user` and `housekeeping` admin subcommands
  wire/                        separate module github.com/jyothri/bhandaar/agentsync/wire:
                               request/response types, standard library only (shared with driveagent)
  internal/api/                HTTP handlers, middleware (auth, version check, idempotency, gzip, limits)
  internal/auth/               argon2id, JWT issue/verify, refresh-token rotation
  internal/store/              Postgres access + migrations
  internal/housekeeping/       hourly cleanup of expired rows (see Housekeeping)
  build/Dockerfile
```

- Router: the standard library `net/http.ServeMux` with method and path patterns (Go ≥ 1.22).
- Postgres: `pgx/v5`. Batches are applied with `unnest`-array upserts, one statement per table per batch.
- JWT: `github.com/golang-jwt/jwt/v5`. Password hashing: `golang.org/x/crypto/argon2`.
- The shared wire types are their own module; see [Shared wire module](#shared-wire-module).

### Shared wire module

The request and response types are defined once, in `agentsync/wire`, so the agent and the server can't drift apart. `wire` is a **separate Go module**, with its own `go.mod`, nested inside `agentsync/`. Go excludes a nested module's directory from its parent module, so `agentsync` imports it like any other module.

```
agentsync/wire/go.mod           module github.com/jyothri/bhandaar/agentsync/wire
                                go 1.22        (no require lines)
agentsync/go.mod                require github.com/jyothri/bhandaar/agentsync/wire v0.0.0
                                replace github.com/jyothri/bhandaar/agentsync/wire => ./wire
agent/linux/go.mod              require github.com/jyothri/bhandaar/agentsync/wire v0.0.0
                                replace github.com/jyothri/bhandaar/agentsync/wire => ../../agentsync/wire
```

Rules:
- **Standard library only.** `wire/go.mod` has no `require` lines. A `require` pulls a whole module into the importer's module graph, not just one package, so this keeps the agent's `go.sum` and version selection unaffected by the server's dependencies (pgx, jwt, argon2). A test in `wire` fails if `go.mod` gains a `require`.
- **`replace`, not tags or `go.work`.** The `replace` lines resolve `wire` from the same checkout, so a wire change and the code using it land in one commit, with no module tags or publishing. They live in `go.mod`, so they also apply in CI and Docker builds. There is no `go.work` file; with `replace` in place it would be redundant. (A developer may create an uncommitted one for editor convenience; it's in `.gitignore`.)
- **Go version.** The `go` line in `wire/go.mod` stays at the lowest version its code needs (`1.22`), and never above the `go` line of either importer (`agent/linux` is `1.27.1`). Otherwise the older importer fails to build.
- **Builds need both directories.** Docker builds of `agentsync` already use the repo root as build context, so `wire` is included. Any future agent build (release job, Dockerfile) must also check out or copy `agentsync/wire` alongside `agent/linux`.
- **CI.** Both workflows watch `wire`: the `agentsync` workflow triggers on `agentsync/**`, and the `driveagent` workflow triggers on `agentsync/wire/**`. A `wire` change is therefore tested on both sides, and it counts as an agent change that needs a version bump. See [`remote-sync-ci.md`](remote-sync-ci.md).

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSL_MODE` | same as `be` | Same database as `be` |
| `AGENTSYNC_LISTEN` | `:8091` | |
| `AGENTSYNC_JWT_SECRET` | none (required) | ≥ 32 random bytes, base64. The server refuses to start without it |
| `AGENTSYNC_ACCESS_TTL` | `15m` | |
| `AGENTSYNC_REFRESH_TTL` | `720h` | 30 days, sliding (each rotation issues a fresh 30 days) |
| `AGENTSYNC_MIN_AGENT_VERSION` | `0.1.0` | Below this: `upgrade_required` |
| `AGENTSYNC_LATEST_AGENT_VERSION` | `0.1.0` | Below this (but ≥ min): `upgrade_recommended` |
| `AGENTSYNC_AGENT_DOWNLOAD_URL` | `https://github.com/jyothri/bhandaar/releases/latest` | Returned in the handshake with upgrade decisions (see [releases](remote-sync-ci.md#linking-releases-to-the-servers-version-check)) |

Server timeouts: read 60 s, write 60 s, idle 120 s. Body limit on `/changes`: 2 MiB compressed, 16 MiB after decompression, enforced while streaming (gzip bomb protection). Other endpoints: 16 KiB.

## API

All paths are under `/agent/`. Versioned endpoints are under `/agent/v1/`. Health is unversioned so an agent of any version can probe it.

Common request headers:

| Header | When |
|---|---|
| `Authorization: Bearer <access_token>` | Every endpoint except health, handshake, login, refresh |
| `X-Agent-Version: 0.3.1` | Every request. On every endpoint **except** `GET /agent/health` and `POST /agent/v1/handshake`, the server returns `426` if it is below the minimum. Those two stay reachable to any version: health so any agent can probe the server, and the handshake so an old agent gets its `200 {decision: upgrade_required, download_url}` instead of a bare `426` |
| `X-Agent-Protocol: 1` | Every request after the handshake: the protocol it negotiated. Protocols are additive changes inside `/agent/v1` (see D1), so the path alone doesn't say which one an agent uses. A breaking change gets a new path (`/agent/v2`) instead. A missing header means protocol 1; an unsupported value gets `426` |
| `X-Agent-Id: <uuid>` | Every request. Must match the token's `agent_id` claim on authenticated endpoints |
| `Idempotency-Key: <string ≤ 128 chars>` | Required on mutating `POST`s (login, refresh and logout excepted) |
| `Content-Encoding: gzip` | Optional on `/changes` (the agent always sends it) |

Errors use the same shape as `be` (`be/web/middleware.go`):

```json
{"error": {"code": "STREAM_MISMATCH", "message": "…", "details": {"stream_id": "c1f5…"}, "timestamp": "2026-09-24T10:00:00Z"}}
```

### `GET /agent/health`

No auth. Cheap: a DB ping with a 1 s timeout, no other queries.

```json
200 {"status": "ok", "service": "agentsync", "server_version": "0.1.0", "api_versions": ["v1"], "time": "2026-09-24T10:00:00Z"}
503 {"status": "unavailable", "service": "agentsync", "reason": "database", "api_versions": ["v1"], "time": "…"}
```

The agent treats anything other than a `200` with `status: ok` (including a connection error or timeout) as "unavailable".

### `POST /agent/v1/handshake`

No auth. Version negotiation. It runs before login, so an agent that needs an upgrade never sends credentials.

Request:
```json
{"agent_version": "0.3.1", "protocols": [1], "os": "linux", "arch": "amd64"}
```

Response `200`:
```json
{
  "decision": "ok",
  "protocol": 1,
  "min_agent_version": "0.1.0",
  "latest_agent_version": "0.3.1",
  "download_url": "",
  "message": "",
  "limits": {"max_changes_per_batch": 1000, "max_batch_bytes": 1048576}
}
```

| `decision` | Meaning | Agent action |
|---|---|---|
| `ok` | Compatible | Proceed |
| `upgrade_recommended` | Compatible, newer version available | Proceed, print `message` once |
| `upgrade_required` | `agent_version` < min | Don't log in; the agent command exits 4 |
| `unsupported_protocol` | No common protocol (agent too new for this server) | Same as `upgrade_required`, with a message that the server is behind |

`protocol` is the highest value in both the agent's `protocols` list and the server's supported set. Protocol 1 is this document. `limits` lets the server tighten batch sizes without an agent release. The agent uses the smaller of its own defaults and these limits.

### `POST /agent/v1/auth/login`

Request:
```json
{"username": "jyothri", "password": "…", "agent_id": "7b0e…", "hostname": "optiplex7070"}
```
Response `200`:
```json
{"token_type": "Bearer", "access_token": "eyJ…", "access_expires_in": 900, "refresh_token": "rt_…", "refresh_expires_in": 2592000, "user": "jyothri"}
```
- `401 INVALID_CREDENTIALS` for an unknown user, a wrong password or a disabled user. The message is identical in all three cases, and the server spends constant time: it verifies against a dummy hash when the user doesn't exist.
- `429 TOO_MANY_ATTEMPTS` after 10 failures for the same **username from the same client IP** within 15 minutes, with `Retry-After`. Keying on the IP too matters because usernames aren't secret (`jyothri` is in this spec): a lockout per username alone would let anyone lock the owner out indefinitely with 10 bad attempts every 15 minutes, well under the nginx rate limit. The client IP comes from `X-Real-IP`, set by nginx (see [Deployment](#deployment)). nginx also rate-limits by IP.
- On success, the server upserts the `agent_agents` row (`agent_id`, user, hostname) and starts a new refresh-token family.
- An `agent_id` already bound to a different user gets `403 AGENT_OWNED_BY_OTHER_USER`.

Access-token JWT claims: `sub` (user id), `aid` (agent id), `iat`, `exp`, `jti`.

### `POST /agent/v1/auth/refresh`

Request `{"refresh_token": "rt_…"}`. Response: same shape as login, with a **new** refresh token. The old one is marked `rotated`. The token's row is locked (`FOR UPDATE`) during the exchange, so two simultaneous requests with the same token are handled one after the other: the first rotates it, and the second sees it as just rotated (below).

- Presenting a rotated token **within 30 s** of its rotation is accepted **once more**: the server issues a fresh pair and revokes the successor it issued before, which the agent never received. The usual cause is a lost refresh response, or an agent that crashed before saving the new token. Returning an error there instead would leave the agent retrying until the window ran out, then revoke the family, forcing an interactive `driveagent login` for every scan and cron `sync`. A token replayed a second time within the window is treated as reuse (below).
- Presenting a rotated token after that window revokes the **whole family** and returns `401 REFRESH_REUSED`. The agent must log in again.
- An expired or revoked token returns `401 INVALID_REFRESH_TOKEN`.

### `POST /agent/v1/auth/logout`

Request `{"refresh_token": "rt_…"}`. Revokes the family. Always `204`.

### `GET /agent/v1/drives`

Lists this agent's drives: `[{"drive_id", "stream_id", "acked_ranges", "drive_root", "backup_root", "last_synced_at", "physical_drive"}]`, where `physical_drive` has the same shape as in the `PUT` response. Used by `driveagent remote-status`.

### `PUT /agent/v1/drives/{drive_id}`

Opens (or re-opens) a drive's stream, and returns which parts of the drive's feed the server already has. Called whenever an agent process starts uploading a drive, before any batch. `drive_id` is path-escaped and at most 128 bytes.

Request:
```json
{"stream_id": "c1f5…", "drive_root": "/mnt/seagate2", "backup_root": "Jyo/Backup",
 "identity": {"fs_uuid": "ABCD1234", "fs_type": "exfat", "fs_uuid_source": "linux", "hw_serial": "NA8F2K1X"}}
```
Every `identity` field is optional (see [drive identity](remote-sync-agent.md#drive-identity) in the agent spec).

Response `200`:
```json
{"drive_id": "seagate2", "stream_id": "c1f5…", "acked_ranges": [[0, 18234], [20011, 20510]], "reset": false,
 "physical_drive": {"id": 7, "clone_of": null,
                    "linked": [{"hostname": "macbook", "drive_id": "seagate1", "last_synced_at": "2026-09-20T18:02:11Z"}]}}
```
`physical_drive` is `null` when the drive couldn't be matched (no filesystem ID). `linked` lists this user's *other* drive rows, from other agents, matched to the same physical drive.

`acked_ranges` lists the version intervals `(from, to]` the server has fully applied, sorted and non-overlapping. The agent keeps a copy of them and compares it with the next response, to notice if either side went back in time (see [Reconciling](remote-sync-agent.md#reconciling) in the agent spec). Everything in the drive's feed inside them is on the server. A drive is synced contiguously from 0 up to its **watermark**, the end of the range starting at 0 (`acked_version` in the table below). Ranges above the watermark come from scans that uploaded their own data while older history was still pending on the agent (see the [agent spec](remote-sync-agent.md#what-each-command-uploads)).

In one transaction, with the drive row locked (`FOR UPDATE`):
- **No row**: create it, with no ranges.
- **Same `stream_id`**: update `drive_root`/`backup_root` and return the current ranges.
- In every case, store the reported `identity` on the row. If it's new or changed, run [matching](#matching-physical-drives) to set `physical_drive_id`.
- **Different `stream_id`**: delete the drive's `agent_files`, `agent_dir_listings`, `agent_scan_runs`, `agent_tombstones` and `agent_sync_ranges` rows, set the new `stream_id` and `acked_version = 0`, and return `reset: true` with no ranges.

`PUT` is idempotent by definition; an `Idempotency-Key` is accepted but not required.

#### Matching physical drives

A drive row belongs to one agent (`agent_id`, `drive_id`), and its uploaded data stays there. Copies uploaded from different machines are **linked, not merged**: the server records that they are the same physical drive, so a UI or a server-side compare can treat them as one. Merging them into a single copy would need multi-writer sync and is out of scope for v1.

Matching runs within one user's drives, in the `PUT` transaction. It first locks the user's row (`SELECT … FROM agent_users WHERE id = $1 FOR UPDATE`), so two agents of the same user opening a new drive at the same moment can't both create a physical drive for it. Locking `agent_physical_drives` rows wouldn't be enough: when the drive is new, there are no rows yet to lock. The candidates are the user's physical drives with the same (normalised) `fs_uuid`. For FAT, exFAT and NTFS, they must also have the same `fs_uuid_source` (`linux` or `macos`), because the two operating systems report different IDs for those filesystems (see [drive identity](remote-sync-agent.md#drive-identity)). A Linux copy and a Mac copy of such a drive therefore stay unlinked in v1. For ext4, APFS and HFS+ the source doesn't matter.

| Situation | Result |
|---|---|
| The request has no `fs_uuid` | Not linked (`physical_drive_id = NULL`). A serial alone isn't used, because it identifies the disk, not the partition |
| No candidates | Create a physical drive from the request's identity, and link it |
| A candidate has the same `hw_serial` (both present) | **Same physical drive**: link it |
| A candidate has no `hw_serial`, or the request has none | **Same physical drive**: link it. If the request has a serial and the candidate doesn't, store it on the candidate |
| Every candidate has a `hw_serial`, and all differ from the request's | **A different drive with the same filesystem ID**, i.e. a clone (`dd`, disk-cloning tools). Create a new physical drive with `clone_of` set to the oldest candidate, and link it |

If several candidates would match (possible only when serials are missing), keep the row's existing link if it is one of them; otherwise link the oldest candidate.

**Known limitation:** because a missing serial counts as a match, a drive cloned from another and kept in an enclosure that reports no serial is linked as the *same* physical drive as its original. The agent's [wrong-drive guard](remote-sync-agent.md#drive-identity) can't tell such a pair apart either, because there's no serial to compare. Linking is informational only, and each copy's data stays in its own drive row. So the effect is a wrong grouping, never mixed data. Giving each drive of a mirrored pair its own `--drive-id` still keeps their data separate.

### `POST /agent/v1/drives/{drive_id}/changes`

Uploads one batch of the drive's change feed. Batches may arrive **in any version order**: an agent's `scan` uploads only its own changes, which are newer than the history that `sync` uploads later.

Request (gzip-compressed JSON):
```json
{
  "stream_id": "c1f5…",
  "from_version": 18234,
  "to_version": 19233,
  "changes": [
    {"v": 18240, "kind": "file", "op": "upsert", "path": "Jyo/Backup/a.jpg",
     "size": 482113, "mtime_unix": 1726000000, "mode": 420,
     "content_hash": "9f2c…", "hash_algo": "blake3", "status": "hashed", "scanned_at": "2026-09-24T09:58:01Z"},
    {"v": 18241, "kind": "file", "op": "upsert", "path": "Jyo/Backup/b.mov",
     "size": 0, "mtime_unix": 1726000100, "mode": 420, "status": "error", "error_message": "read: input/output error", "scanned_at": "…"},
    {"v": 18302, "kind": "file", "op": "delete", "path": "Jyo/Backup/old.txt"},
    {"v": 18400, "kind": "dir_child", "op": "upsert", "path": "Jyo/Backup", "child": "photos", "is_dir": true, "first_seen_at": "…"},
    {"v": 18401, "kind": "dir_child", "op": "delete", "path": "Jyo/Backup", "child": "tmp"},
    {"v": 19233, "kind": "scan_run", "op": "upsert", "run_id": 42, "started_at": "…", "finished_at": null,
     "files_seen": 0, "bytes_hashed": 0, "interrupted": null}
  ]
}
```

Semantics:
- The batch **covers** `(from_version, to_version]` (exclusive, inclusive). The agent claims that `changes` holds every entry of the drive's current feed in that interval. Versions have gaps (superseded rows, other drives' writes), which is expected. `to_version` is usually the highest `v`, but may be higher: the agent sets it to the start of the next range when closing a gap, so the ranges meet.
- `changes` may be **empty** (with `from_version < to_version`). That just records coverage, and is how an agent closes a gap it knows holds no pending entries.
- `changes` is sorted by `v`, each `v` appears at most once, and every `v` is inside the covered interval.
- The feed holds each key at most once, at its latest version, as either an upsert or a delete. Across batches, the higher version of a key wins.
- Paths are relative to `drive_root`, with `/` separators and no leading `/`. `path: ""` is the drive root itself (valid only as a `dir_child` parent).

Header `Idempotency-Key` is required. Response `200`:
```json
{"acked_ranges": [[0, 19233], [20011, 20510]], "applied": 997, "skipped": 2, "duplicate": false,
 "rejected": [{"v": 18241, "reason": "mtime out of range"}]}
```

`rejected` lists entries the server couldn't store. The batch's range is still acknowledged, so a single bad entry can't block the drive. The agent records them and shows them in `remote-status` (see [Rejected entries](remote-sync-agent.md#rejected-entries)).

#### Apply algorithm

In one transaction:

1. Lock the drive row with `FOR UPDATE`. A missing drive returns `404 DRIVE_NOT_OPEN`, and the agent then calls `PUT`.
2. If `stream_id` differs from the stored one, return `409 STREAM_MISMATCH` with `details.stream_id`. The agent re-opens the stream.
3. **Coverage check.** If `(from_version, to_version]` lies entirely inside one acked range, the batch is a full duplicate: return `200` with `duplicate: true` and the current ranges. This comes **before** the key lookup. An agent restarted after a lost response re-reads the page, and may send the same range with slightly different contents (a byte-limited page can be cut differently). That must count as a duplicate, not as a reused key.
4. **Idempotency lookup.** If `(user_id, key)` exists with the same request hash, return the stored response. If it exists with a different hash, return `422 IDEMPOTENCY_KEY_REUSED`.
5. **Validate each entry** against the server's rules (valid `_b64`, no NUL bytes, sizes and timestamps in range, known `kind`/`op`). Entries that fail are left out and listed in `rejected` with a reason; the rest continue.
6. Apply every remaining change whose `v` is **not** inside an acked range (the rest are counted as `skipped`; they were applied, or superseded, when that range was acked). For each key, compare `v` with the key's current version on the server, which is the version of its row or of its tombstone, whichever exists:
   - **upsert** (file, dir_child): apply only if `v` is higher. Write the row with `row_version = v` and delete the key's tombstone.
   - **delete** (file, dir_child): apply only if `v` is higher. Delete the row and write a tombstone with `row_version = v`.
   - **scan_run upsert**: upsert keyed by `(drive_pk, run_id)` if `v` is higher. Scan runs are never deleted individually.

   In SQL this is one `unnest` statement per table and op. For example, a file upsert is `INSERT … ON CONFLICT … DO UPDATE … WHERE agent_files.row_version < EXCLUDED.row_version`, filtered with `NOT EXISTS` against a tombstone with a higher version.

   **If a bulk statement fails** on data (a constraint, a value Postgres rejects), the server falls back to applying that batch **entry by entry**, each under a `SAVEPOINT`. Entries that still fail are rolled back to their savepoint and added to `rejected` with the database error as the reason. The fast path stays one statement per table, and one unexpected bad entry costs a slower batch, not a stuck drive. Only failures unrelated to the data, such as a lost connection, return `500`.
7. Add `(from_version, to_version]` to `agent_sync_ranges`, merging it with every range it overlaps or touches. Set `acked_version` to the end of the range that starts at 0, if there is one, and `last_synced_at = now()`.
8. Store the idempotency record (key, request hash, status, response body).
9. Commit.

Why this is safe in any order:
- Each key appears once in the agent's feed, and on the server the higher version of a key always wins. Tombstones make that include deletions: a stale upsert that arrives after a newer delete loses to the tombstone, rather than bringing the file back.
- Skipping changes inside acked ranges means a retried or slow old batch can't reapply anything.

There is no cursor to fall out of step with, so there is no "gap" error.

A batch whose **envelope** is invalid (unsorted or duplicate `v`, `v` outside `(from_version, to_version]`, `from_version ≥ to_version`, a missing `stream_id`) gets `400 INVALID_BATCH` and is not applied. This means a client bug, and the agent does not retry it. Problems with individual entries don't fail the batch; they go in `rejected`.

### Status and error codes

| HTTP | `code` | Agent treats as |
|---|---|---|
| 400 | `INVALID_BATCH`, `INVALID_REQUEST` | Permanent (bug); stop uploading |
| 401 | `TOKEN_EXPIRED` | Refresh once, then retry |
| 401 | `INVALID_CREDENTIALS`, `INVALID_REFRESH_TOKEN`, `REFRESH_REUSED` | Re-login needed; permanent for this run |
| 403 | `AGENT_OWNED_BY_OTHER_USER`, `AGENT_MISMATCH` | Permanent |
| 404 | `DRIVE_NOT_OPEN` | `PUT` the drive, then retry |
| 409 | `STREAM_MISMATCH` | Resync as described above |
| 413 | `PAYLOAD_TOO_LARGE`, or nginx's own `413` (HTML body) | Halve the batch size and retry |
| 422 | `IDEMPOTENCY_KEY_REUSED` | Permanent (bug) |
| 426 | `UPGRADE_REQUIRED` | Permanent; same as a handshake `upgrade_required` |
| 429 | `RATE_LIMITED`, `TOO_MANY_ATTEMPTS` | Transient; honour `Retry-After` |
| 500, 502, 503, 504 | any (nginx's `502`/`504` have an HTML body) | Transient; back off and retry |

The agent classifies responses **by HTTP status code**. The JSON `code` only refines it, because nginx answers some statuses itself with an HTML body.

## Database schema

Tables live in the shared database. The service creates them at startup through numbered migrations, recorded in `agentsync_schema_migrations(version INT PRIMARY KEY, applied_at TIMESTAMPTZ)`. It never alters `be`'s tables.

```sql
CREATE TABLE agent_users (
  id             BIGSERIAL PRIMARY KEY,
  username       TEXT NOT NULL UNIQUE,
  password_hash  TEXT NOT NULL,              -- argon2id PHC string
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  disabled_at    TIMESTAMPTZ
);

CREATE TABLE agent_login_failures (         -- lockout window per (username, client IP)
  username   TEXT NOT NULL,
  client_ip  INET NOT NULL,
  failed_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON agent_login_failures (username, client_ip, failed_at);
CREATE INDEX ON agent_login_failures (failed_at);          -- housekeeping

CREATE TABLE agent_agents (
  id               UUID PRIMARY KEY,          -- agent_id, generated by the agent
  user_id          BIGINT NOT NULL REFERENCES agent_users(id),
  hostname         TEXT,
  os               TEXT,
  arch             TEXT,
  last_version     TEXT,
  first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agent_refresh_tokens (
  id            BIGSERIAL PRIMARY KEY,
  token_hash    BYTEA NOT NULL UNIQUE,       -- sha256(token); raw tokens are never stored
  family_id     UUID NOT NULL,
  user_id       BIGINT NOT NULL REFERENCES agent_users(id),
  agent_id      UUID NOT NULL REFERENCES agent_agents(id),
  issued_at     TIMESTAMPTZ NOT NULL,
  expires_at    TIMESTAMPTZ NOT NULL,
  rotated_at    TIMESTAMPTZ,                 -- set when exchanged for a new one
  revoked_at    TIMESTAMPTZ
);
CREATE INDEX ON agent_refresh_tokens (family_id);
CREATE INDEX ON agent_refresh_tokens (expires_at);         -- housekeeping

CREATE TABLE agent_physical_drives (       -- one per real drive, across all of a user's agents
  id              BIGSERIAL PRIMARY KEY,
  user_id         BIGINT NOT NULL REFERENCES agent_users(id),
  fs_uuid         TEXT NOT NULL,             -- normalised: uppercase, no dashes
  fs_type         TEXT,
  fs_uuid_source  TEXT,                      -- 'linux' | 'macos'; part of the match for FAT/exFAT/NTFS
  hw_serial       TEXT,                      -- NULL if never reported
  clone_of        BIGINT REFERENCES agent_physical_drives(id),
  first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON agent_physical_drives (user_id, fs_uuid);

CREATE TABLE agent_drives (
  id              BIGSERIAL PRIMARY KEY,
  agent_id        UUID NOT NULL REFERENCES agent_agents(id),
  drive_id        TEXT NOT NULL,             -- the agent's label, e.g. "seagate2"
  physical_drive_id BIGINT REFERENCES agent_physical_drives(id),  -- NULL if not matched
  fs_uuid         TEXT,                      -- identity as last reported by the agent
  fs_type         TEXT,
  fs_uuid_source  TEXT,
  hw_serial       TEXT,
  stream_id       UUID NOT NULL,
  acked_version   BIGINT NOT NULL DEFAULT 0, -- watermark: end of the acked range starting at 0 (cached from agent_sync_ranges)
  drive_root      TEXT NOT NULL,
  backup_root     TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_synced_at  TIMESTAMPTZ,
  UNIQUE (agent_id, drive_id)
);

CREATE TABLE agent_files (
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  path_key        BYTEA NOT NULL,            -- sha256(raw path bytes): the key (see "Keys")
  relative_path   TEXT NOT NULL,             -- readable form, not unique by itself
  raw_path        BYTEA,                     -- only set when the path isn't valid UTF-8
  size            BIGINT NOT NULL,
  mtime           TIMESTAMPTZ NOT NULL,
  mode            INTEGER NOT NULL,
  content_hash    TEXT,
  hash_algo       TEXT,
  status          TEXT NOT NULL,             -- hashed | error
  error_message   TEXT,
  scanned_at      TIMESTAMPTZ NOT NULL,
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, path_key)
);
CREATE INDEX ON agent_files (drive_pk, content_hash);

CREATE TABLE agent_dir_listings (
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  entry_key       BYTEA NOT NULL,            -- sha256(raw parent bytes || 0x00 || raw child bytes)
  parent_key      BYTEA NOT NULL,            -- sha256(raw parent bytes), to list a directory's children
  relative_path   TEXT NOT NULL,             -- parent directory ('' = drive_root)
  child_name      TEXT NOT NULL,
  raw_path        BYTEA,                     -- only set when the parent path isn't valid UTF-8
  raw_child_name  BYTEA,                     -- only set when the child name isn't valid UTF-8
  is_dir          BOOLEAN NOT NULL,
  first_seen_at   TIMESTAMPTZ NOT NULL,
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, entry_key)
);
CREATE INDEX ON agent_dir_listings (drive_pk, parent_key);

CREATE TABLE agent_scan_runs (
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  run_id          BIGINT NOT NULL,           -- the agent's local scan_runs.id
  started_at      TIMESTAMPTZ NOT NULL,
  finished_at     TIMESTAMPTZ,
  files_seen      BIGINT,
  bytes_hashed    BIGINT,
  interrupted     BOOLEAN,
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, run_id)
);

CREATE TABLE agent_tombstones (             -- deletions, kept so an older upsert can't resurrect a key
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL,             -- 'file' | 'dir_child'
  key             BYTEA NOT NULL,            -- path_key or entry_key of the deleted row
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, kind, key)
);

CREATE TABLE agent_sync_ranges (            -- acked version intervals (from, to], merged, non-overlapping
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  from_version    BIGINT NOT NULL,
  to_version      BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, from_version),
  CHECK (from_version < to_version)
);

CREATE TABLE agent_idempotency_keys (
  user_id         BIGINT NOT NULL REFERENCES agent_users(id),
  key             TEXT NOT NULL,
  request_sha256  BYTEA NOT NULL,
  status_code     INTEGER NOT NULL,
  response_body   BYTEA NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);
CREATE INDEX ON agent_idempotency_keys (created_at);       -- housekeeping
```

Housekeeping (deleting rows that are no longer needed) is described in [Housekeeping](#housekeeping).

`quick_sig` and the agent's comparison columns are not uploaded (see non-goals).

### Keys

Rows are keyed on a **SHA-256 of the raw path bytes** (32-byte `BYTEA`), not on the path text:
- `agent_files`: `path_key = sha256(path)`.
- `agent_dir_listings`: `entry_key = sha256(parent || 0x00 || child)`, with `parent_key = sha256(parent)` indexed for listing a directory.
- `agent_tombstones`: the deleted row's key.

The paths themselves are ordinary, unindexed-for-uniqueness columns. That's for two reasons:
- **Length.** A Postgres B-tree entry is capped at about 2.7 KB after compression, while Linux allows relative paths near 4 KB, and a directory-listing key is parent plus child. Keying on the text would bring back a length limit, and an unpredictable one, since it depends on how well the path compresses. A 32-byte hash has no such limit, so "no length limit" in D3 holds.
- **Exactness.** The hash is over the raw bytes, so two different names can never share a key, even when their readable forms look alike (next section).

### Paths that aren't valid UTF-8

Such names are uploaded, not rejected (decided 2026-09-25). Linux filenames are arbitrary bytes. On the wire, a change carries either `path` (valid UTF-8) or `path_b64` (standard base64 of the raw bytes), never both; the same applies to `child` / `child_b64`. When the server gets a `_b64` form:
- `relative_path` / `child_name` store a readable display form in which each invalid byte becomes `\xHH`. It's for display only; the [key](#keys) is the hash of the raw bytes, so a file literally named `\xHH` can't collide with it.
- The raw columns store the original bytes: `raw_path` in `agent_files`, and `raw_path` (parent) and `raw_child_name` in `agent_dir_listings`. They stay `NULL` for valid UTF-8, which is nearly every row.
- An entry whose `_b64` value decodes to valid UTF-8 (the agent must send it as plain `path`/`child`), or contains a NUL byte (which Linux doesn't allow in names), goes in `rejected`.

## Housekeeping

A few tables only need recent rows. `agentsync` runs one instance, and it cleans them up itself; there is no separate cron job.

| Task | Deletes | Why it's safe |
|---|---|---|
| Idempotency keys | `agent_idempotency_keys` with `created_at` older than 7 days | An agent retries a batch within minutes. After 7 days a replay no longer needs the stored response; the acked-range check still answers it as a duplicate |
| Login failures | `agent_login_failures` with `failed_at` older than 1 day | The lockout only looks at the last 15 minutes |
| Refresh tokens | `agent_refresh_tokens` with `expires_at` more than 7 days ago | An expired token is rejected anyway. The 7 days keep recent rows around for looking into a reuse alert |
| Tombstones | `agent_tombstones` with `row_version` at or below their drive's `acked_version` (watermark) | Every version up to the watermark is acked, so any older change for that key is skipped on arrival, and the tombstone protects nothing. `acked_version` only increases, so this is safe while uploads are running |

**How it runs.** It's a goroutine in `internal/housekeeping`, started by `agentsync serve`:
- The first run is 5 minutes after startup, so it doesn't compete with a restart, then once an hour (`time.Ticker`). It stops with the server's context on shutdown.
- The four tasks run one after another. A task that fails is logged and tried again next hour; the others still run.
- Each task deletes in batches of 5,000 rows, each batch in its own short transaction, repeated until a batch deletes fewer than 5,000:

  ```sql
  DELETE FROM agent_idempotency_keys
   WHERE ctid IN (SELECT ctid FROM agent_idempotency_keys
                   WHERE created_at < now() - interval '7 days' LIMIT 5000);
  ```

  Tombstones are done drive by drive:

  ```sql
  DELETE FROM agent_tombstones
   WHERE ctid IN (SELECT t.ctid FROM agent_tombstones t
                   WHERE t.drive_pk = $1
                     AND t.row_version <= (SELECT acked_version FROM agent_drives WHERE id = $1)
                   LIMIT 5000);
  ```

  Small transactions keep any single delete from holding locks long enough to slow an upload. The filter columns are indexed (see the schema).
- Each task logs one line with `slog`: task name, rows deleted, duration. For example: `housekeeping task=idempotency_keys deleted=1840 took=42ms`.

Retention periods and the batch size are constants in the code, not configuration.

**Manual run:** `agentsync housekeeping` runs the four tasks once and exits, logging the same lines. It's useful in tests and for checking the service by hand, e.g. `docker exec <container> agentsync housekeeping`.

## Admin CLI

Run on the prod box, inside the container:

```bash
docker exec -it <agentsync container> agentsync user add --username jyothri   # prompts for password twice (no echo)
docker exec -it <agentsync container> agentsync user passwd --username jyothri
docker exec -it <agentsync container> agentsync user disable --username jyothri # also revokes all refresh tokens
docker exec -it <agentsync container> agentsync user list
```

Passwords must be at least 12 characters. They never appear in argv, logs or error messages.

## Deployment

- **Image and CI**: `jyothri/bhandaar-agentsync:latest`, pushed to Docker Hub on merge to `main` by `.github/workflows/agentsync-docker-image.yml`, the same way as `be` and `ui`. It's a static binary on Alpine, running as non-root. The workflow, the Dockerfile and the test setup (Postgres service container) are in [`remote-sync-ci.md`](remote-sync-ci.md#server-agentsync-docker-imageyml).
- **Prod compose** (`~/jyothri-apps/apps/storagemanager`, changed by the user): add an `agentsync` service on the same network as `hdd_db`, with the `DB_*` env vars (the password comes from `HDD_DB_PASS`, as for `be`) and `AGENTSYNC_JWT_SECRET`, exposing port 8091 to nginx only.
- **nginx** (prod box, changed by the user): in the `sm.jkurapati.com` server block, add

  ```nginx
  # outside server{}:
  limit_req_zone $binary_remote_addr zone=agent_auth:1m rate=10r/m;

  location /agent/ {
      auth_basic off;                       # app-level auth instead
      client_max_body_size 2m;
      proxy_read_timeout 90s;
      proxy_pass http://<agentsync host>:8091;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-Proto $scheme;
  }
  location /agent/v1/auth/ {
      auth_basic off;
      limit_req zone=agent_auth burst=5 nodelay;
      proxy_pass http://<agentsync host>:8091;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-Proto $scheme;
  }
  ```

  Mirror the same blocks in `dev.sm.jkurapati.com`, pointing at the dev box, for testing.
- The service takes the client IP from `X-Real-IP`, which nginx sets to `$remote_addr`, and only when the request comes from the nginx address. It uses it for the login lockout and for logging. `X-Forwarded-For` isn't used: `$proxy_add_x_forwarded_for` appends to whatever the client sent, so its first entry can be spoofed.

## Testing

- **Housekeeping**: each task deletes only rows past its cutoff, and leaves rows just inside it; a table with more than 5,000 expired rows is emptied over several batches; tombstones at or below the watermark go, those above it stay; one failing task doesn't stop the others.
- **Unit**: batch validation (including empty batches and `to_version` above the last `v`; `from_version = to_version` is `400`); per-entry validation puts bad entries in `rejected` and still acks the range; a bulk failure falls back to per-entry savepoints and rejects only the bad entry; a path near 4 KB (and a listing of a near-4 KB parent plus child) stores fine; a replayed range with different contents is a duplicate, not `422`; the apply algorithm (full duplicate, partial overlap, out-of-order ranges, range merging and watermark advance, stream reset, stale upsert after a newer delete loses to the tombstone, delete older than a live row ignored, tombstone GC below the watermark); physical-drive matching for every row of the matching table, including the clone case, a serial filled in later, and ambiguous candidates; a property test that applies random permutations of the same batches and gets identical tables; semver decisions; refresh rotation, a replay within 30 s getting a fresh pair (and the orphaned successor revoked), a second replay and a replay after 30 s revoking the family; the login lockout per (username, IP), so failures from one IP don't lock out another; `426` on normal endpoints but not on health or handshake; `X-Agent-Protocol` checked; idempotency replay and conflict; the gzip size cap.
- **Store tests** against a real Postgres (a CI service container locally; skipped when `AGENTSYNC_TEST_DB` is unset), each test in its own schema.
- **HTTP tests** with `httptest.Server`: full login → open drive → changes → duplicate replay flows.
- **Contract**: golden JSON fixtures in `agentsync/wire/testdata/`, used by both the server tests and the driveagent client tests, so the two sides can't drift.
