# Bhandaar Architecture

## System Overview

Bhandaar is a storage analyzer application that scans and analyzes data across multiple sources: local filesystems, Google Drive, Gmail, and Google Photos.

## Architecture Diagram

```mermaid
graph TB
    subgraph "Client Layer"
        UI[React/Vite Frontend<br/>Port: 5173]
        Browser[Web Browser]
    end

    subgraph "Backend Layer - Go Server :8090"
        direction TB
        WebServer[Web Server<br/>gorilla/mux + CORS]
        
        subgraph "API Handlers"
            API[REST API<br/>api.go]
            OAuth[OAuth Handler<br/>oauth.go]
            SSE[Server-Sent Events<br/>sse.go]
        end
        
        subgraph "Collection Services"
            LocalCollect[Local Scanner<br/>local.go]
            DriveCollect[Drive Scanner<br/>drive.go]
            GmailCollect[Gmail Scanner<br/>gmail.go]
            PhotosCollect[Photos Scanner<br/>photos.go]
        end
        
        NotifHub[Notification Hub<br/>hub.go<br/>Progress Broadcasting]
        
        DB[Database Layer<br/>database.go<br/>PostgreSQL Client]
    end

    subgraph "Data Storage"
        PostgreSQL[(PostgreSQL Database<br/>Port: 5432)]
        
        subgraph "Database Tables"
            Scans[scans]
            ScanData[scandata]
            ScanMetadata[scanmetadata]
            MessageMeta[messagemetadata]
            PhotosMedia[photosmediaitem]
            PhotoMeta[photometadata]
            VideoMeta[videometadata]
            Tokens[privatetokens]
        end
    end

    subgraph "External Services"
        GoogleOAuth[Google OAuth 2.0<br/>Authentication]
        GoogleDrive[Google Drive API<br/>Drive Storage]
        GmailAPI[Gmail API<br/>Email Data]
        PhotosAPI[Google Photos API<br/>Photo/Video Data]
    end

    %% Client to Backend connections
    Browser --> UI
    UI -->|HTTP/REST| API
    UI -->|OAuth Flow| OAuth
    UI -->|Real-time Updates| SSE

    %% Backend internal connections
    WebServer --> API
    WebServer --> OAuth
    WebServer --> SSE
    
    API --> LocalCollect
    API --> DriveCollect
    API --> GmailCollect
    API --> PhotosCollect
    
    GmailCollect -.->|Progress Events| NotifHub
    NotifHub -.->|Broadcast| SSE
    
    %% Data storage connections
    LocalCollect --> DB
    DriveCollect --> DB
    GmailCollect --> DB
    PhotosCollect --> DB
    OAuth --> DB
    
    DB --> PostgreSQL
    PostgreSQL --> Scans
    PostgreSQL --> ScanData
    PostgreSQL --> ScanMetadata
    PostgreSQL --> MessageMeta
    PostgreSQL --> PhotosMedia
    PostgreSQL --> PhotoMeta
    PostgreSQL --> VideoMeta
    PostgreSQL --> Tokens

    %% External service connections
    OAuth <-->|Authorization Flow| GoogleOAuth
    DriveCollect -->|API Calls| GoogleDrive
    GmailCollect -->|API Calls| GmailAPI
    PhotosCollect -->|API Calls| PhotosAPI

    %% Styling
    classDef frontend fill:#61dafb,stroke:#333,stroke-width:2px,color:#000
    classDef backend fill:#00add8,stroke:#333,stroke-width:2px,color:#fff
    classDef database fill:#336791,stroke:#333,stroke-width:2px,color:#fff
    classDef external fill:#4285f4,stroke:#333,stroke-width:2px,color:#fff
    
    class UI,Browser frontend
    class WebServer,API,OAuth,SSE,LocalCollect,DriveCollect,GmailCollect,PhotosCollect,NotifHub,DB backend
    class PostgreSQL,Scans,ScanData,ScanMetadata,MessageMeta,PhotosMedia,PhotoMeta,VideoMeta,Tokens database
    class GoogleOAuth,GoogleDrive,GmailAPI,PhotosAPI external
```

## Component Details

### 1. Frontend Layer (React + TypeScript)

**Technology Stack:**
- React 18 with TypeScript
- Vite (build tool)
- TanStack Router (routing)
- TanStack Query (data fetching)
- Tailwind CSS (styling)

**Key Components:**
- `/routes/request.tsx` - Main scan request form and results display
- `/routes/requests.tsx` - List view of scan requests by account
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

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/health` | GET | Health check |
| `/api/scans` | POST | Submit scan request |
| `/api/scans` | GET | List all scans (paginated) |
| `/api/scans/requests/{account_key}` | GET | Get scan requests for account |
| `/api/scans/{scan_id}` | GET | Get scan data |
| `/api/scans/{scan_id}` | DELETE | Delete scan |
| `/api/gmaildata/{scan_id}` | GET | Get Gmail scan results |
| `/api/photos/{scan_id}` | GET | Get Photos scan results |
| `/api/photos/albums` | GET | List photo albums |
| `/api/accounts` | GET | List OAuth-authenticated accounts |
| `/api/scans/accounts` | GET | List accounts with scans |

#### OAuth Flow (`web/oauth.go`)
- `/oauth/authorize` - Initiate Google OAuth2 flow
- `/oauth/callback` - Handle OAuth callback, store refresh tokens

#### Server-Sent Events (`web/sse.go`)
- `/events` - Real-time scan progress updates
- Broadcasts progress for Gmail scans

#### Collection Services (`collect/`)

**Local Scanner (`local.go`):**
- Scans local filesystem
- Recursive directory traversal
- Calculates sizes including subdirectories
- Stores: filename, path, size, modification time, MD5 hash

**Drive Scanner (`drive.go`):**
- Uses Google Drive API
- Scans cloud storage
- Directory-level size calculation (non-recursive)
- Authentication via service account or OAuth2

**Gmail Scanner (`gmail.go`):**
- Uses Gmail API
- Scans mailbox messages
- Extracts: message ID, thread ID, from, to, subject, size, labels
- Real-time progress updates via SSE
- Deduplication by message ID

**Photos Scanner (`photos.go`):**
- Uses Google Photos API
- Scans photos and videos
- Extracts metadata: camera info, EXIF data, file size
- Separate tables for photo vs video metadata

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
└── photosmediaitem (Photos/videos)
    ├── photometadata (photo-specific EXIF)
    └── videometadata (video-specific metadata)

privatetokens (OAuth refresh tokens)
```

