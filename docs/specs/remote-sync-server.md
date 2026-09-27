# Remote Sync: `agentserver` Server

**Status:** implemented (migrations 1–3). This is the compact as-built reference for `agent/server/`. The original spec, with its rationale and test list, is in [`archive/remote-sync/remote-sync-server.md`](../archive/remote-sync/remote-sync-server.md). The overview is [`remote-sync.md`](remote-sync.md).

`agentserver` receives scan data from `driveagent` and stores it in the Bhandaar Postgres database, next to `be`; neither calls the other.

## Layout

```
agent/
  client/   driveagent (module …/agent/client)
  wire/     request/response types shared by both, standard library only (module …/agent/wire)
  server/   agentserver (module …/agent/server)
    cmd/agentserver/        serve (default), user, housekeeping
    internal/api/           handlers and middleware (body limits, version check, auth, idempotency, gzip)
    internal/auth/          argon2id, JWT, refresh-token rotation
    internal/store/         Postgres (pgx/v5), migrations, the apply algorithm, matching
    internal/housekeeping/  hourly cleanup
    build/Dockerfile
```

Routing is `net/http.ServeMux` with method patterns; JWTs use `golang-jwt/jwt/v5`.

### Shared wire module

`agent/wire` has its own `go.mod` with **no `require` lines** (a test enforces it), so importing it pulls nothing into either side's module graph. Both `agent/server` and `agent/client` require it with `replace … => ../wire`, so a wire change and the code using it land in one commit; there's no `go.work`. Its `go` line stays at the lowest version it needs. Builds need `agent/wire` next to the module being built (the Docker build uses the repo root as its context). Both CI workflows run on `agent/wire/**` changes, and a wire change needs an agent version bump.

## Configuration

| Env var | Default | Notes |
|---|---|---|
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSL_MODE` | as for `be` | Same database as `be` |
| `AGENTSERVER_LISTEN` | `:8091` | |
| `AGENTSERVER_JWT_SECRET` | none (required) | ≥ 32 random bytes, base64 (`openssl rand -base64 48`) |
| `AGENTSERVER_ACCESS_TTL` | `15m` | |
| `AGENTSERVER_REFRESH_TTL` | `720h` | Sliding: each rotation issues a fresh 30 days |
| `AGENTSERVER_MIN_AGENT_VERSION` | `0.1.0` | Below it: `upgrade_required`, and `426` on normal endpoints |
| `AGENTSERVER_LATEST_AGENT_VERSION` | `0.1.0` | Below it: `upgrade_recommended`. Raised by hand after each agent release |
| `AGENTSERVER_TRUSTED_PROXIES` | loopback and private ranges | Where `X-Real-IP` is accepted from (nginx) |
| `AGENTSERVER_AGENT_DOWNLOAD_URL` | `https://github.com/jyothri/bhandaar/releases/latest` | Returned with upgrade decisions |

Server timeouts: read and write 60 s, idle 120 s. Body limits: `/changes` 2 MiB compressed and 16 MiB decompressed (enforced while streaming); 16 KiB elsewhere.

## API

All under `/agent/`; versioned endpoints under `/agent/v1/`. Request headers:
- `X-Agent-Version` on every request. Below the minimum gives `426`, except on health and the handshake.
- `X-Agent-Protocol` after the handshake (missing means 1; unsupported gives `426`).
- `X-Agent-Id` on every request; it must match the token's `aid` claim.
- `Authorization: Bearer <access token>` on everything except health, handshake, login, refresh and logout.
- `Idempotency-Key` (≤ 128 chars), required on mutating `POST`s other than auth.
- `Content-Encoding: gzip`, optional on `/changes` (the agent always sends it).

Errors have `be`'s shape: `{"error": {"code", "message", "details", "timestamp"}}`.

