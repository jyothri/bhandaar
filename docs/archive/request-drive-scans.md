# Request Page: Google Drive Scans

**Status:** implemented in #33 (steps 1–6), and checked on dev.sm against a copy of production. Written 2026-09-27. Covers review items 4.2 (only Gmail scans can be requested) and 4.1 (results view) in [`codebase-review.md`](../codebase-review.md), and part of 4.9 (re-linking adds a duplicate account).

## Problem

The Request page (`ui/src/routes/request.tsx`) can only start Gmail scans. The backend can already scan Google Drive (`POST /api/scans` with `ScanType: "GDrive"`, `be/collect/drive.go`), but nothing in the UI reaches it, and several things around it are Gmail-only:

- **Linking** asks Google for `gmail.readonly` only, so no linked account can be scanned for Drive. On 2026-09-27, all three production accounts had exactly that scope.
- **Account identity** at link time comes from the Gmail API (`collect.GetIdentity` calls `users.getProfile`), so an account linked without Gmail access can't be linked at all.
- **Request History** lists scans by `scanmetadata.name`, which only Gmail scans fill in (with the display name the UI sends). Drive scans save an empty name, so they'd never appear there.
- **Live progress** is published only by the Gmail collector.
- **Results:** there's no results view for any scan type. The home page is a placeholder (4.1), and `GET /api/scans/{id}` returns `sql.Null*` values as `{String, Valid}` objects.

## Scope

In:
1. Linking per service with incremental authorization: an account can be linked for Gmail, Drive or both, and access can be added to an existing account later.
2. A Google Drive section on the Request page: guided filter fields, a raw query you can edit, and an optional folder, with or without its subfolders.
3. Every scan of one Google account, Gmail or Drive, recorded under that account (its `client_key`) and its name, so they appear together in Request History, and two accounts that happen to share a name don't.
4. Live progress for Drive scans.
5. A results view for a scan, for Drive and Gmail, linked from Request History.

