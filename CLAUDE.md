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
go run . -oauth_client_id=$OAUTH_CLIENT_ID -oauth_client_secret=$OAUTH_CLIENT_SECRET -frontend_url=http://localhost:5173

# Build Docker image (from repository root)
docker build . -f ./build/Dockerfile -t jyothri/hdd-go-build

# Start full stack with docker-compose (from repository root)
# Prerequisites:
#   - Google application credentials at ~/keys/gae_creds.json
#   - Set OAUTH_CLIENT_ID, OAUTH_CLIENT_SECRET, FRONTEND_URL in build/docker-compose.yml
docker compose -f build/docker-compose.yml up
```

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
npx tsc -b

# Lint
npm run lint

# Preview production build
npm run preview
```

### Database Setup

PostgreSQL database is required. Tables are auto-created by the backend on startup.

```bash
# Run PostgreSQL in Docker
docker run --name postgres -e POSTGRES_PASSWORD=postgres -d -p 5432:5432 postgres

# Start existing container
docker start postgres

# Access PostgreSQL shell
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
4. Exchange authorization code for refresh token (see be/debug.md for curl commands)
5. Set environment variables: `OAUTH_CLIENT_ID`, `OAUTH_CLIENT_SECRET`, `REFRESH_TOKEN`

For Cloud Storage access, set `GOOGLE_APPLICATION_CREDENTIALS` to service account key file path.

## Important Notes

- **No tests**: The codebase currently has no test files
- **Database connection**: Default connection in `be/db/database.go` expects host `hdd_db`, port `5432`, user/password `hddb/hddb`, database `hdd_db`. Update constants if using different configuration (e.g., `localhost` for local development)
- **Backend API URL**: Hardcoded in `ui/src/api/index.ts` as `https://sm.jkurapati.com`
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
