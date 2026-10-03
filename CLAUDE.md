# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Bhandaar is a storage analyzer application that scans and analyzes data across multiple sources:
- Local file systems
- Google Drive
- Gmail mailboxes
- Google Photos
- Google Cloud Storage

The application consists of:
- **Backend (be/)**: Go server with REST API, OAuth integration, and data collection services
- **Frontend (ui/)**: React + TypeScript SPA using Vite, TanStack Router, and TanStack Query
- **Agent (agent/)**: three sibling Go modules for local drive tracking and remote sync:
  - `agent/client/`: `driveagent`, the CLI that scans and compares drives (`docs/specs/drive-comparison-agent.md`), and logs in to `agentserver` (`login`, `logout`, `remote-status`) and uploads its scan data to it: every `scan` uploads what it writes, and `sync` uploads the rest (`docs/specs/remote-sync-agent.md`)
  - `agent/server/`: `agentserver`, the hosted service that receives `driveagent` uploads (auth, drives, change batches; `docs/specs/remote-sync-server.md`)
  - `agent/wire/`: the request/response types both sides share; standard library only

## Architecture

### Backend Structure (`be/`)

- **main.go**: Entry point that initializes logging and starts the web server
- **web/**: HTTP server and API handlers
  - `web_server.go`: Server initialization with CORS and routing setup
  - `auth.go`: web login (session cookie) and the middleware that requires it on every route but health and login/logout
  - `browse.go`: the Browse API (`/api/browse/…`), checking each source is the user's
  - `duplicates.go`: the Duplicates API (`/api/duplicates/…`), reading the user's index
  - `manage_data.go`: Manage data's API (`/api/manage-data`) and its deletions (a drive from one box, one service's data of an account, disconnecting an account with a typed name and revoking at Google), each started as a background job
- **auth/**: argon2id password verification for agentserver's users (be reads `agent_users`, never writes it)
  - `api.go`: REST API endpoints for scans, accounts, and data retrieval
  - `oauth.go`: Google OAuth2 flow implementation (authorization and callback)
  - `sse.go`: Server-Sent Events for real-time scan progress updates
- **collect/**: Data collection modules for different sources
  - `local.go`: Local filesystem scanning
  - `drive.go`: Google Drive scanning (whole Drive, or one folder with or without its subfolders) with SSE progress updates
  - `gmail.go`: Gmail mailbox scanning with SSE progress updates
  - `photos.go`: Google Photos through the Picker API: a picking session, polled until the user picks, then a scan sizing each picked item (`docs/archive/photos-picker.md`)
  - `gcs.go`: Google Cloud Storage: an account's projects and buckets, and scans of a project's buckets (or one, under a prefix) into the account's living record, with noncurrent and soft-deleted versions (`docs/archive/gcs-scans.md`)
  - `common.go`: Shared collection utilities
- **db/**: Database layer using PostgreSQL
  - `database.go`: Database initialization, schema creation, and CRUD operations
  - `users.go`: users, login lockout and `web_sessions`, from agentserver's tables; tying scans and linked accounts to users
  - `driveitems.go`: each linked account's living Drive record (`drive_items`, `drive_accounts`), updated by every Drive scan (`docs/archive/browse.md`)
  - `browse.go`: Browse's queries: sources, a folder's children (Drive record or agentserver's `agent_files`/`agent_dir_listings`, read only), an agent drive's status, an account's messages
  - `photos.go`: Google Photos picking sessions (`photos_picker_sessions`) and picked items (`photos_picked_items`)
  - `gcs.go`: each account's Cloud Storage record (`gcs_buckets`, `gcs_objects`, `gcs_prefix_totals`, updated by every scan) and each scan's totals per bucket (`gcs_scan_buckets`); Browse's buckets and prefixes
  - `deletion.go`: the deletions and their jobs (`deletions` table, one running per account and per drive, failed at restart, purged after 30 days), and `StartScan`, which every collector uses: a scan of an account and a deletion of it lock the account's `privatetokens` row (`FOR SHARE` / `FOR UPDATE`), so neither can start during the other; deleting a drive is be's only write to agentserver's tables (one `agent_drives` row, which cascades)
  - `totals.go`: the folder totals cache (`browse_folder_totals`, `browse_totals_state`) and its checker, a goroutine `main` starts that rebuilds changed agent drives every 10 minutes
  - `duplicates.go`: each user's duplicates index (`dup_groups`, `dup_members`, `dup_state`): identical files by exact key (MD5 and size, or an agent's BLAKE3), identical folders by Merkle signature (topmost only), and Google Photos items likely copied elsewhere (name and time). Its builder, a goroutine `main` starts, rebuilds a user's index when its fingerprint changes: at once, every 10 minutes, and soon after a scan or deletion ends (`WakeDuplicates`); a change to agent drives alone waits until they've been still for 5 minutes. See `docs/archive/duplicates.md`
  - Tables: `Scans`, `ScanData`, `messagemetadata`, `photos_picker_sessions`, `photos_picked_items`, `gcs_buckets`, `gcs_objects`, `gcs_prefix_totals`, `gcs_scan_buckets` (the Photos Library API's old tables are dropped at startup)
- **notification/**: SSE hub for broadcasting scan progress events
- **constants/**: Application constants and configuration

### Frontend Structure (`ui/`)

- **src/routes/**: TanStack Router route components
  - `index.tsx`: Browse, the landing page: pick a Google account (then Google Drive, Gmail or Google Photos) or an agent drive, and see a collapsible folder tree (`components/FolderTree.tsx`, one folder loaded at a time), the account's messages, or its picked photos (`components/PickedItemsTable.tsx`); `?source=google:<client_key>|agent:<id>&service=&folder=`
  - `request.tsx`: Scan request form for Gmail, Google Drive and Google Photos (`?type=gmail|drive|photos`; Photos picks in Google Photos, `components/PhotosPick.tsx`): account linking per service (`googleLink.ts`), the Drive query builder (`driveQuery.ts`), and live progress
  - `requests.tsx`: List view of scan requests by account (`?account=<client_key>`); each scan ID links to its results. It and a scan's page show a trail under the nav tabs (`components/Breadcrumbs.tsx`): Request History › account › Scan N
  - `scans.$scanId.tsx`: One scan's results: summary (with a link to Browse the account, and for Drive and Photos to Duplicates), then a page of files and folders (Drive, local), new messages (Gmail) or picked items (Google Photos)
  - `duplicates.tsx`: Duplicates, the fourth nav tab: reclaimable space, tabs for Files, Folders and Photos (likely), filters, and a page of groups that expand to their copies; `?kind=&source=&across=&min_size=&hide_same_physical=&page=`
  - `manage-data.tsx`: Manage data, the last nav tab: linked accounts and uploaded drives (by box), with confirmed deletions (`components/ui/Dialog.tsx`) that run as jobs the page watches
  - `oauth/glink.tsx`: OAuth callback handler
- **src/api/**: Backend API client functions
- **src/components/**: Reusable UI components; `components/ui/` holds the shared ones (Button, Card, Field, Input, Select, Checkbox, Tabs, Switch, Badge, Table, Pager, Icon, Spinner), and `Header.tsx` the app shell's top bar
- **src/App.css**: the design tokens (Tailwind v4 `@theme`: `canvas`, `surface`, `line`, `fg`, `muted`, `accent`, …), redefined for dark mode. Style with the tokens (`bg-surface`, `text-muted`), never raw palette colours (`docs/archive/ui-refresh.md`)
- **src/types/**: TypeScript type definitions for API contracts

### Data Flow

0. User logs in with an `agentserver` user (`POST /api/auth/login`) → HttpOnly session cookie. Scans and linked Google accounts belong to the user who made them; nobody sees another user's
1. User initiates OAuth flow → backend redirects to Google → callback stores refresh token in DB
2. User submits scan request via UI → backend creates scan record and starts collection
3. Collector services scan data sources → write results to PostgreSQL; a Drive scan also updates its account's living record (`drive_items`) and folder totals
4. For Gmail and Drive scans, progress is broadcast via SSE to connected clients
5. UI fetches scan results via REST API endpoints; Browse reads the Drive record, Gmail's messages, and agentserver's uploaded drives

## Development Commands

### Backend (Go)

Navigate to `be/` directory for all backend commands:

```bash
# Run the server locally
# Requires PostgreSQL running (update db/database.go host constant if needed)
# Backend listens on port 8090
go run .

# Build the backend
go build -o hdd

# Run with custom flags
# -frontend_url takes one origin or a comma-separated list; it sets CORS and
# the origins account linking may redirect back to
go run . -oauth_client_id=$OAUTH_CLIENT_ID -oauth_client_secret=$OAUTH_CLIENT_SECRET -frontend_url=http://localhost:5173

# -legacy_owner (default jyothri) is the agentserver user that scans and linked
# accounts from before users are given to at startup; empty skips it

# Build Docker image (from repository root)
docker build . -f ./build/Dockerfile -t jyothri/hdd-go-build

# Start full stack with docker-compose (from repository root)
# Prerequisites:
#   - Set OAUTH_CLIENT_ID, OAUTH_CLIENT_SECRET, FRONTEND_URL in build/docker-compose.yml
docker compose -f build/docker-compose.yml up
```

### Agent server (Go)

Go isn't installed on the dev box, so run these in the container matching `agent/server/go.mod`. Mount the repo root, not just `agent/server/`, so the `../wire` replace resolves:

```bash
# Tests; the Postgres ones skip unless AGENTSERVER_TEST_DB is set (each test gets its own schema)
docker run --rm --network host -v "$(git rev-parse --show-toplevel)":/src -w /src/agent/server \
  -e AGENTSERVER_TEST_DB='postgres://postgres:postgres@localhost:5432/agentserver_test?sslmode=disable' \
  golang:1.27.1 sh -c 'go test -race ./... && cd ../wire && go test -race ./...'

# Image (from the repository root)
docker build . -f agent/server/build/Dockerfile -t jyothri/bhandaar-agentserver

# Admin, inside the container: add a user (prompts for the password), list, run housekeeping once
agentserver user add --username NAME
agentserver user list
agentserver housekeeping
```

`agentserver serve` needs `AGENTSERVER_JWT_SECRET` (`openssl rand -base64 48`) and the same `DB_*` variables as the backend; it listens on `:8091`. CI (`agentserver-docker-image.yml`) tests against a Postgres service container and pushes `jyothri/bhandaar-agentserver` on merge to `main`.

### Frontend (React/Vite)

Navigate to `ui/` directory for all frontend commands:

```bash
# Install dependencies
npm install

# Start development server (default: http://localhost:5173)
npm run dev

# Build for production
npm run build

# Type check
npm run typecheck

# Run tests (Vitest; npm run test:watch to watch)
npm test

# Lint
npm run lint

# Preview production build
npm run preview
```

### Database Setup

PostgreSQL database is required. Tables are auto-created by the backend on startup. The backend's users are agentserver's, so `agentserver` must have started once against the same database first (it creates `agent_users`); until then the backend exits with an error naming `agent_users`. Add users with `agentserver user add`.

#### Database Configuration via Environment Variables

The backend supports the following database environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_HOST` | `hdd_db` | Database host (use `localhost` for local dev) |
| `DB_PORT` | `5432` | Database port |
| `DB_USER` | `hddb` | Database user |
| `DB_PASSWORD` | `""` (empty) | Database password |
| `DB_NAME` | `hdd_db` | Database name |
| `DB_SSL_MODE` | `disable` | SSL mode: `disable`, `require`, `verify-ca`, `verify-full` |

**Local Development with PostgreSQL:**
```bash
# Run PostgreSQL in Docker
docker run --name postgres -e POSTGRES_PASSWORD=postgres -d -p 5432:5432 postgres

# Start existing container
docker start postgres

# Run backend with environment variables
export DB_HOST=localhost
export DB_USER=postgres
export DB_PASSWORD=postgres
export DB_NAME=postgres
go run .
```

**Production Configuration:**
```bash
# Set environment variables for production
export DB_HOST=your-db-host.com
export DB_USER=your_db_user
export DB_PASSWORD=strong_secure_password
export DB_NAME=your_database_name
export DB_SSL_MODE=require
```

**Using .env file (recommended for local development):**
```bash
# Copy the example file
cd be/
cp .env.example .env

# Edit .env with your database configuration
# Then run the backend (if using a tool like godotenv)
```

**Access PostgreSQL shell:**
```bash
docker exec -it postgres /bin/bash
psql -U postgres
```

## OAuth Configuration

The application uses Google OAuth2 for accessing Drive, Gmail, and Photos. Setup:

Users link Google accounts from the Request page: it asks Google for `openid email` plus the service's scope (`include_granted_scopes=true`), and `/api/glink` identifies the account by the `id_token`'s `sub`, so linking an account again updates its row (`privatetokens.google_sub`) and keeps its `client_key`. The OAuth client is in "Testing": linked accounts' refresh tokens expire after 7 days (review item 2.11, deferred). By hand, for debugging:

1. Create OAuth client in Google Cloud Console
2. Configure OAuth consent screen with test users
3. Obtain authorization code via browser:
   ```
   https://accounts.google.com/o/oauth2/v2/auth?response_type=code&scope=https://www.googleapis.com/auth/drive.readonly%20https://www.googleapis.com/auth/gmail.readonly&client_id=CLIENT_ID&state=YOUR_CUSTOM_STATE&redirect_uri=https://local.jkurapati.com&access_type=offline&prompt=consent
   ```
4. Exchange authorization code for refresh token (see docs/archive/be/debug.md for curl commands)
5. Set environment variables: `OAUTH_CLIENT_ID`, `OAUTH_CLIENT_SECRET`, `REFRESH_TOKEN`

Google Cloud Storage is read through linked accounts too (`devstorage.read_only`, plus `cloudplatformprojects.readonly` to list projects); no service account key is used. The OAuth client's project needs the Cloud Resource Manager API enabled (and the Google Photos Picker API, for Photos).

## Important Notes

- **UI tests**: Vitest + Testing Library (jsdom), run with `npm test` from `ui/`. Unit tests sit next to their modules (`src/*.test.ts`); route-level tests go in `src/test/`, because the router generator treats every file under `src/routes` as a route. `src/test/renderRoute.tsx` renders a path through the real route tree. The test config in `vite.config.ts` fixes `VITE_BACKEND_URL` (`http://backend.test`) and `VITE_GOOGLE_CLIENT_ID`, so tests ignore local `.env` files. CI runs lint, typecheck and tests in the UI workflow's `check` job before building the image.
- **Backend tests**: A few, per package: `be/auth/password_test.go` (checks a hash made by agentserver), `be/web/auth_test.go` (session middleware, origin check, cookie flags), `be/db/users_test.go` (migration, legacy owner, sessions, lockout, per-user scoping; skips unless `BE_TEST_DB` is a `postgres://` URL, and uses a fresh schema per test, in a database of its own), `be/db/accounts_test.go` (linking and re-linking, services from scopes, history by account, the scan-account backfill; also needs `BE_TEST_DB`), `be/collect/common_test.go` (a scan's account name comes from the database), `be/web/api_test.go` (scan-request checks), `be/notification/hub_test.go`, `be/collect/gmail_test.go` (fake Gmail API via `httptest`), `be/collect/drive_test.go` (fake Drive API: folder walks, folder checks, progress, what goes to the Drive record), `be/db/driveitems_test.go` (the Drive record: add, update, rename, move, deletions in scope, none from filtered or failed scans; needs `BE_TEST_DB`), `be/db/photos_test.go` (the Library API's tables dropped, picking sessions one at a time, picked items and their summary; needs `BE_TEST_DB`), `be/collect/gcs_test.go` (fake Cloud Storage and Resource Manager: projects, buckets, both listing passes, states, Requester Pays, a failed bucket, progress), `be/db/gcs_test.go` (the Cloud Storage record across scans, scoped deletions, prefix totals, summaries, Browse's buckets and prefixes; needs `BE_TEST_DB`), `be/web/gcs_test.go` (the Cloud Storage routes and scan-request checks), `be/collect/photos_test.go` (fake Picker API: polling, sizing by HEAD or download, retries, fresh baseUrls, progress), `be/web/photos_test.go` (the Photos routes' checks), `be/db/browse_test.go` (Browse's queries and the totals cache against an agentserver-table fixture: rebuild on a new version, live totals before the first build; needs `BE_TEST_DB`), `be/web/browse_test.go` (Browse's owner checks), `be/db/duplicates_test.go` (the duplicates index against all three sources: left-out files, live only, same physical drive, filters, folders and the topmost rule, likely photos, same-named Drive folders, shared copies, rebuilds and the agent quiet period, failed builds; needs `BE_TEST_DB`), `be/web/duplicates_test.go` (filter and group parsing), `be/db/deletion_test.go` (each deletion removes exactly its target; jobs; needs `BE_TEST_DB`), `be/web/manage_data_test.go` (owner, running-scan and typed-name checks; revoking against a fake endpoint), `be/web/oauth_test.go` (account linking against a fake token endpoint, via the `tokenEndpoint` var), `be/web/cors_test.go` and `be/constants/constants_test.go`. Tests that change package-level config (`constants.FrontendUrl`, `tokenEndpoint`, `sessionUser`, `scanOwnedBy`, `linkAccount`, `accountFor`, `linkedAccount`, `googleAccountOwnedBy`, `agentDriveOwnedBy`, `startPhotosPick`, `cancelPhotosPick`, `pickerSession`, `pickedItems`, `photosPickerApi`, `minPollInterval`, `photosBackoff`, `baseUrlTTL`, `gcsOptions`, `gcsProjects`, `gcsBuckets`, `gcsScanBuckets`, the Cloud Storage record writers `upsertGcsObjects` and the like, `manage_data.go`'s handler vars and `revokeEndpoint`, and the duplicates builder's `dupAgentSeen`, `dupAgentQuiet` and `dupRetryAfter`) restore it with `t.Cleanup` and don't run in parallel. Run `go test ./...` from `be/`; add `-race` where cgo/gcc is available, e.g. `docker run --rm --network host -v "$PWD":/src -w /src -e BE_TEST_DB='postgres://postgres:postgres@localhost:5432/be_test?sslmode=disable' golang:1.23.5 go test -race ./...` (match the `toolchain` in `be/go.mod`; create `be_test` first). `flag.Parse()` runs in `main`, so tests use flag defaults; read flag values lazily, never from another package's `init()`. CI runs `go test -race ./...` against a Postgres service (backend workflow `test` job) before building the image.
- **Agent (driveagent) tests and releases**: tests sit next to their packages in `agent/client/internal/` (`internal/testutil` builds file trees; the `cmd/driveagent` tests use a temp `$DRIVEAGENT_LOCK_DIR`, so they don't collide with a real `driveagent`'s locks); run them in the Go container like the server's, with `-w /src/agent/client`. `agent/client/internal/version.Version` must be bumped in every PR that changes release-relevant files (everything under `agent/client/` and `agent/wire/` except Markdown, `_test.go`, `testdata/` and `agent/client/scripts/`); `agent/client/scripts/version-check.sh` enforces it in CI (`driveagent.yml`), with its own tests in `version-check_test.sh`. Merging to `main` releases `driveagent/v<Version>` with Linux and macOS tarballs; then raise `AGENTSERVER_LATEST_AGENT_VERSION` in the prod `.env`.
- **Database connection**: Configured via environment variables (see Database Setup section). Defaults: host `hdd_db`, port `5432`, user `hddb`, password empty, database `hdd_db`. For local development, set `DB_HOST=localhost` and configure credentials to match your PostgreSQL instance.
- **Backend API URL**: Read from `VITE_BACKEND_URL` via `ui/src/config.ts` (with `VITE_GOOGLE_CLIENT_ID`). `ui/.env.development` points at `http://localhost:8090`, `ui/.env.production` at `https://sm.jkurapati.com`; put local overrides in the gitignored `ui/.env.development.local`
- **Docs**: `docs/architecture.md` describes the system (components, API, data flow). `docs/codebase-review.md` lists open review items only; completed and won't-fix items, with the change log, are in `docs/archive/codebase-review-history.md` under the same numbers. When an item is done, move it to the archive and add a change-log row there. Local machine setup is in `docs/local-dev.md`. Design specs live in `docs/specs/`: the drive comparison agent in `agent/client/` and remote sync (the agent uploads its scan data to `agentserver`), both implemented and kept there as compact as-built references (start at `remote-sync.md`; code comments cite their sections by name, so keep those headings). Open remote-sync work is in `remote-sync-followups.md`, and the proposed cross-OS drive linking in `remote-sync-cross-os-linking.md`. The original specs and the completed implementation plan are in `docs/archive/remote-sync/`, the agent's design history in `docs/archive/drive-comparison-agent-history.md`, and the agent hardening (0.5.0: one state dir and version per machine, one scan per disk, machine binding) in `docs/archive/agent-hardening.md`, which code comments still cite by section. Google Drive scans from the Request page (per-service account linking, Drive query builder, results view; implemented in #33, Photos deferred) are in `docs/archive/request-drive-scans.md`, Browse (files and folders by Google account and by agent drive; implemented, with an "As built" section on where it differs) in `docs/archive/browse.md`, the UI refresh (tokens, shared components, app shell, phone layouts; implemented) in `docs/archive/ui-refresh.md`, Google Photos through the Picker API (the audit of the old Library API scanner, what a probe of the API found, the design, and an "As built" section; implemented in #39) in `docs/archive/photos-picker.md`, Google Cloud Storage scans (a living record per bucket, noncurrent and soft-deleted versions, storage classes; implemented in #40) in `docs/archive/gcs-scans.md`, self-service deletion (a drive uploaded from one box, one service's data of an account, disconnecting a Google account; on the Manage data page, as background jobs; implemented in #46) in `docs/archive/data-deletion.md`, and the duplicates report (identical files and folders within and across Drive, Cloud Storage, agent drives and Photos, likely copies of picked photos, and MD5 in `driveagent`; implemented in #47 and #49, with an "As built" section) in `docs/archive/duplicates.md`; code comments cite all seven by section too, except a few in `agent/` that still say `docs/specs/duplicates.md`. `docs/archive/be/` holds the backend's earlier issue plans and status notes (December 2025), plus its old `debug.md` and `roadmap.md` (March 2025), kept as written: paths inside the plans still say `be/docs/`.
- **Directory sizes**: local and Google Drive scans both save a row per folder below the one scanned (`is_dir`), with the total size and file count under it, recursively - see be/README.md "Kinks" section
- **CORS**: Backend configured to allow requests from frontend origin, with credentials (the session cookie). Requests that change state must also come from a `-frontend_url` origin or carry no `Origin`
- **Web auth**: no nginx basic auth; `be` authenticates web users itself with agentserver's users (`agent_users`, argon2id), sharing agentserver's login lockout (`agent_login_failures`). Sessions are rows of `web_sessions` (sha256 of the cookie's token), 30 days sliding; the cookie is `bhandaar_session`, HttpOnly, SameSite=Lax, Secure when any `-frontend_url` is https. `agentserver user disable` ends a user's web sessions at once. The UI's root route checks `GET /api/auth/me` before every page but `/login` and `/oauth/glink`, and any 401 sends it back to `/login`

## Key API Endpoints

All but health and login/logout need the session cookie.

- `POST /api/auth/login` - `{username, password}` → sets the session cookie; `401 INVALID_CREDENTIALS`, `429 TOO_MANY_ATTEMPTS`
- `POST /api/auth/logout` - Ends the session
- `GET /api/auth/me` - `{username}` of the logged-in user
- `POST /api/scans` - Submit scan request (Drive scans, like Gmail, can name a linked account by `ClientKey`; `GStorage` scans a project's buckets, or one bucket under a prefix; `GPhotos` answers 400 until the Picker API scans land)
- `GET /api/scans` - List all scans (paginated)
- `GET /api/scans/requests/{client_key}` - The scans of one linked account
- `GET /api/scans/accounts` - The accounts with scans (`{clientKey, displayName}`), for Request History
- `GET /api/scans/{scan_id}/summary` - A scan's details and totals (files or new messages, bytes, folders)
- `GET /api/scans/{scan_id}` - A page of a scan's files and folders, in tree order
- `GET /api/gmaildata/{scan_id}` - Get Gmail scan results
- `POST /api/photos/sessions` - `{clientKey}` → the pick (as below): starts picking in Google Photos (one pick at a time per user); the scan starts once the user has picked
- `GET /api/photos/sessions` - The user's pick that is waiting or scanning, or `null`
- `GET /api/photos/sessions/{session_key}` - `{sessionKey, pickerUri, state, scanId?, pickBy}`, `state` one of `waiting`, `scanning`, `done`, `expired`, `cancelled`
- `DELETE /api/photos/sessions/{session_key}` - Cancels a waiting pick
- `GET /api/photos/{scan_id}?page=` - A page (10) of a Photos scan's picked items
- `GET /api/gcs/{client_key}/projects` - The account's active Cloud projects (409 without the project-list scope)
- `GET /api/gcs/{client_key}/projects/{project}/buckets` - A project's buckets, with location, class, versioning, soft delete, Requester Pays
- `GET /api/gcs/{scan_id}` - What a Cloud Storage scan did with each bucket, with totals by state and class
- `GET /api/accounts` - List linked Google accounts, with the `services` each has granted (`gmail`, `drive`)
- `DELETE /api/scans/{scan_id}` - Delete scan
- `GET /api/browse/sources` - What the user can browse: linked accounts (with each service's totals) and their agents' drives
- `GET /api/browse/google/{client_key}/drive/children?folder=&page=` - A page (200) of a folder of the account's Drive record; empty `folder` for the roots (My Drive, `shared-with-me`)
- `GET /api/browse/agent/{id}/children?folder=&page=` - The same for an agent drive, `folder` a path relative to its root
- `GET /api/browse/agent/{id}/status` - An agent drive's last scan, last sync, physical drive
- `GET /api/browse/google/{client_key}/gmail/messages?sort=size|date&page=` - A page (50) of the account's messages across its Gmail scans
- `GET /api/browse/google/{client_key}/photos/items?sort=size|date&page=` - A page (50) of the account's picked Photos across its scans, each item once (from its latest scan)
- `GET /api/browse/google/{client_key}/gcs/children?folder=&page=` - A page (200) of the account's Cloud Storage record: buckets for an empty `folder`, else `<bucket>/<prefix>`'s subfolders and object versions, with totals by state and class
- `GET /api/duplicates/summary` - Reclaimable bytes, groups by kind, bytes per source, uncomparable folders, `built_at`, `updating`
- `GET /api/duplicates/groups?kind=file|folder|photo&source=&across=1&min_size=&hide_same_physical=1&page=` - A page (50) of groups, largest reclaimable first, each with up to 10 copies
- `GET /api/duplicates/members?kind=&key=&page=` - A page (200) of one group's copies; groups are named by kind and key, which survive rebuilds
- `GET /api/manage-data` - The user's linked accounts and uploaded drives, with what deleting each removes
- `DELETE /api/agent-drives/{id}` - Delete a drive as uploaded from one box → `202` job
- `DELETE /api/accounts/{client_key}/{service}` - Delete one service's data of an account (`gmail`, `drive`, `gcs`, `photos`: its scans and records) → `202` job; `409` while one of its scans runs
- `DELETE /api/accounts/{client_key}` - `{confirm: "<account name>"}`: revoke at Google and delete everything recorded for the account → `202` job; `400` if the name doesn't match
- `GET /api/deletions/{id}` - A deletion job's status and counts
- `GET /oauth/authorize` - Initiate OAuth flow
- `GET /oauth/callback` - OAuth callback handler
- `GET /events` - SSE endpoint for scan progress
