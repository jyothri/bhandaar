# Bhandaar Architecture

## System Overview

Bhandaar is a storage analyzer application that scans and analyzes data across multiple sources: local filesystems, Google Drive, Gmail, Google Photos, and Google Cloud Storage.

## Architecture Diagram

The whole system as built.

```mermaid
graph TB
    subgraph Clients
        Browser["Web browser"]
        Agent["driveagent CLI<br/>Linux / macOS machines<br/>local state.db"]
    end

    subgraph "Prod box"
        Nginx["nginx<br/>TLS, rate limits (web_login, agent_auth)"]
        UI["UI: React SPA<br/>Vite, TanStack Router + Query<br/>Browse, Request, Request History,<br/>Duplicates, scan results"]

        subgraph BE["be: Go web app :8090"]
            MW["Middleware<br/>CORS, body limits,<br/>session check + origin check"]
            AuthH["auth.go<br/>/api/auth/login, logout, me"]
            ApiH["api.go, browse.go, photos.go, gcs.go,<br/>duplicates.go<br/>/api/scans, /api/accounts,<br/>/api/scans/{id}/summary, results,<br/>/api/browse, /api/photos, /api/gcs,<br/>/api/duplicates"]
            OAuthH["oauth.go<br/>/api/glink: link accounts"]
            SseH["sse.go<br/>/sse/scanprogress"]
            Collect["Collectors<br/>drive.go, gmail.go, local.go,<br/>photos.go (polls picks), gcs.go<br/>one scan at a time"]
            Hub["notification hub<br/>progress events"]
            DBL["db layer<br/>database.go, users.go, accounts.go,<br/>results.go, driveitems.go, browse.go,<br/>photos.go, gcs.go, totals.go (folder totals checker),<br/>duplicates.go (duplicates builder)"]
        end

        subgraph AS["agentserver: Go :8091"]
            AgentAPI["/agent/v1: handshake, login (JWT),<br/>drives, change batches"]
            House["housekeeping"]
        end

        subgraph PG["PostgreSQL :5432 (one database)"]
            BeTables[("be tables<br/>scans, scanmetadata, scandata,<br/>messagemetadata, privatetokens,<br/>web_sessions, drive_items, drive_accounts,<br/>browse_folder_totals, browse_totals_state,<br/>photos_picker_sessions, photos_picked_items,<br/>gcs_buckets, gcs_objects, gcs_prefix_totals,<br/>gcs_scan_buckets, dup_groups, dup_members,<br/>dup_state")]
            AgentTables[("agentserver tables<br/>agent_users, agent_login_failures,<br/>agent_agents, agent_drives, agent_files,<br/>agent_dir_listings, agent_scan_runs, …")]
        end
    end

    subgraph Google
        GAuth["Google OAuth 2.0 / OpenID<br/>consent + token endpoint"]
        GDrive["Google Drive API<br/>drive.metadata.readonly"]
        GMail["Gmail API<br/>gmail.readonly"]
        GPhotos["Google Photos Picker API<br/>photospicker.mediaitems.readonly"]
        GCS["Cloud Storage JSON API<br/>devstorage.read_only"]
        GRM["Cloud Resource Manager API<br/>cloudplatformprojects.readonly"]
    end

    Browser -->|HTTPS| Nginx
    Agent -->|"HTTPS /agent/"| Nginx
    Nginx -->|"/"| UI
    Nginx -->|"/api, /sse"| MW
    Nginx -->|"/agent/"| AgentAPI
    Browser -.->|"consent redirect<br/>(openid email + service scope)"| GAuth
    Browser -.->|"picks items<br/>(pickerUri window)"| GPhotos

    MW --> AuthH
    MW --> ApiH
    MW --> OAuthH
    MW --> SseH
    ApiH -->|start scan| Collect
    Collect -->|progress| Hub
    Hub -->|"owner's scans only"| SseH

    AuthH --> DBL
    ApiH --> DBL
    OAuthH --> DBL
    Collect --> DBL
    DBL --> BeTables
    DBL -.->|"reads users, shares<br/>login lockout"| AgentTables
    AgentAPI --> AgentTables
    House --> AgentTables

    OAuthH -->|"code exchange, id_token"| GAuth
    Collect -->|"files.list / files.get"| GDrive
    Collect -->|"messages.list / get"| GMail
    Collect -->|"sessions, mediaItems.list,<br/>HEAD each item's bytes"| GPhotos
    Collect -->|"buckets.list, objects.list<br/>(versions, softDeleted)"| GCS
    Collect -->|"projects.search"| GRM

    classDef client fill:#61dafb,stroke:#333,color:#000
    classDef edge fill:#999,stroke:#333,color:#fff
    classDef backend fill:#00add8,stroke:#333,color:#fff
    classDef agentsrv fill:#6b8e23,stroke:#333,color:#fff
    classDef database fill:#336791,stroke:#333,color:#fff
    classDef external fill:#4285f4,stroke:#333,color:#fff

    class Browser,Agent,UI client
    class Nginx edge
    class MW,AuthH,ApiH,OAuthH,SseH,Collect,Hub,DBL backend
    class AgentAPI,House agentsrv
    class BeTables,AgentTables database
    class GAuth,GDrive,GMail,GPhotos,GCS,GRM external
```