Out:
- **Google Photos.** Deferred; see [Google Photos (deferred)](#google-photos-deferred).
- Shared drives (`supportsAllDrives`). Folder scans cover folders in My Drive and folders shared with you, not shared drives.
- Removing a linked account (4.9's other half).
- Reusing earlier scans. Each Drive scan is a full snapshot: a scan of a parent folder lists a subfolder scanned before all over again. That costs one list call per folder, and Drive supplies each file's MD5, so there's no hashing to save, unlike `driveagent`, which skips re-hashing unchanged files. A folder's modified time doesn't change when something deeper in it does, so it can't tell which subtrees are unchanged either. If re-scans ever get slow, the Drive counterpart of `driveagent`'s model would be one record per account, keyed by `scandata.file_id`, kept current with Drive's changes feed (`changes.list` from a saved page token).
- `completion_pct` and ETA in progress events (7.6, deferred). Drive progress shows a count and an indeterminate bar, like Gmail.

## Linking accounts per service

### Scopes

| Service | Scope requested | Why |
|---|---|---|
| (every link) | `openid email` | Identifies the Google account from the `id_token`, whatever else is granted |
| Gmail | `https://www.googleapis.com/auth/gmail.readonly` | Unchanged |
| Google Drive | `https://www.googleapis.com/auth/drive.metadata.readonly` | Name, size, MIME type, modified time and `md5Checksum` are all metadata. The collector never reads file contents, so it doesn't need `drive.readonly`. `cloudConfig` in `drive.go` changes to match |

The UI builds the authorization URL as today, plus:
- `scope`: `openid email` and the one service's scope.
- `include_granted_scopes=true`: Google adds the account's earlier grants to this one, so the new refresh token covers Gmail and Drive together.
- `login_hint=<google sub>` when adding a service to an account that's already linked, so Google preselects that account.
- `access_type=offline` and `prompt=consent`, unchanged, so Google always returns a refresh token.

Google's consent screen lets the user untick individual scopes. The backend stores the scopes Google actually granted (the token response's `scope`), never the ones requested. So an account whose Drive box was unticked still shows "no Drive access" afterwards.

### Identity and re-linking (`be/web/oauth.go`)

`GoogleAccountLinkingHandler` identifies the account from the token response's `id_token` instead of the Gmail API. Its payload is decoded for `sub` and `email`. Signature checking isn't needed, because the token comes straight from Google's token endpoint over TLS, which is the case Google's OpenID docs exempt. A response without an `id_token` is answered with `400`. `collect.GetIdentity` goes away.

`privatetokens` gains `google_sub TEXT` (nullable), with a unique index on `(user_id, google_sub)` where `google_sub` is not null. After the code exchange:
1. **Same account again:** if this user has a row with this `sub`, it's updated: `access_token`, `refresh_token`, `scope`, `expires_in`, `token_type` and `display_name`. The row keeps its `client_key`, so earlier scans still point at it.
2. **Legacy row:** otherwise, if this user has rows with no `google_sub` and the same `display_name` (`getDisplayName(email)`), the newest one is updated the same way and gets the `sub`. This is how the three production accounts pick it up the first time Drive access is added. Older duplicates stay, until 4.9 lets users remove them.
3. **New account:** otherwise a new row, as today.

Two different bhandaar users may link the same Google account; each gets their own row.

The handler then redirects to `/request?account=<client_key>`, instead of `/request`, so the page can select the account that was just linked.

### Accounts API

`GET /api/accounts` adds two fields to each account:

```json
{ "clientKey": "…", "displayName": "jyo****ri@gmail.com",
  "services": ["gmail", "drive"], "loginHint": "1178…" }
```

- `services` is derived from the stored `scope`: `gmail.readonly` gives `gmail`, and any of `drive.metadata.readonly`, `drive.readonly` or `drive` gives `drive`. An empty scope counts as `["gmail"]`, since only Gmail was ever linked before.
- `loginHint` is the `google_sub`, or absent for a legacy row.

### Scan requests check the scope

`DoScansHandler` checks, before calling a collector, that a `GMail` or `GDrive` request's `ClientKey` names one of the user's accounts with that service. If it doesn't, the answer is `400` with a message the Request page shows as is, e.g. `This account hasn't granted Google Drive access. Use "Grant Drive access" first.` No scan row is created.

It also rejects a `QueryString` or `Filter` longer than 2000 characters (`scanmetadata.search_filter` is `VARCHAR(2000)`). Today a longer one fails its insert silently, in a goroutine.

## Request page

`/request` gains a service picker above the form, and search params: `type` (`gmail` or `drive`, default `gmail`) and `account` (a `client_key`).

```
 Scan:  (•) Gmail   ( ) Google Drive

 Account  [ jyo****ri@gmail.com  ▾ ]   [Link another Google account]
          ⚠ This account hasn't granted Google Drive access.  [Grant Drive access]

 ── Google Drive ──────────────────────────────────────────
 Owned by me       [x]
 File types        [ ] Images [ ] Videos [ ] Audio [ ] PDFs [ ] Google Docs/Sheets/Slides
 Modified          [yyyy-mm-dd] to [yyyy-mm-dd]   (UTC)
 Include trash     [ ]
 Folder            [ paste a folder link or ID     ]  [x] Include subfolders
 Query             trashed = false and mimeType != '…folder' and 'me' in owners
                   [ ] Edit query

                              [ Submit ]
```

- **Service picker:** radio buttons. Switching keeps the selected account and updates `type` in the URL (replacing the history entry).
- **Accounts:** the list shows every linked account. When the selected one lacks the chosen service, a notice and a **Grant \<service\> access** button replace the form fields, and Submit is disabled. The button starts linking for that service with the account's `loginHint`.
- **Link another Google account** starts linking for the chosen service. It replaces the "Continue with Google" button.
- **Returning from Google:** the page before the redirect saves the service being linked in `sessionStorage` (next to the OAuth `state`, in `oauthState.ts`). When `/request` loads with `account`, it selects that account and takes `type` from the saved service, then clears it and drops `account` from the URL.
- **Gmail section:** unchanged, except that the request no longer sends `Username`; the backend takes the account's name from the database (see [Account names](#account-names)). This also fixes 3.7.
- **Drive section:** described in [Drive query builder](#drive-query-builder). Submit sends:
  ```json
  { "ScanType": "GDrive",
    "GDriveScan": { "QueryString": "<q>", "FolderId": "<id or empty>", "Recursive": true,
                    "ClientKey": "<client_key>", "RefreshToken": "" } }
  ```
- **Validation**, for Drive: an end date before the start date, a query over 2000 characters, and a folder that isn't a Drive folder link or ID (see [Folder scans](#folder-scans)). An empty query is allowed: it scans the whole Drive (or the whole folder), trash and folders included.
- **After submit:** same as Gmail. It shows the scan ID (linked to its results view), invalidates Request History's queries, and `ScanProgress` shows the scan's progress.

### Drive query builder

`ui/src/driveQuery.ts` (next to `gmailFilter.ts`) builds the Drive API's [`q`](https://developers.google.com/drive/api/guides/search-files) from the guided fields. Terms are joined with `and`, in this order:

| Field | Default | Term |
|---|---|---|
| (always) | | `mimeType != 'application/vnd.google-apps.folder'`, since the collector skips folders anyway |
| Include trash | off | `trashed = false` while off |
| Owned by me | on | `'me' in owners`. Files shared with you don't count against your quota |
| File types | none (all) | One of these per ticked type, OR-ed together in parentheses: `mimeType contains 'image/'`, `'video/'`, `'audio/'`, `mimeType = 'application/pdf'`, `mimeType contains 'application/vnd.google-apps.'` |
| Modified from | empty | `modifiedTime >= 'YYYY-MM-DDT00:00:00'` |
| Modified to | empty | `modifiedTime < '<next day>T00:00:00'`, so the end date is included |

Drive compares these times in UTC; the form says so.

All values come from fixed lists or date inputs, so nothing needs escaping.

**Edit query:** ticking it makes the query box editable, starting from the built query, and disables the guided fields. Unticking it goes back to the built query. The query box is read-only otherwise, like Gmail's. The folder isn't part of the query, so it stays editable either way.

### Folder scans

The **Folder** field takes a folder's link (`https://drive.google.com/drive/folders/<id>`, with or without `/u/<n>/` or a `?usp=` suffix) or its bare ID. The UI extracts the ID, and rejects anything that isn't `[A-Za-z0-9_-]{10,}`; the backend checks the same pattern, since the ID goes inside quotes in a query. **Include subfolders** (on by default) only shows once a folder is given. With the folder empty, the scan covers the whole Drive, as before.

The folder is sent as `FolderId`, and `Recursive` says whether to include subfolders. `QueryString` stays the filter: the same query as without a folder, applied to the files in the folder.

The backend walks the folder breadth first. For each folder, it lists
```
'<folder id>' in parents and ((mimeType = 'application/vnd.google-apps.folder' and trashed = false) or (<QueryString>))
```
With an empty `QueryString`, it lists `'<folder id>' in parents`: everything in the folder. Subfolders go on the queue when `Recursive` is set, and everything else is a file to save, as today. So the filter applies to files, including its `trashed = false` unless Include trash is ticked, and every subfolder is walked whether it matches the filter or not: a subfolder owned by someone else can still hold files you own. Trashed folders are never walked; their contents are trashed too. Shortcuts (`application/vnd.google-apps.shortcut`) are not followed. A set of visited folder IDs guards against loops.

This costs one list call per folder (more for folders with over 1000 entries), so a tree of 5,000 folders makes about 5,000 calls, well within Drive's per-user quota but a few minutes of wall time. The calls go through a rate limiter like the Photos collector's.

**Checked before the scan starts:** `CloudDrive` gets the folder (`files.get`, fields `id, name, mimeType, trashed`) before creating the scan row. A folder that doesn't exist, isn't visible to the account, isn't a folder or is trashed gives `400` with a message the Request page shows, e.g. `Folder not found, or this account can't see it.` To do this, `CloudDrive` resolves the refresh token and builds the Drive service before `LogStartScan`, instead of after it.

**Recorded:** `scanmetadata.search_path` holds the folder, as `<folder path> (<id>)`, plus ` and subfolders` when recursive. Request History shows it in front of the filter, e.g. `My Drive/Photos 2019 (1AbC…) and subfolders: trashed = false and …`.

## Backend: Drive collector (`be/collect/drive.go`)

- **Account name:** saved from the database, see [Account names](#account-names), with the query as `search_filter` and the folder as `search_path`.
- **Folder walk:** see [Folder scans](#folder-scans). Without a folder, the collector lists the whole Drive with `QueryString`, as today.
- **Progress:** `startCloudDrive` publishes progress like Gmail. `logProgress` and the counters move from `gmail.go` to `common.go`. The publisher key is the `ClientKey`. `processed_count` is the number of files listed so far, and `active_count` is 0. The end event already comes from `MarkScanCompleted` or `MarkScanFailed`.
- Scans still run one at a time, behind `collect`'s `lock`. A Drive scan requested while another scan runs shows as Running in history, and publishes progress only once it starts. Unchanged, but worth knowing.
- Google Docs, Sheets and Slides files have no `size` or `md5Checksum` in Drive; they're stored with size 0 and an empty hash.
- **Folder paths:** each file is saved with the folder path it was found at, in `scandata.path` (`Desktop/Qns/q1.pdf`), as local scans save theirs, and its Drive file ID in a new `scandata.file_id` column (NULL for local scans).
  - Paths are full, the way the Drive UI shows locations, so a file has the same path whichever folder it was scanned from: `My Drive/…` for files in My Drive, or `Shared with me/<topmost folder the account can see>/…` for files in a folder shared with it (a shortcut in My Drive doesn't change that).
  - A folder scan first builds the folder's own path: it looks up the folder's parents one level at a time (`files.get`, one call per level) until My Drive, or until a parent the account can't see (no `parents`, or 404). The walk then tracks each subfolder's path as it goes, at no extra cost.
  - A whole-Drive scan's paths come the same way: before listing files, it gets the My Drive folder's ID (`files.get("root")`) and lists every folder once, so each file's first parent can be turned into a path. A file whose folder the account can't see (typically one shared with it) goes under `Shared with me`.
  - Scans from before this column have the file ID in `path` and no `file_id`; only scan 8 on the dev copy, none in production.
- **Folder rows:** at the end of the scan, a row per folder below the scanned one (`is_dir = true`, its Drive ID in `file_id`), with the total size and file count of everything under it, as local scans save theirs. The scanned folder itself gets no row in either (the results summary gives its totals). A folder scan saves every subfolder it walked, empty ones too; a whole-Drive scan, the folders with at least one scanned file under them, not `My Drive` or `Shared with me`. Totals are tracked by folder ID, not by splitting paths, since a Drive folder's name can contain `/`. The results summary counts only file rows.

## Account names

Request History groups scans by `scanmetadata.name`. Today that name is:
1. made once, when the account is linked, by `getDisplayName`: the Google email, masked (`jyo****ri@gmail.com`), or the account's `client_key` when the part before `@` is shorter than 6 characters; stored as `privatetokens.display_name`;
2. sent to the UI by `GET /api/accounts`, shown in the Accounts list;
3. read back from the selected option's text by the Request page, and sent with a Gmail request as `GMailScan.Username`;
4. saved by the Gmail collector as `scanmetadata.name` (and `messagemetadata.username`).

So the name the history shows is whatever the browser sent.

From now on, **every** Google collector, Gmail and Drive, takes the name from the account's `privatetokens.display_name`, read along with its refresh token (`refreshToken` in `common.go` returns both). `GMailScan.Username` is ignored and dropped from the UI's type. Since re-linking now keeps an account's row and `getDisplayName` is deterministic, one Google account keeps one name, and all its Gmail and Drive scans list together under it.

The five existing Gmail scans already carry the name their account shows, so nothing is backfilled.

### Grouping by account, not by name

The name alone isn't enough to group by: it's masked, so two different Google accounts can mask to the same name (`jyo****ri@gmail.com` for both `jyothri@…` and `jyoXYZri@…`), and their scans would list together. So scans also record which account they belong to, and Request History groups by that.

- **Schema:** `scanmetadata` gains `client_key VARCHAR(100)` (nullable; Local scans have none). Every Google collector saves the account's `client_key` next to its name. The name stays too: it's what history shows, and it survives if the account is removed later (4.9).
- **`GET /api/scans/accounts`** returns the accounts that have scans as `[{ "clientKey": "…", "displayName": "…" }]` instead of a list of names: one entry per `client_key`, named by its newest scan's `name`.
- **`GET /api/scans/requests/{client_key}`** takes a `client_key` instead of a name, and lists that account's scans.
- **Telling same-named accounts apart:** when two of a user's accounts share a display name, the Accounts lists on the Request page and on Request History add the first 4 characters of the `client_key`, e.g. `jyo****ri@gmail.com · aB3x`. Otherwise they look as they do now.
- **Backfill,** once at startup, for rows with a name but no `client_key`: each gets the `client_key` of its user's newest `privatetokens` row with that `display_name`. The newest is the one re-linking adopts (see [Identity and re-linking](#identity-and-re-linking-beweboauthgo)), so old and new scans end up under the same account. Rows with no matching account keep a null `client_key` and aren't listed in Request History; the backend logs how many. It's idempotent: rows that already have a `client_key` are never touched.

  In production (checked 2026-09-27, read-only), this puts scans 1–3 under account 2, and scans 4–5 under account 3. Accounts 1 and 3 are the same Google account (4.9's stale duplicate) and share a name, so 4–5 go to the newer one, whichever of the two they actually ran on. No scan is left unmatched.

## Request History (`ui/src/routes/requests.tsx`)

- The account list comes from `GET /api/scans/accounts` as `{clientKey, displayName}`: the option's value is the `clientKey`, its label the name (with the `client_key` suffix when two share one). The selected account, and `queryKeys.scanRequests`, are keyed by `clientKey`.
- Drive scans appear under their account, alongside its Gmail scans. Production has no Drive scans yet.
- The Scan Type column shows `Gmail` and `Google Drive` instead of `gmail` and `google_drive`.
- The scan ID links to the scan's results view.

## Results view

A new route, `/scans/$scanId` (`ui/src/routes/scans.$scanId.tsx`), links from Request History and from the Request page's success message. It shows a summary on top, and one page of results below with Previous and Next.

**Summary:** a new `GET /api/scans/{scan_id}/summary`, owner-checked like the other scan routes:

```json
{ "scan_id": 12, "scan_type": "google_drive", "name": "jyo****ri@gmail.com",
  "search_filter": "…", "status": "Completed", "scan_start_time": "…",
  "scan_duration_in_sec": "812.4", "item_count": 5231, "total_bytes": 8123456789 }
```

`item_count` and `total_bytes` are counted from `scandata` file rows (`is_dir` false) for Drive, and from `messagemetadata` (`size_estimate`) for Gmail. For Gmail, the page labels them "new messages", per 4.1's note: a scan's rows are only the messages new in that scan.

**Rows**, 10 per page, from the existing endpoints:

| Scan type | Endpoint | Columns |
|---|---|---|
| `google_drive` | `GET /api/scans/{id}?page=` | Folder (the path without the name), Name (linked to `https://drive.google.com/file/d/<file_id>/view` when there's a `file_id`), Size, Modified, MD5 (first 8 characters, full on hover) |
| `gmail` | `GET /api/gmaildata/{id}?page=` | From, Subject, Date, Size |
| other | | "No results view for this scan type yet." |

Those two endpoints change their JSON to plain values: read structs with `json` tags, and `null` for missing values, instead of `{String, Valid}` objects. No UI reads them today, so nothing breaks. `/api/photos/{id}` is left as it is.

The home page (`/`) stays a placeholder. Linking results from history is enough for 4.1.

**Navigation:** Request History keeps the selected account in its URL (`/requests?account=<client_key>`). Under the nav tabs, a trail shows where you are: `Request History › <account> › Scan N`. A scan's trail always goes through its account (the summary's `client_key`), however the scan was opened, and its account link goes back to that account's scans. The Request History tab stays highlighted on scan pages.