**Auto-Migration:**
- Tables auto-created on startup
- Migration system with versioning
- Handles schema updates

### 4. External Services (Google APIs)

**OAuth 2.0:**
- Authorization code flow
- Refresh token storage
- Scopes: Drive (readonly), Gmail (readonly), Photos

**Required Credentials:**
- `OAUTH_CLIENT_ID` - Google OAuth client ID
- `OAUTH_CLIENT_SECRET` - Google OAuth client secret
- `GOOGLE_APPLICATION_CREDENTIALS` - Service account key file path

## Data Flow

### Scan Request Flow

```mermaid
sequenceDiagram
    participant User
    participant UI
    participant API
    participant Collector
    participant DB
    participant SSE
    
    User->>UI: Initiate Scan
    UI->>API: POST /api/scans
    API->>DB: Create scan record
    DB-->>API: Return scan_id
    API->>Collector: Start collection
    API-->>UI: Return scan_id
    
    loop Collection Process
        Collector->>External: Fetch data
        External-->>Collector: Return data
        Collector->>DB: Store data
        Collector->>SSE: Send progress update
        SSE-->>UI: Stream progress
    end
    
    Collector->>DB: Mark scan complete
    User->>UI: Request results
    UI->>API: GET /api/scans/{scan_id}
    API->>DB: Query results
    DB-->>API: Return data
    API-->>UI: Return results
    UI-->>User: Display results
```

### OAuth Flow

```mermaid
sequenceDiagram
    participant User
    participant UI
    participant Backend
    participant Google
    participant DB
    
    User->>UI: Click "Connect Account"
    UI->>Backend: GET /oauth/authorize
    Backend-->>UI: Redirect URL
    UI->>Google: Redirect to OAuth
    Google-->>User: Show consent screen
    User->>Google: Approve
    Google->>Backend: Redirect to /oauth/callback
    Backend->>Google: Exchange code for tokens
    Google-->>Backend: Return access + refresh tokens
    Backend->>DB: Store tokens
    Backend-->>UI: Redirect to success page
```

### Real-time Progress Updates (SSE)

```mermaid
sequenceDiagram
    participant UI
    participant SSE
    participant Hub
    participant Collector
    
    UI->>SSE: Connect to /events
    SSE->>Hub: Subscribe to updates
    
    loop Scanning
        Collector->>Hub: Publish progress
        Hub->>SSE: Broadcast to subscribers
        SSE->>UI: Stream event
        UI->>UI: Update progress bar
    end
    
    Collector->>Hub: Publish completion
    Hub->>SSE: Broadcast completion
    SSE->>UI: Stream complete event
    UI->>UI: Show results
```

## Deployment

### Docker Compose Stack

```yaml
Services:
├── hdd_db (PostgreSQL)
│   └── Port: 5432
├── hdd_be (Go Backend)
│   ├── Port: 8090
│   └── Volume: ~/keys/gae_creds.json
└── hdd_ui (React Frontend)
    └── Port: 80/443
```

### Environment Variables

**Backend:**
- `OAUTH_CLIENT_ID` - Google OAuth client ID
- `OAUTH_CLIENT_SECRET` - Google OAuth client secret
- `GOOGLE_APPLICATION_CREDENTIALS` - Path to service account JSON
- `FRONTEND_URL` - Frontend URL for CORS

**Frontend:**
- Backend API URL configured in `ui/src/api/index.ts`

## Technology Stack Summary

| Layer | Technology |
|-------|-----------|
| Frontend | React, TypeScript, Vite, TanStack Router/Query, Tailwind CSS |
| Backend | Go 1.x, Gorilla Mux, sqlx |
| Database | PostgreSQL 15+ |
| APIs | Google Drive API, Gmail API, Google Photos API |
| Auth | Google OAuth 2.0 |
| Real-time | Server-Sent Events (SSE) |
| Containerization | Docker, Docker Compose |

## Key Features

1. **Multi-source Scanning:** Local, Google Drive, Gmail, Photos
2. **Real-time Progress:** SSE-based progress updates for long-running scans
3. **OAuth Integration:** Secure Google account authentication
4. **Persistent Storage:** PostgreSQL with auto-migration
5. **RESTful API:** Clean REST endpoints with pagination
6. **Deduplication:** Gmail messages deduplicated by message ID
7. **Rich Metadata:** EXIF data for photos, email headers, file attributes

## Known Limitations

1. **Directory Size Inconsistency:**
   - Local scans: recursive size calculation
   - Cloud scans: directory-level only (excludes subdirectories)

2. **No Testing:** Codebase currently lacks test coverage

3. **Hardcoded Configuration:** Database connection and API URLs are hardcoded

4. **Single Region:** Timestamps converted to America/Los_Angeles timezone