- **Web users** log in to `be` with `agentserver`'s users (`agent_users`), sharing its login lockout. Sessions are `web_sessions` rows behind an HttpOnly cookie (see [Web authentication](#web-authentication)).
- **Scans and linked Google accounts** belong to the user who made them.
- **`be` and `agentserver` never call each other.** They only share the database, where each owns its own tables.
- **The local scanner** walks the filesystem of the machine `be` runs on. `driveagent` is how other machines' disks are scanned.

## Component Details

### 1. Frontend Layer (React + TypeScript)

**Technology Stack:**
- React 19 with TypeScript
- Vite (build tool)
- TanStack Router (routing)
- TanStack Query (data fetching)
- Tailwind CSS v4 (styling): design tokens in `App.css` (`@theme`), light and dark from the system theme; no component library ([archive/ui-refresh.md](archive/ui-refresh.md))

**Key Components:**
- `/routes/__root.tsx` and `/components/Header.tsx` - The app shell: a top bar with the Bhandaar name, the nav tabs (Browse · Request · Request History · Duplicates · Manage data; a menu on phones) and the user menu (Log out); page titles `<page> · Bhandaar`. Login and the OAuth callback show just the name
- `/components/ui/` - The shared components (Button, Card, Field, Input, Select, Checkbox, Tabs, Switch, Badge, Table, Pager, Icon, Spinner), styled only with the tokens; `Table` stacks rows into cards on phones
- `/routes/index.tsx` - Browse: source picker, service tabs (Google Drive · Gmail · Google Photos · Google Cloud Storage), a summary card per source, and the folder tree (`/components/FolderTree.tsx`: file-type icons from `fileTypes.ts`, size bars for each entry's share of its folder), the account's messages, or its picked photos and videos (`/components/PickedItemsTable.tsx`). Google Cloud Storage uses the folder tree too: buckets, then prefixes as folders (folder IDs `<bucket>/<prefix>`), every object version a row, with a card for the noncurrent and soft-deleted totals and the live bytes by class that the tree hides
- `/routes/request.tsx` - Scan request form for Gmail, Google Drive, Google Photos and Google Cloud Storage (`?type=gmail|drive|photos|gcs`). An account without the chosen service gets a "Grant … access" button, which links it again for that service with Google's `login_hint`; the service being linked is kept in `sessionStorage` across the round trip. Drive fields build the Drive query (`driveQuery.ts`), which "Edit query" lets you replace, plus an optional folder (link or ID) with or without subfolders. Photos has no form: **Pick in Google Photos** (`/components/PhotosPick.tsx`) opens Google Photos to pick, then follows the pick until its scan is done (see [Photos Pick Flow](#photos-pick-flow)). Google Cloud Storage (`/components/GcsFields.tsx`): a project (listed, or typed when the account can't list its projects), all its buckets or one, an optional prefix, and whether to include noncurrent versions and soft-deleted objects
- `/routes/requests.tsx` - List view of scan requests by account, with readable scan types; each scan ID links to its results. The selected account is in the URL (`?account=<client_key>`), so links and Back return to it
- `/components/Breadcrumbs.tsx` - The trail under the nav tabs on Request History and scan pages: `Request History › <account> › Scan N`. A scan's trail always goes through its account (the summary's `client_key`), however the scan was opened, and the Request History tab stays highlighted on it
- `/routes/scans.$scanId.tsx` - One scan's results: its summary, then 10 rows a page: files and folders with their folder, size, file count, modified time and MD5, linked to Drive (Drive, local), new messages (Gmail), picked items with when taken, dimensions, camera and size (Google Photos), or each bucket's status and totals by state and class, linked into Browse (Google Cloud Storage)
- `/routes/duplicates.tsx` - Duplicates: reclaimable space and per-source totals, tabs for identical files, identical folders and likely photo copies, filters (source, across sources, minimum size, hide same physical drive), and a page of groups, each expanding to its copies with links into Browse, Drive or the Cloud console. See [specs/duplicates.md](specs/duplicates.md)
- `/routes/oauth/glink.tsx` - OAuth callback handler
- `/api/index.ts` - Backend API client
- `/components/ScanProgress.tsx` - Real-time progress display
- `/components/hooks/useSse.ts` - SSE connection hook

### 2. Backend Layer (Go)

#### Web Server (`web/`)
- **Framework:** Gorilla Mux router
- **Port:** 8090
- **Features:** CORS enabled, RESTful API, OAuth2, SSE

#### API Endpoints (`web/api.go`)