| Endpoint | Does |
|---|---|
| `GET /agent/health` | No auth. DB ping (1 s): `200 {"status":"ok", …}` or `503 {"status":"unavailable","reason":"database"}` |
| `POST /agent/v1/handshake` | No auth. `{agent_version, protocols, os, arch}` → `{decision, protocol, min_agent_version, latest_agent_version, download_url, message, limits}`. Decisions: `ok`, `upgrade_recommended`, `upgrade_required`, `unsupported_protocol`. `limits`: `max_changes_per_batch` 1000, `max_batch_bytes` 1 MiB |
| `POST /agent/v1/auth/login` | `{username, password, agent_id, hostname, os, arch}` → tokens. Registers the agent (`403 AGENT_OWNED_BY_OTHER_USER` if another user has it). Same `401 INVALID_CREDENTIALS` for every failure, in constant time. 10 failures per (username, `X-Real-IP`) in 15 minutes → `429` with `Retry-After` |
| `POST /agent/v1/auth/refresh` | See [below](#post-agentv1authrefresh) |
| `POST /agent/v1/auth/logout` | Revokes the refresh token's family; always `204` |
| `GET /agent/v1/drives` | This agent's drives: `drive_id`, `stream_id`, `acked_ranges`, roots, `last_synced_at`, `physical_drive` |
| `PUT /agent/v1/drives/{drive_id}` | Opens a drive's stream; see [below](#put-agentv1drivesdrive_id) |
| `POST /agent/v1/drives/{drive_id}/changes` | One change batch; see the [apply algorithm](#apply-algorithm) |

Access tokens are HS256 JWTs with `sub` (user), `aid` (agent), `iat`, `exp`, `jti`.

### `POST /agent/v1/auth/refresh`

`{refresh_token}` → a new pair; the old token is marked rotated. The token's row is locked (`FOR UPDATE`), so concurrent refreshes are serialised.
- Within **30 s** of its rotation, a rotated token is accepted **once more** if its successor is unused: a fresh pair is issued, and the unreceived successor is revoked with `revoked_reason = 'grace'` (a lost response or a crash before saving).
- Presenting it later, a second time, or after the successor was used is reuse: the whole family is revoked, `401 REFRESH_REUSED`.
- Presenting a grace-revoked successor is also reuse. That catches a thief who replayed within the window, at the owner's next refresh.
- An expired or otherwise revoked token gives `401 INVALID_REFRESH_TOKEN`.

### `PUT /agent/v1/drives/{drive_id}`

`{stream_id, drive_root, backup_root, identity: {fs_uuid, fs_type, fs_uuid_source, hw_serial}}` (identity fields optional; `drive_id` ≤ 128 bytes, path-escaped) → `{drive_id, stream_id, acked_ranges, reset, physical_drive: {id, clone_of, linked: [{hostname, drive_id, last_synced_at}]}}`. In one transaction with the drive row locked:
- no row: create it, with no ranges;
- the same `stream_id`: update the roots, return the ranges;
- a different `stream_id`: delete the drive's files, listings, runs, tombstones and ranges, set the new stream, `acked_version = 0`, `reset: true`;
- always: store the identity, and when it's new or changed, run [matching](#matching-physical-drives).

`acked_ranges` are the version intervals `(from, to]` fully applied, sorted and apart; the one from 0 ends at the drive's watermark (`acked_version`). `physical_drive` is `null` when unmatched; `linked` lists this user's other agents' rows on the same physical drive. The body is decoded leniently (unknown fields are ignored).

### `POST /agent/v1/drives/{drive_id}/changes`

`{stream_id, from_version, to_version, changes: [...]}`, gzip, strict JSON. The batch covers `(from_version, to_version]`: `changes` holds every entry of the drive's feed in it, sorted by `v`, unique, and inside the interval. It may be empty (just recording coverage). `to_version` may be above the last `v` (closing a gap). Paths are relative to `drive_root`, `/`-separated; `path: ""` is the root (only as a `dir_child` parent). A broken envelope (unsorted or repeated `v`, `v` outside the interval, `from ≥ to`, a bad `stream_id`) is `400 INVALID_BATCH`; more than 1,000 changes is `413`. The answer is `{acked_ranges, applied, skipped, duplicate, rejected: [{v, reason}]}`.

#### Apply algorithm

In one transaction:
1. Lock the drive row (`404 DRIVE_NOT_OPEN` if missing).
2. A different `stream_id` → `409 STREAM_MISMATCH` with `details.stream_id`.
3. **Coverage check:** if `(from, to]` lies inside one acked range, answer `duplicate: true` with the current ranges. This comes before the key lookup, because a restarted agent may resend a range with different contents.
4. **Idempotency:** a known `(user, key)` with the same request hash returns the stored response; with a different hash, `422 IDEMPOTENCY_KEY_REUSED`.
5. **Validate each entry** (valid `_b64`, no NUL, sizes and times in range, known kind and op). Invalid entries go in `rejected`. A rejected `file`/`dir_child` whose key can be computed becomes a **delete at its version**, so the server shows the key as missing rather than stale.
6. **Apply** every entry not inside an acked range (the rest are `skipped`), where its `v` is above the key's current version (row or tombstone): an upsert writes the row and drops the tombstone; a delete removes the row and writes a tombstone; scan runs upsert by `(drive, run_id)`. One `unnest` statement per table and op, each under its own `SAVEPOINT`; on a data error (SQLSTATE class 22 or 23), fall back to one savepoint per entry, rejecting only those that fail. Any other error is `500`, with nothing applied.
7. Merge `(from, to]` into the drive's ranges (merging touching and overlapping ones), update `acked_version` and `last_synced_at`.
8. Store the idempotency record, and commit.

Batches can arrive in any order: a key's higher version always wins, tombstones included, and entries inside acked ranges are never re-applied.

### Status and error codes

The agent classifies by **HTTP status first** (nginx answers some statuses itself, with HTML); the JSON `code` refines it.

| HTTP | `code` | Agent treats as |
|---|---|---|
| 400 | `INVALID_BATCH`, `INVALID_REQUEST` | Permanent (a bug) |
| 401 | `TOKEN_EXPIRED`, `INVALID_TOKEN` | Refresh once, retry; a second one means `driveagent login` |
| 401 | `INVALID_CREDENTIALS`, `INVALID_REFRESH_TOKEN`, `REFRESH_REUSED` | Log in again |
| 403 | `AGENT_OWNED_BY_OTHER_USER`, `AGENT_MISMATCH` | Permanent |
| 404 | `DRIVE_NOT_OPEN` | Re-open the drive (`PUT`), then retry |
| 409 | `STREAM_MISMATCH` | Re-open and reconcile |
| 413 | `PAYLOAD_TOO_LARGE`, or nginx's HTML 413 | Halve the batch |
| 422 | `IDEMPOTENCY_KEY_REUSED` | Permanent (a bug) |
| 426 | `UPGRADE_REQUIRED` | Upgrade needed |
| 429 | `RATE_LIMITED`, `TOO_MANY_ATTEMPTS` | Transient; honour `Retry-After` |
| 500, 502, 503, 504 | any | Transient; back off |

## Matching physical drives

A drive row belongs to one agent; copies from different machines are **linked** to one physical drive, never merged. Matching runs in the `PUT` transaction, under a lock on the user's row (so two agents opening a new drive at once create one physical drive). Candidates are the user's physical drives with the same normalised `fs_uuid`; for FAT, exFAT and NTFS they must also have the same `fs_uuid_source`, since Linux and macOS report different IDs for those.

| Situation | Result |
|---|---|
| No `fs_uuid` in the request | Not linked (a serial identifies a disk, not a partition) |
| No candidates | A new physical drive |
| A candidate with the same serial, or where either side has none | The same drive: keep the current link if it's a candidate, else the oldest; fill in a missing serial |
| Every candidate has a different serial | A clone: a new physical drive with `clone_of` the oldest candidate |

A drive cloned into an enclosure that reports no serial gets linked to its original; linking only groups rows, and each keeps its own data. A second step, matching FAT/exFAT/NTFS across OSes by partition key, is proposed in [`remote-sync-cross-os-linking.md`](remote-sync-cross-os-linking.md).

## Database schema

Migrations are numbered SQL files, applied at startup in a transaction each and recorded in `agentserver_schema_migrations`; the server never touches `be`'s tables. An older image starts on a newer schema (unknown versions are ignored).

| Table | Holds |
|---|---|
| `agent_users` | Username, argon2id PHC hash, `disabled_at` |
| `agent_login_failures` | (username, client IP, time), for the lockout |
| `agent_agents` | `agent_id` → user, hostname, os, arch, `last_version` (recorded at login) |
| `agent_refresh_tokens` | `sha256(token)`, family, `parent_id`, expiry, `rotated_at`, `grace_used_at`, `revoked_at`, `revoked_reason` (`grace`, `reuse`, `logout`, `disabled`) |
| `agent_physical_drives` | Per user: `fs_uuid`, `fs_type`, `fs_uuid_source`, `hw_serial`, `clone_of` |
| `agent_drives` | (`agent_id`, `drive_id`) unique: the reported identity, `physical_drive_id`, `stream_id`, `acked_version`, roots, `last_synced_at` |
| `agent_files` | (`drive_pk`, `path_key`): readable path, `raw_path` (non-UTF-8 only), size, mtime, mode, hash, status, error, `scanned_at`, `row_version` |
| `agent_dir_listings` | (`drive_pk`, `entry_key`), `parent_key`: parent and child (readable and raw), `is_dir`, `first_seen_at`, `row_version` |
| `agent_scan_runs` | (`drive_pk`, `run_id`): times, counters, `interrupted`, `row_version` |
| `agent_tombstones` | (`drive_pk`, kind, key), `row_version` |
| `agent_sync_ranges` | (`drive_pk`, `from_version`), `to_version`, merged, `from < to` |
| `agent_idempotency_keys` | (user, key): request hash, status, response body |

Drive data tables cascade on the drive row. Compare results and `quick_sig` aren't uploaded.

### Keys

Rows are keyed on SHA-256 of the **raw path bytes**, not the text: `path_key = sha256(path)`, `entry_key = sha256(parent || 0x00 || child)`, `parent_key = sha256(parent)`. That avoids Postgres's ~2.7 KB index-entry limit (Linux paths reach 4 KB) and keeps keys exact for names that look alike.

### Paths that aren't valid UTF-8

They're uploaded as `path_b64`/`child_b64`. The server stores a display form with `\xHH` for invalid bytes, the raw bytes in `raw_path`/`raw_child_name`, and keys on the raw bytes. A `_b64` value that decodes to valid UTF-8, or contains NUL, is rejected.

## Housekeeping

An hourly goroutine (first run 5 minutes after start), and `agentserver housekeeping` for a one-off run. Each task deletes in batches of 5,000 rows (`ctid`), logs one line (`housekeeping task=… deleted=… took=…`), and a failing task doesn't stop the others.

| Task | Deletes |
|---|---|
| `idempotency_keys` | Older than 7 days (a replay after that is still caught by the coverage check) |
| `login_failures` | Older than 1 day |
| `refresh_tokens` | Expired more than 7 days ago |
| `tombstones` | At or below their drive's watermark |

## Admin CLI

Inside the container (`docker exec -it <container> …`):
- `agentserver user add --username NAME`, `user passwd --username NAME`: the password is prompted twice with no echo, at least 12 characters.
- `agentserver user disable --username NAME`: also revokes the user's refresh tokens.
- `agentserver user list`.
- `agentserver housekeeping`: one run of the four tasks.

## Deployment

- **Image:** `jyothri/bhandaar-agentserver:latest`, pushed on merge to `main` ([CI](remote-sync-ci.md#server-agentserver-docker-imageyml)); static binary on Alpine, non-root.
- **Prod compose** (`~/jyothri-apps/apps/storagemanager`, the user's): service `agentserver` on `hdd_db`'s network, `DB_*` as for `be` (password from `HDD_DB_PASS`), `AGENTSERVER_JWT_SECRET`, the version variables; port 8091 exposed to nginx only. Redeploy: pull `:latest`, restart the service.
- **nginx**, in the `sm.jkurapati.com` block (and `dev.sm.jkurapati.com`, pointing at the dev box, for testing):

  ```nginx
  # outside server{}:
  limit_req_zone $binary_remote_addr zone=agent_auth:1m rate=10r/m;

  location /agent/ {
      auth_basic off;                       # the service does its own auth
      client_max_body_size 2m;
      proxy_read_timeout 90s;
      proxy_pass http://<agentserver host>:8091;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-Proto $scheme;
  }
  location /agent/v1/auth/ {
      auth_basic off;
      limit_req zone=agent_auth burst=5 nodelay;
      proxy_pass http://<agentserver host>:8091;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-Proto $scheme;
  }
  ```

- The client IP comes from `X-Real-IP`, trusted only from `AGENTSERVER_TRUSTED_PROXIES` (`X-Forwarded-For` can be spoofed). Requests made on the prod box itself arrive through Docker's userland proxy, so they're logged as `172.23.0.1`.