## Google Photos (deferred)

Since 2025-03-31, the Google Photos Library API no longer grants `photoslibrary.readonly` or `photoslibrary.sharing` to new authorizations. Apps can list only media items they created themselves (`photoslibrary.readonly.appcreateddata`). `be/collect/photos.go` lists the whole library, or one album, with the removed scopes, so it can't work for any account linked now.

The replacement Google offers, the **Photos Picker API**, has the user pick items in a Google Photos window, one session at a time. It can't list a library, and picked items' metadata doesn't appear to include file sizes (to check before relying on it). For a storage analyzer, that's a poor fit.

Options for later:
1. **Picker API:** scan only what the user picks. Sizes would need each file downloaded, or would stay unknown.
2. **Google Takeout:** import a Takeout export of Google Photos (its JSON sidecars) as a local scan.
3. **Remove** the Photos collector, the `photos` routes and their tables.

Until then, the Request page offers no Photos option, and the Photos code stays as it is. `GET /api/photos/albums` still takes a raw refresh token in its URL. It should take a `ClientKey` if Photos comes back, and be removed if it doesn't.

## Testing

**Backend:**
- `be/web/oauth_test.go`, against the fake token endpoint, which returns an unsigned `id_token`:
  - a new account is inserted with its `sub`
  - the same `sub` again updates the row and keeps its `client_key`
  - a legacy row with a matching display name is adopted
  - another user's row is never touched
  - no `id_token` gives `400`
  - the redirect carries `account`