Every route but health and login/logout needs a logged-in user (see [Web authentication](#web-authentication)), and only shows that user's scans and linked accounts; another user's scan answers 404.

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/health` | GET | Health check |
| `/api/auth/login` | POST | Log in; sets the session cookie |
| `/api/auth/logout` | POST | End the session |
| `/api/auth/me` | GET | The logged-in user |
| `/api/scans` | POST | Submit scan request; a Gmail or Drive request's account must have granted that service, and its query must fit 2000 characters (else 400, before any scan is created). `GPhotos` answers 400: Photos scans start from a pick. `GStorage` (`{ClientKey, ProjectId, Bucket, Prefix, Versions, SoftDeleted}`) checks the project or bucket with Google first: one the account can't see is a 403 or 404, with no scan |
| `/api/scans` | GET | List all scans (paginated) |
| `/api/scans/requests/{client_key}` | GET | The scans of one linked account, newest first |
| `/api/scans/{scan_id}/summary` | GET | A scan's details and totals: file (or new-message, or picked-item) count and bytes, folder count, and Photos items without a size |
| `/api/scans/{scan_id}` | GET | A page (10) of a scan's files and folders, in tree order (paths compared a segment at a time), with `file_id` for Drive |
| `/api/scans/{scan_id}` | DELETE | Delete scan |
| `/api/gmaildata/{scan_id}` | GET | A page (10) of a Gmail scan's new messages, newest first |
| `/api/photos/sessions` | POST | `{clientKey}`: start a Google Photos pick for an account that has granted Photos (one pick at a time per user, else 409); answers the pick |
| `/api/photos/sessions` | GET | The user's pick that is waiting or scanning, or `null` |
| `/api/photos/sessions/{session_key}` | GET | A pick: `{sessionKey, pickerUri, state, scanId?, pickBy}`, `state` one of `waiting`, `scanning`, `done`, `expired`, `cancelled` |
| `/api/photos/sessions/{session_key}` | DELETE | Cancel a waiting pick (409 once it's past `waiting`) |
| `/api/photos/{scan_id}` | GET | A page (10) of a Photos scan's picked items |
| `/api/gcs/{client_key}/projects` | GET | The account's active Cloud projects, `[{projectId, displayName}]`; 409 when it didn't grant the project list |
| `/api/gcs/{client_key}/projects/{project}/buckets` | GET | The project's buckets: location, default class, versioning, soft-delete retention, Requester Pays |
| `/api/gcs/{scan_id}` | GET | What a Cloud Storage scan did with each bucket: status, error, totals by state and class |
| `/api/accounts` | GET | List linked Google accounts: `clientKey`, `displayName`, `services` (`gmail`, `drive`, `photos`, `gcs`), `loginHint` (the Google account ID, when known) and `canListProjects` (Cloud Storage) |
| `/api/scans/accounts` | GET | The accounts with scans, as `{clientKey, displayName}`, each named by its newest scan |
| `/api/browse/sources` | GET | What the user can browse: each linked account, with each service's grant and recorded totals, and each of their agents' drives (`<drive_id> (<hostname>)`), with totals, last sync and linked physical drive |
| `/api/browse/google/{client_key}/drive/children` | GET | A page (200) of a folder of the account's Drive record (`?folder=<id>`; empty for the roots, My Drive and Shared with me): its path, subfolders with totals, then files, each largest first |
| `/api/browse/agent/{id}/children` | GET | The same for an agent drive (`?folder=<path>`, relative to the drive's root) |
| `/api/browse/agent/{id}/status` | GET | An agent drive's last scan run, last sync, physical drive, and whether its folder totals are being rebuilt |
| `/api/browse/google/{client_key}/gmail/messages` | GET | A page (50) of the account's messages across its Gmail scans (`?sort=size|date`), each with the scan that found it |
| `/api/browse/google/{client_key}/photos/items` | GET | A page (50) of the account's picked Photos across its scans, each item once as its latest scan found it (`?sort=size|date`, date being when taken), with that scan |
| `/api/browse/google/{client_key}/gcs/children` | GET | A page (200) of the account's Cloud Storage record: its buckets (`?folder=` empty), or a prefix's subfolders then object versions (`?folder=<bucket>/<prefix>`), each largest first, with the folder's totals by state and class |
| `/api/duplicates/summary` | GET | The user's duplicates: reclaimable bytes, groups by kind, bytes per source, folders that couldn't be compared, when the index was built and whether it's being rebuilt |
| `/api/duplicates/groups` | GET | A page (50) of groups, largest reclaimable first (`?kind=file|folder|photo&source=&across=1&min_size=&hide_same_physical=1&page=`), each with up to 10 copies |
| `/api/duplicates/members` | GET | A page (200) of one group's copies (`?kind=&key=&page=`: groups are named by kind and key, which survive rebuilds); a group not in the user's index answers 404 |

Browse routes answer 404 for a source that isn't the user's. See [archive/browse.md](archive/browse.md).

Duplicates are read from an index of each user's, rebuilt by a goroutine (`db.DuplicatesBuilder`) when a scan or deletion ends, and every 10 minutes when its inputs changed. See [specs/duplicates.md](specs/duplicates.md).

#### Linking Google accounts (`web/oauth.go`)
- The UI sends the user to Google asking for `openid email` plus a service's scope, with `include_granted_scopes=true` and `access_type=offline`, and Google returns to `/oauth/glink`, which hands the code to `GET /api/glink`.
- `/api/glink` exchanges the code, and identifies the Google account by the `id_token`'s `sub` (its email gives the masked display name). It stores the scopes Google actually granted. The linked account is the user's row with that `sub`, else the user's newest row from before `sub`s were recorded with the same display name, else a new row; an existing row keeps its `client_key`. Then it redirects to `/request?account=<client_key>`.
- See [archive/request-drive-scans.md](archive/request-drive-scans.md#identity-and-re-linking-beweboauthgo).
- A Google scan is recorded (`scanmetadata`) under its linked account's `client_key` and `display_name`, both read from `privatetokens`, never taken from the request. Request History groups by `client_key`, since masked names can collide; the UI adds the start of the key to names two accounts share. Scans from before `client_key` was recorded get their user's newest account of the same name, at startup.

#### Server-Sent Events (`web/sse.go`)
- `/events` - Real-time scan progress updates
- Broadcasts progress for Gmail, Drive, Photos and Cloud Storage scans, to the scan's owner only

#### Web authentication

`be` has no users of its own: it logs web users in against `agentserver`'s `agent_users` table in the shared database (argon2id hashes, verified by `be/auth`), and shares its login lockout (`agent_login_failures`: 10 failures per username and client IP in 15 minutes). Users are managed only with `agentserver user …`; `user disable` also ends web sessions at once. A login creates a row in `web_sessions` (keyed on sha256 of a random token) and sets the `bhandaar_session` cookie (HttpOnly, SameSite=Lax, Secure when the UI is on https), valid for 30 days after last use. Requests that change state must come from a `-frontend_url` origin, since SameSite=Lax still lets sibling subdomains send the cookie. nginx no longer asks for basic auth.

`scans.user_id` and `privatetokens.user_id` reference `agent_users`. Rows from before users are given to `-legacy_owner` (default `jyothri`) at startup, once that user exists.

#### Collection Services (`collect/`)

**Local Scanner (`local.go`):**
- Scans local filesystem
- Recursive directory traversal
- Calculates sizes including subdirectories
- Stores: filename, path, size, modification time, MD5 hash

**Drive Scanner (`drive.go`):**
- Uses the Google Drive API with a linked account's refresh token, and the `drive.metadata.readonly` scope: it reads metadata only
- Lists the files matching the request's `QueryString` (Drive's `q`), across the whole Drive; folders are skipped, and Google Docs files have no size or MD5
- Or, with `FolderId`, one folder: breadth first, one list call per folder, into subfolders when `Recursive`. The query applies to files; every subfolder is walked whether it matches or not, except trashed ones, and shortcuts aren't followed. The folder is checked (`files.get`) before the scan is recorded: a bad one is a 400, with no scan. `scanmetadata.search_path` records it as `<path> (<id>)`, plus ` and subfolders`
- Saves, at the end, a row per folder below the scanned one (`is_dir`, with its Drive ID), with the total size and file count under it, as local scans do. Totals are tracked by folder ID, since a folder name can contain `/`. A whole-Drive scan saves only folders with scanned files under them; a folder scan, every subfolder it walked
- Stores: file name, its full folder path as the Drive UI shows it (`My Drive/A/Desktop/Qns/q1.pdf`, or `Shared with me/<shared folder>/…`; a folder scan looks up its folder's parents first, one call per level), the Drive file ID (`scandata.file_id`), size, modification time, MD5. A whole-Drive scan lists every folder once first, to build the paths
- Real-time progress updates via SSE (files so far), like Gmail
- A scan of a linked account also updates the account's living record, `drive_items` (one row per file and folder, keyed by Drive ID, with its parent's ID), in the goroutine that writes `scandata`: every file and folder it saves, lists or looks up is upserted. A complete scan whose query is one of the Request page's unfiltered defaults then deletes what it didn't see in its scope; other scans only add and update. Then the account's folder totals are rebuilt. See [archive/browse.md](archive/browse.md#drive-a-living-record-per-account)
- See [archive/request-drive-scans.md](archive/request-drive-scans.md#folder-scans)

**Gmail Scanner (`gmail.go`):**
- Uses Gmail API
- Scans mailbox messages
- Extracts: message ID, thread ID, from, to, subject, size, labels
- Real-time progress updates via SSE
- Deduplication by message ID

**Photos Scanner (`photos.go`):**
- Uses the Google Photos Picker API, the only way left to read a user's own photos since Google turned off the Library API's read scopes in 2025. It can't list a library: a scan covers what the user picks, up to 2000 items
- `POST /api/photos/sessions` creates a picking session (`photos_picker_sessions`, one `waiting` or `scanning` per user) and a goroutine polls it every `pollInterval` (5 s) until the user has picked, or `timeoutIn` (30 minutes) passes (`expired`). Google's session ID never reaches the browser
- Once picked, a `google_photos` scan lists the items and sizes each one with a `HEAD` of its bytes' URL (`=d`, `=dv` for videos), with the account's bearer token, four at a time; only when `HEAD` gives no length does it download the item, recording its MD5. It retries 429s, 5xx and network errors, re-lists after 50 minutes for fresh `baseUrl`s, and deletes the session when done
- Sizes are of the copy Google Photos keeps, which for Storage saver uploads can be far smaller than the original; the Picker API gives no upload quality or quota use
- Stores (`photos_picked_items`): media item ID, photo or video, MIME type, file name, when taken, dimensions, camera and exposure, fps, size and how it was found
- Real-time progress updates via SSE, under the account's `client_key`
- At startup, picks still `waiting` expire, and `scanning` ones end (their scans are failed as interrupted)
- See [archive/photos-picker.md](archive/photos-picker.md)

**Cloud Storage Scanner (`gcs.go`):**
- Uses the Cloud Storage JSON API and Resource Manager with a linked account's refresh token, and the read-only scopes `devstorage.read_only` and `cloudplatformprojects.readonly`: it sees what that Google account's IAM roles allow
- A scan covers a project's buckets, or one bucket under an optional prefix. The project or bucket is checked (`buckets.list` or `buckets.get`) before the scan is recorded
- Each bucket in turn: `objects.list` (1000 a page; with `versions=true` for noncurrent versions), then, when asked, a second pass with `softDeleted=true`, since the two can't be combined. Rows go to the account's living record (`gcs_objects`, one per object version: live, noncurrent or soft-deleted) in batches of 1000, stamped with the scan
- Once a bucket is done, what the scan didn't see in its scope (prefix, and the states it listed) leaves the record, and the bucket's folder totals (`gcs_prefix_totals`: objects and bytes by state, live bytes by class, per prefix) are rebuilt in SQL. The scan keeps its own totals per bucket (`gcs_scan_buckets`), which stay as later scans change the record
- Requester Pays buckets are skipped (listing them would bill this app's project); a bucket that fails (403) is recorded and the scan goes on; it fails only if every bucket did. The client library retries 429s and 5xx
- About 3,300 objects a second per pass, so a 10-million-object bucket takes about 50 minutes per pass
- Real-time progress updates via SSE, under the account's `client_key`
- See [archive/gcs-scans.md](archive/gcs-scans.md)

#### Notification Hub (`notification/hub.go`)
- Pub/sub pattern for progress updates
- Channel-based communication
- Broadcasts to all subscribers or specific client keys
- Progress data: processed count, completion %, ETA

### 3. Database Layer (PostgreSQL)

**Connection Details:**
- Host: `hdd_db` (Docker) / `localhost` (local)
- Port: 5432
- User: `hddb`
- Database: `hdd_db`

**Schema:**

```
scans (main scan records)
├── scandata (file/directory data from local/drive scans)
├── scanmetadata (scan configuration)
├── messagemetadata (Gmail message data)
└── photos_picked_items (Google Photos items a scan picked)

photos_picker_sessions (Google Photos picks: state, pick_by, scan_id)
gcs_scan_buckets (what each Cloud Storage scan found of each bucket)

gcs_buckets, gcs_objects, gcs_prefix_totals (each account's Cloud Storage record,
  by client_key: buckets, object versions, folder totals; kept when scans are deleted)

dup_groups, dup_members, dup_state (each user's duplicates index: groups of copies,
  the copies, and what the index was built from)

privatetokens (linked Google accounts: refresh tokens, granted scope, google_sub)
web_sessions (web login sessions)

scans, privatetokens and web_sessions have a user_id → agent_users (agentserver's)
```

**Auto-Migration:**
- Tables auto-created on startup
- Migration system with versioning
- Handles schema updates

### 4. Agent sync service (`agent/server/`, Go)

A separate service for `driveagent` (the local drive-comparison agent in `agent/client/`), designed in [`specs/remote-sync.md`](specs/remote-sync.md). It shares the Postgres database but only uses its own `agent_*` tables, created by its own numbered migrations (`agentserver_schema_migrations`); `be` and `agentserver` never call each other, but `be` reads its users (see [Web authentication](#web-authentication)). nginx routes `/agent/` to it on port 8091.

Implemented (rollout steps 1 and 4): `GET /agent/health`, `POST /agent/v1/handshake` (version negotiation), username/password login with JWT access tokens and rotating refresh tokens, `PUT`/`GET /agent/v1/drives` (a drive's upload stream, acked version ranges, and linking copies of one physical drive by filesystem ID and serial), and `POST /agent/v1/drives/{id}/changes` (gzip JSON batches of the agent's change feed, applied with higher-version-wins and tombstones, so batches can arrive in any order). Plus the `agentserver user` admin commands and hourly housekeeping. Uploaded data lives in `agent_files`, `agent_dir_listings` and `agent_scan_runs`, keyed on a hash of the raw path. On the agent side, every `driveagent scan` uploads what it writes as it goes (step 6), and `driveagent sync` (step 5) uploads the rest of each drive's pending change feed from `state.db`. Data flow: `driveagent` → nginx `/agent/` → `agentserver` → Postgres, step by step in [Agent Upload Flow](#agent-upload-flow-driveagent--agentserver).

### 5. External Services (Google APIs)

**OAuth 2.0:**
- Authorization code flow, one service at a time with incremental authorization (see [Account Linking Flow](#account-linking-flow))
- Refresh tokens stored per linked account, with the scopes Google granted
- Scopes: `openid email` (identifies the account) plus `gmail.readonly` and/or `drive.metadata.readonly`. `photospicker.mediaitems.readonly` (Photos: only what the user picks), and `devstorage.read_only` with `cloudplatformprojects.readonly` (Google Cloud Storage; the OAuth client's project needs the Cloud Resource Manager API enabled for the latter)
- The OAuth client is in "Testing": refresh tokens expire 7 days after they're issued (review item 2.11)

**Required Credentials:**
- `OAUTH_CLIENT_ID` - Google OAuth client ID
- `OAUTH_CLIENT_SECRET` - Google OAuth client secret

## Data Flow

### Scan Request Flow

A Google Drive scan, from the Request page to its results. A Gmail scan runs the same way, minus the folder lookup, and saves messages instead of files.

```mermaid
sequenceDiagram
    actor User
    participant UI
    participant API as be: api.go
    participant Col as be: collector
    participant DB as PostgreSQL
    participant Drive as Google Drive API
    participant Hub as notification hub

    User->>UI: Pick account + Google Drive, set filters, Submit
    UI->>API: POST /api/scans (session cookie)
    API->>DB: account has granted Drive? query ≤ 2000 chars?
    alt not granted, or bad query or folder ID
        API-->>UI: 400 with a message (no scan created)
    end
    API->>Col: CloudDrive(scan, user)
    Col->>DB: refresh token, name and client_key of the account
    opt folder scan
        Col->>Drive: files.get folder, then its parents up to My Drive
        Drive-->>Col: folder, full path
        Note over Col: missing, trashed or a file → 400, no scan
    end
    Col->>DB: scans row (Running) + scanmetadata (name, client_key, folder, query)
    Col-->>API: scan_id
    API-->>UI: {scan_id}
    UI-->>User: "Request submitted" + View results

    loop each folder (folder scan) or page of results (whole Drive)
        Col->>Drive: files.list q=…
        Drive-->>Col: files
        Col->>DB: scandata rows (full path, file_id, size, MD5)
        Col->>Hub: progress every 5 s (files so far)
    end
    Col->>DB: folder rows with recursive totals
    Col->>DB: mark Completed (or Failed with the error)
    DB->>Hub: final event (Completed / Failed)

    User->>UI: Request History › account › scan
    UI->>API: GET /api/scans/{id}/summary, GET /api/scans/{id}?page=n
    API->>DB: owner check, totals, a page of rows in tree order
    API-->>UI: summary + rows
    UI-->>User: results page
```

### Account Linking Flow

Linking a Google account, or adding a service (e.g. Drive) to one already linked. The user is logged in to the web app throughout.

```mermaid
sequenceDiagram
    actor User
    participant UI
    participant Google
    participant BE as be: oauth.go
    participant DB as PostgreSQL

    User->>UI: "Link another Google account" or "Grant Drive access"
    UI->>UI: sessionStorage: random state + service being linked
    UI->>Google: authorize: openid email + service scope,<br/>include_granted_scopes, offline, prompt=consent,<br/>login_hint (when adding a service)
    Google-->>User: account chooser + consent
    User->>Google: approve (may untick scopes)
    Google->>UI: /oauth/glink?code&state
    UI->>UI: state matches this tab's?
    UI->>BE: full-page redirect: GET /api/glink?code&redirectUri (session cookie)
    BE->>BE: redirectUri's origin is a -frontend_url?
    BE->>Google: exchange the code (token endpoint)
    Google-->>BE: access + refresh token, granted scope, id_token
    BE->>BE: id_token → sub, email → masked display name
    BE->>DB: LinkAccount: update the user's row with this sub,<br/>else adopt the newest same-named legacy row,<br/>else insert (new client_key)
    BE-->>UI: 302 /request?account=<client_key>
    UI-->>User: account selected, back on the service being linked
```

### Photos Pick Flow

A Google Photos scan, from the Request page's **Pick in Google Photos** to its results. See [archive/photos-picker.md](archive/photos-picker.md#flow).

```mermaid
sequenceDiagram
    actor User
    participant UI as UI (PhotosPick)
    participant BE as be: photos.go
    participant Picker as Photos Picker API
    participant DB as PostgreSQL

    User->>UI: Pick in Google Photos
    UI->>UI: open a blank window (while the click counts)
    UI->>BE: POST /api/photos/sessions {clientKey}
    BE->>DB: account granted Photos? no pick under way?
    BE->>Picker: sessions.create
    Picker-->>BE: id, pickerUri, pollingConfig, expireTime
    BE->>DB: photos_picker_sessions: waiting, pick_by
    BE-->>UI: {sessionKey, pickerUri, pickBy}
    UI->>UI: window → pickerUri/autoclose
    loop every pollInterval, until picked or pick_by
        BE->>Picker: sessions.get
    end
    User->>Picker: picks items, Done
    loop every 3 s while waiting or scanning
        UI->>BE: GET /api/photos/sessions/{key}
    end
    BE->>DB: scanning; LogStartScan(google_photos)
    BE->>Picker: mediaItems.list
    par four at a time
        BE->>Picker: HEAD baseUrl=d / =dv (bearer token)
    end
    BE->>DB: photos_picked_items; scan Completed; done
    BE->>Picker: sessions.delete
    UI-->>User: Scan N is done · View results
```

### Cloud Storage Scan Flow

A Google Cloud Storage scan of one bucket, from the Request page to Browse. A whole-project scan repeats the bucket part for each bucket. See [archive/gcs-scans.md](archive/gcs-scans.md#a-living-record-per-bucket).

```mermaid
sequenceDiagram
    actor User
    participant UI as UI (Request, Browse)
    participant BE as be: gcs.go
    participant GCS as Cloud Storage API
    participant RM as Resource Manager
    participant DB as PostgreSQL

    User->>UI: Google Cloud Storage tab, pick an account
    UI->>BE: GET /api/gcs/{key}/projects
    BE->>RM: projects.search (state:ACTIVE)
    UI->>BE: GET /api/gcs/{key}/projects/{project}/buckets
    BE->>GCS: buckets.list
    User->>UI: a bucket, prefix, versions + soft-deleted, Submit
    UI->>BE: POST /api/scans {GStorage}
    BE->>GCS: buckets.get (checked before the scan exists)
    BE->>DB: scans + scanmetadata (gcs)
    BE-->>UI: {scan_id}
    loop 1000 objects a page
        BE->>GCS: objects.list (versions=true)
        BE->>DB: upsert gcs_objects (seen by this scan)
    end
    loop 1000 objects a page
        BE->>GCS: objects.list (softDeleted=true)
        BE->>DB: upsert gcs_objects
    end
    BE->>DB: delete unseen in scope; rebuild gcs_prefix_totals;<br/>gcs_buckets; gcs_scan_buckets; scan Completed
    User->>UI: Browse › Google Cloud Storage
    UI->>BE: GET /api/browse/google/{key}/gcs/children?folder=<bucket>/<prefix>
    BE->>DB: gcs_prefix_totals, gcs_objects by parent
```

### Real-time Progress Updates (SSE)

```mermaid
sequenceDiagram
    participant UI as UI (Request page)
    participant SSE as be: sse.go
    participant Hub as notification hub
    participant Col as be: collector (Gmail or Drive)
    participant DB as PostgreSQL

    UI->>SSE: EventSource /sse/scanprogress (withCredentials)
    SSE->>Hub: SubscribeAll
    loop while a scan runs
        Col->>Hub: progress (scan_id, processed, elapsed)
        Hub->>SSE: broadcast (never blocks, keeps the newest)
        SSE->>DB: scan owned by this user? (once per scan)
        SSE-->>UI: event: progress
        UI->>UI: progress bar
    end
    Col->>DB: mark Completed / Failed
    DB->>Hub: PublishScanEnd
    Hub->>SSE: final event
    SSE-->>UI: event: progress (status Completed / Failed, error)
    UI->>UI: "Completed" or "Failed: …" (invalid_grant → link again)
```

### Agent Upload Flow (driveagent → agentserver)

How a machine's `driveagent` gets its scan data to `agentserver`: logging in once, then a `scan` that uploads while it walks, and a `sync` that uploads whatever is left. The full protocol is in [specs/remote-sync.md](specs/remote-sync.md) and [specs/remote-sync-server.md](specs/remote-sync-server.md).

- **Every request** goes through nginx's `/agent/` location (with a rate limit on `/agent/v1/auth/`), and carries `X-Agent-Version` and `X-Agent-Id`, and after the handshake `X-Agent-Protocol`.
- **At home,** the agent dials `lan_addr` (the prod box's LAN address) first, verifying the certificate for the public name, and falls back to DNS, because the router's hairpin NAT is unreliable.

```mermaid
sequenceDiagram
    actor User
    participant A as driveagent
    participant L as state.db (local)
    participant S as agentserver
    participant P as PostgreSQL

    Note over User,P: Once per machine: driveagent login
    User->>A: driveagent login (password prompted, never stored)
    A->>S: GET /agent/health, POST /agent/v1/handshake {version, protocols, os}
    S-->>A: decision ok / upgrade_recommended / upgrade_required, batch limits
    A->>S: POST /agent/v1/auth/login {username, password, agent_id, hostname}
    S->>P: check argon2id hash in agent_users, lockout per (username, IP), register agent_agents
    S-->>A: access token (JWT, 15 min) + refresh token (30 days, rotating)
    A->>L: save tokens (agent.json, bound to this machine)

    Note over User,P: driveagent scan --drive-id D --path …
    A->>A: locks: one scan per disk, one version per machine
    A->>S: health + handshake (exit 3 if unreachable, 4 if upgrade required)
    opt access token due
        A->>S: POST /agent/v1/auth/refresh {refresh_token}
        S-->>A: new pair (old one rotated, 30 s grace, reuse revokes the family)
    end
    A->>L: wrong-drive guard (filesystem ID, serial), S = sync_clock
    A->>S: PUT /agent/v1/drives/D {stream_id, roots, identity}
    S->>P: create or update agent_drives, link to a physical drive by filesystem ID + serial
    S-->>A: acked_ranges, reset (new stream → start over), physical_drive
    A->>L: reconcile the synced marker with acked_ranges
    par walk
        A->>L: walk, hash (BLAKE3), write rows stamped row_version > S, tombstones for deletions
    and upload
        loop until the walk is done and everything above S is acked
            A->>S: POST /agent/v1/drives/D/changes (gzip, ≤ 1000 changes, Idempotency-Key, range (from, to])
            S->>P: one tx: skip if already acked, higher version wins, merge the range
            S-->>A: acked_ranges, applied / skipped / rejected
            A->>L: advance the synced marker
        end
    end
    alt 409 STREAM_MISMATCH
        A->>S: re-open the drive (PUT) and reconcile, then resume
    else 401, 426 or unreachable past --remote-timeout
        A-->>User: exit 3 or 4, checkpoint kept (driveagent sync resumes)
    end
    A-->>User: exit 0: scan done, all of it acknowledged

    Note over User,P: driveagent sync (no drive needed)
    A->>S: same preflight (health, handshake, token)
    loop each drive in state.db
        A->>S: PUT /agent/v1/drives/{id}, reconcile
        loop each gap of history not yet acked, oldest first
            A->>S: POST …/changes for that range
            S-->>A: acked_ranges
            A->>L: advance the synced marker
        end
    end
```

## Deployment

### Docker Compose Stack

```yaml
Services:
├── hdd_db (PostgreSQL)
│   └── Port: 5432
├── hdd_be (Go Backend)
│   └── Port: 8090
├── hdd_ui (React Frontend)
│   └── Port: 80/443
└── agentserver (driveagent uploads)
    └── Port: 8091 (nginx only)
```

### Environment Variables

**Backend:**
- `OAUTH_CLIENT_ID` - Google OAuth client ID
- `OAUTH_CLIENT_SECRET` - Google OAuth client secret
- `FRONTEND_URL` - UI origin, or a comma-separated list of origins (e.g. `https://sm.jkurapati.com,http://192.168.1.118:5173`). Used for CORS, and as the only origins account linking may return to

**agentserver:** the same `DB_*` variables as the backend, plus `AGENTSERVER_JWT_SECRET` (required) and the optional settings in the [server spec](specs/remote-sync-server.md#configuration).

**Frontend:**
- Backend API URL configured in `ui/src/api/index.ts`

## Technology Stack Summary

| Layer | Technology |
|-------|-----------|
| Frontend | React, TypeScript, Vite, TanStack Router/Query, Tailwind CSS |
| Backend | Go 1.x, Gorilla Mux, sqlx |
| Database | PostgreSQL 15+ |
| APIs | Google Drive API, Gmail API, Google Photos Picker API, Cloud Storage JSON API, Cloud Resource Manager API |
| Auth | Google OAuth 2.0 |
| Real-time | Server-Sent Events (SSE) |
| Containerization | Docker, Docker Compose |

## Key Features

1. **Multi-source Scanning:** Local, Google Drive, Gmail, Google Photos (picked items), Google Cloud Storage
2. **Real-time Progress:** SSE-based progress updates for long-running scans
3. **OAuth Integration:** Secure Google account authentication
4. **Persistent Storage:** PostgreSQL with auto-migration
5. **RESTful API:** Clean REST endpoints with pagination
6. **Deduplication:** Gmail messages deduplicated by message ID
7. **Rich Metadata:** camera and exposure data for picked photos, email headers, file attributes

## Known Limitations

1. **Directory Size Inconsistency:**
   - Local scans: recursive size calculation
   - Cloud scans: directory-level only (excludes subdirectories)

2. **No Testing:** Codebase currently lacks test coverage

3. **Hardcoded Configuration:** Database connection and API URLs are hardcoded

4. **Single Region:** Timestamps converted to America/Los_Angeles timezone

