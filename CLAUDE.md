# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Bhandaar is a storage analyzer application that scans and analyzes data across multiple sources:
- Local file systems
- Google Drive
- Gmail mailboxes
- Google Photos

The application consists of:
- **Backend (be/)**: Go server with REST API, OAuth integration, and data collection services
- **Frontend (ui/)**: React + TypeScript SPA using Vite, TanStack Router, and TanStack Query
- **Agent (agent/)**: three sibling Go modules for local drive tracking and remote sync:
  - `agent/client/`: `driveagent`, the CLI that scans and compares drives (`docs/specs/drive-comparison-agent.md`)
  - `agent/server/`: `agentserver`, the hosted service that receives `driveagent` uploads (health, handshake, login/refresh so far; `docs/specs/remote-sync-server.md`)
  - `agent/wire/`: the request/response types both sides share; standard library only

## Architecture

### Backend Structure (`be/`)

- **main.go**: Entry point that initializes logging and starts the web server
- **web/**: HTTP server and API handlers
  - `web_server.go`: Server initialization with CORS and routing setup
  - `api.go`: REST API endpoints for scans, accounts, and data retrieval
  - `oauth.go`: Google OAuth2 flow implementation (authorization and callback)
  - `sse.go`: Server-Sent Events for real-time scan progress updates
- **collect/**: Data collection modules for different sources
  - `local.go`: Local filesystem scanning
  - `drive.go`: Google Drive scanning
  - `gmail.go`: Gmail mailbox scanning with SSE progress updates
  - `photos.go`: Google Photos scanning
  - `common.go`: Shared collection utilities
- **db/**: Database layer using PostgreSQL
  - `database.go`: Database initialization, schema creation, and CRUD operations
  - Tables: `Scans`, `ScanData`, `messagemetadata`, `PhotoMetadata`, `PhotoAlbums`
- **notification/**: SSE hub for broadcasting scan progress events
- **constants/**: Application constants and configuration

### Frontend Structure (`ui/`)

- **src/routes/**: TanStack Router route components
  - `index.tsx`: Home page/landing
  - `request.tsx`: Main scan request form and results display
  - `requests.tsx`: List view of scan requests by account
  - `oauth/glink.tsx`: OAuth callback handler
- **src/api/**: Backend API client functions
- **src/components/**: Reusable UI components
- **src/types/**: TypeScript type definitions for API contracts

### Data Flow

1. User initiates OAuth flow → backend redirects to Google → callback stores refresh token in DB
2. User submits scan request via UI → backend creates scan record and starts collection
3. Collector services scan data sources → write results to PostgreSQL
4. For Gmail scans, progress is broadcast via SSE to connected clients
5. UI fetches scan results via REST API endpoints

## Development Commands

### Backend (Go)

Navigate to `be/` directory for all backend commands:

```bash
# Run the server locally
# Requires environment variables:
#   - GOOGLE_APPLICATION_CREDENTIALS (path to GCP credentials JSON)
# Requires PostgreSQL running (update db/database.go host constant if needed)
# Backend listens on port 8090
go run .

# Build the backend
go build -o hdd

# Run with custom flags
# -frontend_url takes one origin or a comma-separated list; it sets CORS and
# the origins account linking may redirect back to
go run . -oauth_client_id=$OAUTH_CLIENT_ID -oauth_client_secret=$OAUTH_CLIENT_SECRET -frontend_url=http://localhost:5173

# Build Docker image (from repository root)
docker build . -f ./build/Dockerfile -t jyothri/hdd-go-build

# Start full stack with docker-compose (from repository root)
# Prerequisites:
#   - Google application credentials at ~/keys/gae_creds.json
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

PostgreSQL database is required. Tables are auto-created by the backend on startup.

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

1. Create OAuth client in Google Cloud Console
2. Configure OAuth consent screen with test users
3. Obtain authorization code via browser:
   ```
   https://accounts.google.com/o/oauth2/v2/auth?response_type=code&scope=https://www.googleapis.com/auth/drive.readonly%20https://www.googleapis.com/auth/gmail.readonly&client_id=CLIENT_ID&state=YOUR_CUSTOM_STATE&redirect_uri=https://local.jkurapati.com&access_type=offline&prompt=consent
   ```
4. Exchange authorization code for refresh token (see docs/archive/be/debug.md for curl commands)
5. Set environment variables: `OAUTH_CLIENT_ID`, `OAUTH_CLIENT_SECRET`, `REFRESH_TOKEN`

For Cloud Storage access, set `GOOGLE_APPLICATION_CREDENTIALS` to service account key file path.

## Important Notes

- **UI tests**: Vitest + Testing Library (jsdom), run with `npm test` from `ui/`. Unit tests sit next to their modules (`src/*.test.ts`); route-level tests go in `src/test/`, because the router generator treats every file under `src/routes` as a route. `src/test/renderRoute.tsx` renders a path through the real route tree. The test config in `vite.config.ts` fixes `VITE_BACKEND_URL` (`http://backend.test`) and `VITE_GOOGLE_CLIENT_ID`, so tests ignore local `.env` files. CI runs lint, typecheck and tests in the UI workflow's `check` job before building the image.
- **Backend tests**: A few, per package: `be/notification/hub_test.go`, `be/collect/gmail_test.go` (fake Gmail API via `httptest`), `be/web/oauth_test.go` (account linking against a fake token endpoint, via the `tokenEndpoint` var), `be/web/cors_test.go` and `be/constants/constants_test.go`. Tests that change package-level config (`constants.FrontendUrl`, `tokenEndpoint`) restore it with `t.Cleanup` and don't run in parallel. Run `go test ./...` from `be/`; add `-race` where cgo/gcc is available, e.g. `docker run --rm -v "$PWD":/src -w /src golang:1.23.5 go test -race ./...` (match the `toolchain` in `be/go.mod`). `flag.Parse()` runs in `main`, so tests use flag defaults; read flag values lazily, never from another package's `init()`. CI runs `go test -race ./...` (backend workflow `test` job) before building the image.
- **Agent (driveagent) tests and releases**: tests sit next to their packages in `agent/client/internal/` (`internal/testutil` builds file trees); run them in the Go container like the server's, with `-w /src/agent/client`. `agent/client/internal/version.Version` must be bumped in every PR that changes release-relevant files (everything under `agent/client/` and `agent/wire/` except Markdown, `_test.go`, `testdata/` and `agent/client/scripts/`); `agent/client/scripts/version-check.sh` enforces it in CI (`driveagent.yml`), with its own tests in `version-check_test.sh`. Merging to `main` releases `driveagent/v<Version>` with Linux and macOS tarballs. The planned version sequence is in the implementation plan's "Agent versions" table.
- **Database connection**: Configured via environment variables (see Database Setup section). Defaults: host `hdd_db`, port `5432`, user `hddb`, password empty, database `hdd_db`. For local development, set `DB_HOST=localhost` and configure credentials to match your PostgreSQL instance.
- **Backend API URL**: Read from `VITE_BACKEND_URL` via `ui/src/config.ts` (with `VITE_GOOGLE_CLIENT_ID`). `ui/.env.development` points at `http://localhost:8090`, `ui/.env.production` at `https://sm.jkurapati.com`; put local overrides in the gitignored `ui/.env.development.local`
- **Docs**: `docs/architecture.md` describes the system (components, API, data flow). `docs/codebase-review.md` lists open review items only; completed and won't-fix items, with the change log, are in `docs/archive/codebase-review-history.md` under the same numbers. When an item is done, move it to the archive and add a change-log row there. Local machine setup is in `docs/local-dev.md`. Design specs live in `docs/specs/`: the drive comparison agent in `agent/client/` (implemented) and remote sync, in which the agent uploads scan data to the new `agentserver` service (in progress: the server skeleton is built; start at `remote-sync.md`, and `remote-sync-implementation-plan.md` for status). Their design history is in `docs/archive/`. `docs/archive/be/` holds the backend's earlier issue plans and status notes (December 2025), plus its old `debug.md` and `roadmap.md` (March 2025), kept as written: paths inside the plans still say `be/docs/`.
- **Known issue**: Directory size calculation differs between local scans (recursive) and cloud scans (directory-level only) - see be/README.md "Kinks" section
- **CORS**: Backend configured to allow requests from frontend origin

## Key API Endpoints

- `POST /api/scans` - Submit scan request
- `GET /api/scans` - List all scans (paginated)
- `GET /api/scans/requests/{account_key}` - Get scan requests for account
- `GET /api/scans/{scan_id}` - Get scan data
- `GET /api/gmaildata/{scan_id}` - Get Gmail scan results
- `GET /api/photos/{scan_id}` - Get Photos scan results
- `GET /api/accounts` - List OAuth-authenticated accounts
- `DELETE /api/scans/{scan_id}` - Delete scan
- `GET /oauth/authorize` - Initiate OAuth flow
- `GET /oauth/callback` - OAuth callback handler
- `GET /events` - SSE endpoint for scan progress