- `be/db/users_test.go` or a new `be/db/accounts_test.go`:
  - the `google_sub` migration and unique index
  - scope-to-services mapping, including an empty scope
  - the `scanmetadata.client_key` migration and backfill: newest same-named account wins, another user's account is never matched, an unmatched row stays null, and a second run changes nothing
  - history by account: two accounts with one name list their scans separately, and the accounts list names each by its newest scan
- `be/web`: `POST /api/scans` rejects an account without the service, and a query over 2000 characters, without creating a scan.
- `be/collect/drive_test.go` (new), with a fake Drive API via `httptest`, like `gmail_test.go`:
  - the account name comes from the database, for Drive and Gmail alike
  - progress is published under the `ClientKey`
  - without a folder, folders are skipped
  - a folder scan walks subfolders only when `Recursive`, applies the filter to files but not to folders, skips shortcuts, and survives a loop
  - a missing, trashed or non-folder `FolderId` fails before a scan row exists, and an ID outside the pattern is rejected
- The summary endpoint: owner check, and counts for both scan types.

**UI:**
- `driveQuery.test.ts`: every field, the defaults, OR-ing of file types, the next-day end date, and folder-ID extraction from each link form.
- `src/test/request.test.tsx`:
  - switching service keeps the account
  - an account without Drive shows the grant button with the right `scope`, `include_granted_scopes` and `login_hint`
  - a Drive submit posts the built query, and the folder ID and `Recursive` when a folder is given
  - Include subfolders shows only with a folder
  - a Gmail submit sends no `Username`
  - the editable query overrides the fields
  - returning with `?account=` selects the account and the saved service
