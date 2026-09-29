# Bhandaar

Bhandaar is a storage analyzer. It scans your data where it lives and shows you what takes up the space:

- **Google Drive**: the whole Drive, or one folder with or without its subfolders
- **Gmail**: messages and their sizes
- **Local drives**: via `driveagent`, which scans and compares drives on your machines and uploads the results
- **Google Photos**: the photos and videos you pick in Google Photos, with their sizes (Google no longer lets apps list a whole library; [how it works](docs/archive/photos-picker.md))

Scans and linked Google accounts belong to the user who made them. The web app's **Browse** page shows a folder tree for any linked Google account or uploaded agent drive, with each folder's total size and file count, and an account's Gmail messages and picked Google Photos, largest first.

## Components

| Directory | What it is |
|---|---|
| [`be/`](be/README.md) | Go web backend (`:8090`): REST API, web login, Google account linking (OAuth), the scan collectors, and progress over Server-Sent Events |
| [`ui/`](ui/README.md) | React + TypeScript single-page app (Vite, TanStack Router and Query, Tailwind) |
| [`agent/client/`](agent/client/README.md) | `driveagent`, the CLI that scans, compares and reports on local drives, and syncs its scan data to `agentserver` |
| `agent/server/` | `agentserver` (`:8091`), the service that receives `driveagent` uploads; it also owns the users both it and the backend log in with |
| `agent/wire/` | Request and response types shared by `driveagent` and `agentserver` |
| [`docs/`](docs/architecture.md) | Architecture, local setup, design specs and review notes |

The backend and `agentserver` share one PostgreSQL database.

## Getting started

You need PostgreSQL, Go (see each module's `go.mod`) and Node (see `ui/.nvmrc`).

1. **Start PostgreSQL**, for example:
   ```bash
   docker run --name postgres -e POSTGRES_PASSWORD=postgres -d -p 5432:5432 postgres
   ```
2. **Run `agentserver` once against it and add a user.** The backend logs users in with `agentserver`'s users and won't start until its tables exist:
   ```bash
   docker run -d --name agentserver-local --network host -e DB_HOST=localhost -e DB_USER=postgres \
     -e DB_PASSWORD=postgres -e DB_NAME=postgres -e AGENTSERVER_JWT_SECRET="$(openssl rand -base64 48)" \
     jyothri/bhandaar-agentserver
   docker exec -it agentserver-local agentserver user add --username NAME
   ```
3. **Start the backend:**
   ```bash
   cd be && DB_HOST=localhost DB_USER=postgres DB_PASSWORD=postgres DB_NAME=postgres \
     go run . -frontend_url=http://localhost:5173
   ```
   Linking Google accounts also needs `-oauth_client_id` and `-oauth_client_secret` from a Google Cloud OAuth client, and `GOOGLE_APPLICATION_CREDENTIALS`.
4. **Start the UI** and open http://localhost:5173:
   ```bash
   cd ui && npm ci && npm run dev
   ```

The database settings (`DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSL_MODE`) and the rest of the commands are in [`CLAUDE.md`](CLAUDE.md); the full local setup is in [`docs/local-dev.md`](docs/local-dev.md).

## Tests

```bash
cd be && go test ./...                 # Postgres tests skip unless BE_TEST_DB is set
cd ui && npm run lint && npm run typecheck && npm test
cd agent/server && go test ./...       # Postgres tests skip unless AGENTSERVER_TEST_DB is set
cd agent/client && go test ./...
```

CI (`.github/workflows/`) runs these, then builds the backend, UI and `agentserver` Docker images (pushed to Docker Hub on merge to `main`). Merging a `driveagent` version bump to `main` publishes a release with Linux and macOS binaries.

## Documentation

- [`docs/architecture.md`](docs/architecture.md): components, API and data flow
- [`docs/specs/`](docs/specs/): design specs, including the drive comparison agent, remote sync, and Browse
- [`docs/codebase-review.md`](docs/codebase-review.md): open review items

## License

MIT; see [`be/LICENSE`](be/LICENSE).