- `src/test/scanResults.test.tsx` (new): the Drive and Gmail tables, paging, and a scan of another type.
- `src/test/requests.test.tsx`: type labels, scan IDs link to results, accounts are selected by `clientKey`, and two same-named accounts show their suffixes.

## Implementation order

Six steps, one PR each. Every step leaves production working, and is deployed and checked before the next one starts. A step that changes an API ships its backend and UI in the same PR and the same deploy. Each PR updates the docs for its own part (see [Docs to update](#docs-to-update-with-the-implementation)).

**0. Google Cloud console:** nothing to do. The Google Drive API and the Photos Library API are already enabled in the OAuth client's project (confirmed 2026-09-27). While the client is in "Testing", its test users can grant scopes the consent screen doesn't list, so `openid`, `email` and `drive.metadata.readonly` need adding there only when the app is published (2.11).

The OAuth client stays in **"Testing"** status (confirmed 2026-09-27); publishing it and verification are deferred (2.11). What that means here:
- Every linked Google account must be on the consent screen's test-user list.
- Refresh tokens expire 7 days after they're issued, for Drive as for Gmail. A scan of an account linked over a week ago fails with `invalid_grant`, which the progress panel already turns into "Link the account again". Re-linking (or **Grant Drive access**) issues a new token; after step 1 it updates the account in place, so its history and `client_key` stay.
- Adding `drive.metadata.readonly` changes nothing about verification: like `gmail.readonly`, it's a restricted scope, which only matters once the app is published.

**1. Account identity and re-linking.** Backend and UI.
- *Backend:* `id_token` identity replaces `GetIdentity`; `privatetokens.google_sub` and its index; the update-or-adopt-or-insert logic; the redirect to `/request?account=`; `services` and `loginHint` in `GET /api/accounts`; the scope and length checks in `DoScansHandler`.
- *UI:* linking asks for `openid email` plus Gmail, with `include_granted_scopes`, and `/request` selects the account named by `account`. The UI must ship with the backend: without `openid email`, the new handler rejects a link.
- *Check in production:* re-link one account for Gmail. It keeps its `client_key` (no new row) and gains a `google_sub`, and a Gmail scan still works.

**2. Account names and grouping by account.** Backend and UI.
- *Backend:* `refreshToken` returns the account, and the Gmail and Drive collectors save its `display_name` and `client_key`; `scanmetadata.client_key` and its backfill; `/api/scans/accounts` and `/api/scans/requests/{client_key}` keyed by account.
- *UI:* Request History selects by `clientKey`; the name suffix for same-named accounts; the Gmail request drops `Username`.
- *Check in production:* the backfill logs 0 unmatched; Request History shows scans 1–3 and 4–5 under their accounts; a new Gmail scan lists under its account.

**3. Drive collector.** Backend only; nothing in the UI calls it yet, so it can be tried with `curl`.
- `drive.metadata.readonly` in `cloudConfig`; `logProgress` and the counters moved to `common.go`, and Drive progress; `FolderId` and `Recursive`, the folder check before the scan row, the breadth-first walk and its rate limiter; `search_path`; `be/collect/drive_test.go`.
- *Check in production:* nothing visible changes. An account can't have Drive access until step 4.

**4. Request page for Drive.** UI only.
- The service picker and `type` param; **Grant \<service\> access** with `loginHint`; the saved service across the Google round trip; `driveQuery.ts`; the Drive section, Edit query and the folder field.
- *Check in production:* choose Google Drive, pick an account, **Grant Drive access** (which also renews its 7-day token). The account keeps its `client_key` and gains `drive`. Then run a small Drive scan (one file type, a short date range) and a small recursive folder scan, and watch their progress and history entries.

**5. Results view.** Backend and UI. Needs only step 2, so it can also go before 3 and 4.
- *Backend:* `GET /api/scans/{scan_id}/summary`; plain JSON from `/api/scans/{id}` and `/api/gmaildata/{id}`.
- *UI:* the `/scans/$scanId` route with the Drive and Gmail tables; history's scan IDs and the Request page's success message link to it; the Scan Type labels.
- *Check in production:* open a Gmail scan's and a Drive scan's results, and page through them.

**6. Review items.** Docs only: in `docs/codebase-review.md`, move 4.1, 4.2 and 3.7 to the archive with change-log rows, narrow 4.9 to removing accounts, and add the Photos situation as an open item.

## Docs to update with the implementation

- `CLAUDE.md`: the OAuth section (scopes, incremental linking), the Key API Endpoints (summary route, accounts' `services`, `/api/scans/accounts` and `/api/scans/requests/{client_key}` keyed by account), and the data flow (Drive progress).
- `docs/architecture.md`: linking and identity, the Drive section, and the results view.
- `docs/codebase-review.md`: see step 6.
