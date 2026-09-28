# Google Photos: Move to the Picker API

**Status:** proposed, 2026-09-28. Resolves review item 7.15 in [`codebase-review.md`](../codebase-review.md) ("Google Photos scans can't work"). Supersedes [Google Photos (deferred)](../archive/request-drive-scans.md#google-photos-deferred) in the Request page spec.

## Problem

Google turned off the Photos Library API's read scopes on 2025-03-31 (`photoslibrary.readonly`, `photoslibrary.sharing` and `photoslibrary`). Calls that depend only on them now return `403 PERMISSION_DENIED`. What's left of the Library API covers only media the app created itself (`photoslibrary.readonly.appcreateddata`), and Bhandaar creates none. `be/collect/photos.go` is built on the removed scopes, so it can't work for any account. The only way left to read a user's own photos is the **Photos Picker API**: the user picks items in Google Photos, and the app reads only what they picked.

## Audit of the current integration

### What exists

| Piece | Where | State |
|---|---|---|
| Collector: `Photos()`, `startPhotosScan`, `listMediaItems` (whole library), `listMediaItemsForAlbum`, `ListAlbums`, `getContentSize(AndHash)` | `be/collect/photos.go` | Dead: every call it makes needs a removed scope |
| `photosConfig`, which asks for `photoslibrary.readonly` and `photoslibrary.sharing` | `be/collect/photos.go` | Dead. Only the token's own scopes matter anyway |
| `POST /api/scans` with `ScanType: "GPhotos"` | `be/web/api.go` (`DoScansHandler`) | Reachable, but it fails. `checkScanRequest` skips GPhotos, so it doesn't check that the account has granted Photos |
| `GET /api/photos/albums?refresh_token=` | `be/web/api.go` (`ListAlbumsHandler`) | Dead, and unsafe: it takes a raw refresh token in the URL, which lands in access logs, and never checks who owns it |
| `GET /api/photos/{scan_id}` | `be/web/api.go` (`ListPhotosHandler`) | Works (it checks the owner), but there's nothing to read, and nothing in the UI calls it |
| Tables `photosmediaitem`, `photometadata`, `videometadata` | `be/db/database.go` | Created at startup. Empty in production (checked 2026-09-28): no Photos scan was ever recorded |
| `SavePhotosMediaItemToDb`, `GetPhotosMediaItemFromDb`, and `DeleteScan`'s Photos rows | `be/db/database.go` | Work |
| `db.PhotosMediaItem` (`AlbumIds` is never filled) | `be/db/types.go` | |
| Photos isn't a linkable service: `scopeServices` and `Services()` know only Gmail and Drive | `be/db/accounts.go` | `accounts_test.go` checks that `photoslibrary.readonly` maps to no service |
| UI: `ScanType.GPhotos`, `format.ts` label, a Browse tab that is always disabled ("Soon") | `ui/src/types/scans.ts`, `ui/src/routes/index.tsx` | There's no Photos option on the Request page, `serviceScopes` in `googleLink.ts` has no Photos entry, and there's no results view |

### Gaps

**Functional**

1. **It can't read anything.** Listing the library, listing albums and searching an album all need the removed scopes.
2. **Accounts can't grant Photos.** There's no Photos service in account linking (`scopeServices`, `serviceScopes`, `Service`), and no check that a Photos scan's account has granted access.
3. **Sizes and hashes came from unauthenticated `http.Get`/`http.Head` on `baseUrl`.** The Picker API's `baseUrl`s need an `Authorization: Bearer` header, so this code wouldn't work with Picker items either.
4. **No results anywhere in the UI:** no results view for a scan, and the Browse tab is disabled. `GetScanSummary` counts `scandata` for every scan type but Gmail, so a Photos scan's summary would show 0 items and 0 bytes.
5. **Nothing is recorded about what was scanned.** `SaveScanMetadata` is called with an empty search path and filter, so the album a scan covered is lost.

**Correctness and robustness** (these go away with the rewrite, but the new collector must not repeat them)

6. **Progress is published under the album ID, not the account's `client_key`** (`notification.GetPublisher(photosScan.AlbumId)`). A library scan publishes under `""`, so the Request page, which follows its account's key, never shows it.
7. **`mimeType[:5]` panics on a MIME type shorter than five characters, or an empty one** (`processMediaItem`, `getContentSize*`, `SavePhotosMediaItemToDb`).
8. **The retry loops don't retry.** In `getContentSize*`, a non-200 response `break`s after the first try. In the list loops, a non-200 response `continue`s at once, without backoff and without closing the body, and doesn't tell 403 (permanent) apart from 429 or 5xx.
9. **Timeouts:** the client's 10 s timeout would cut off an MD5 download of any video of real size.
10. **The per-item work is serial.** The `WaitGroup` and channel suggest concurrency, but `processMediaItem` runs inline, one item at a time.
11. **The data model:** `photosmediaitem` has no index on `scan_id` and no uniqueness on `(scan_id, media_item_id)`. Photo and video metadata sit in two side tables, each joined one-to-one. `ioutil` is deprecated.

**Security**

12. `GET /api/photos/albums` takes a refresh token in its query string (see above). It has to go.

## Picker API facts

Checked against Google's documentation on 2026-09-28 ([get started](https://developers.google.com/photos/picker/guides/get-started-picker), [sessions](https://developers.google.com/photos/picker/reference/rest/v1/sessions), [mediaItems](https://developers.google.com/photos/picker/reference/rest/v1/mediaItems), [mediaItems.list](https://developers.google.com/photos/picker/reference/rest/v1/mediaItems/list), [media items and base URLs](https://developers.google.com/photos/picker/guides/media-items), [2025 changes](https://developers.google.com/photos/support/updates)).

- **Scope:** `https://www.googleapis.com/auth/photospicker.mediaitems.readonly`. It's a separate API, "Google Photos Picker API", which has to be enabled in the Cloud project.
- **Session:** `POST v1/sessions` (`sessions.create`) returns `id`, `pickerUri`, `pollingConfig` (a recommended `pollInterval` and `timeoutIn`), `expireTime`, and `mediaItemsSet`. `pickingConfig.maxItemCount` defaults to 2000, which is also the most it allows. `sessions.get` is for polling. Call `sessions.delete` when done.
- **Picking:** the user opens `pickerUri`. On the web, appending `/autoclose` closes the Google Photos window when they're done. On a phone it opens the Google Photos app. `mediaItemsSet` turns `true` once they've picked.
- **Items:** `GET v1/mediaItems?sessionId=&pageSize=&pageToken=` (`pageSize` defaults to 50; at most 100). Before the user has finished picking, it returns `FAILED_PRECONDITION`.
- **`PickedMediaItem`:** `id` (persistent), `createTime`, `type` (`PHOTO` or `VIDEO`), and `mediaFile` with `baseUrl`, `mimeType`, `filename` and `mediaFileMetadata`. That metadata has `width`, `height`, `cameraMake`, `cameraModel`, then `photoMetadata` (focal length, f-number, ISO, exposure time) or `videoMetadata` (fps, `processingStatus`).
- **No file size, no hash, no `productUrl`**, and no album membership.
- **`baseUrl`:** needs a bearer token and works for 60 minutes. `=d` returns an image with its metadata *except location*. `=dv` is documented as returning a *transcoded* video, and only once `processingStatus` is `READY`. In step 0 it returned the stored copy's container (`video/quicktime` for a `.MOV`), at its stored size.

So what a Photos scan can measure is the size of the copy Google serves, not of the original in storage. For images that's close: the original minus its location EXIF. For videos it can be very different. The UI has to say so.

## Step 0 findings

Probed on 2026-09-28 with a throwaway script, against a real account: one session, 26 items picked (22 Pixel photos, 4 videos of 8 MB to 245 MB served).

- **Setup:** the "Google Photos Picker API" had to be enabled in the Cloud project. Until it was, `sessions.create` returned `403 SERVICE_DISABLED`, and it took a few minutes to take effect after enabling. The scope was granted as it is, with the client in "Testing".
- **Polling:** `pollInterval` was `5s`, and `timeoutIn` was 30 minutes from creation. That's the time the user has to pick. `expireTime` was **7 days** after creation. Once `mediaItemsSet` was true, `pollingConfig` was absent. `sessions.delete` returned `200 {}`.
- **Item fields** were exactly the documented ones: `id`, `createTime`, `type`, and `mediaFile` (`baseUrl`, `mimeType`, `filename`, `mediaFileMetadata`). Videos had no `cameraMake` or `cameraModel`. A `.MOV` was reported as `mimeType: video/mp4`, but its `=dv` bytes came with `Content-Type: video/quicktime`.
- **`HEAD` works.** All 26 answered `200` with a `Content-Length`, and it matched the bytes of a full `GET` every time. Without the bearer token, every request got `403`. So the download fallback should almost never run, and MD5s will almost never be recorded.
- **`HEAD` isn't free for videos.** It took up to 14.5 s for the largest video (30.9 s for all 4), but at most 2.1 s for a photo (21.6 s for all 22). Google appears to prepare the video before answering. Downloads right after it were quicker (10.4 s for 245 MB).
- **Served video sizes are far below the originals.** Three of the videos also exist on agent drives 1 and 2, matched by filename; the hashes can't be compared (blake3 against MD5):

  | File | Served (`=dv`) | Original on the agent drives | Served / original |
  |---|---|---|---|
  | `JYO_3459.MOV` | 81,756,503 | 635,078,464 | 13 % |
  | `VID_4648.MOV` | 7,908,979 | 402,732,200 | 2 % |
  | `VID_4728.MOV` | 17,570,058 | 769,757,208 | 2.3 % |

  None of the photos were on the agent drives, so photo sizes couldn't be compared this way.

  **Why:** the owner checked `VID_4648.MOV` in Google Photos. It was uploaded from a web browser at **Storage saver** quality, and its info panel says "This item doesn't take up space in your account storage". So the small size is Google's stored, compressed copy, not a transcode on download. The info panel shows no file size, so served and stored can't be matched byte for byte, but they agree. Two things follow. The size a scan records is the size of the copy Google keeps, which can be far smaller than the file on the owner's disks. And an item can take no quota at all: Storage saver uploads from before June 2021 are free. The Picker API reports neither the upload quality nor whether an item counts against quota.
- **Production has no old Photos data:** `photosmediaitem`, `photometadata` and `videometadata` were empty, and the only scans were 5 Gmail scans.

## Decisions

Taken with the owner on 2026-09-28:

| Question | Decision |
|---|---|
| Sizes | **Try `HEAD`, else download.** `HEAD` on `baseUrl=d` or `=dv` and read `Content-Length`. If there's none, or `HEAD` is refused, stream a `GET` and count the bytes. Label them as the size in Google Photos, not the original's, and not necessarily quota |
| Existing Photos code and tables | **Drop them.** Production has no Photos rows, so `photosmediaitem`, `photometadata` and `videometadata` are dropped, along with the Library API code, the albums route and the old read path. Picker scans go to new tables. (First decided as "keep the old rows read-only"; changed once step 0 found them empty) |
| Browse | **Yes, like Gmail.** The Photos tab lists the account's picked items across its Photos scans, sorted by size or by date |
| Scope | **Picker only.** No Google Takeout import |

## Design

### What a Photos scan is

A Photos scan is **one picking session**: the items the user picked from one Google account in one go (up to 2000). It isn't a snapshot of the library, and scans don't add up to one. Browse shows the union of every scan's items, each item once.

### Flow

```
UI (Request, ?type=photos)          be                                 Google
─────────────────────────           ──                                 ──────
[Pick photos] ── POST /api/photos/sessions {clientKey} ──►
                                    sessions.create (maxItemCount 2000) ──►
                                    ◄── {id, pickerUri, pollingConfig, expireTime}
                                    save photos_picker_sessions row, start poller
◄── {sessionKey, pickerUri} ────────
open pickerUri + "/autoclose"
(in a window opened on the click)
                                    poll sessions.get every pollInterval
                                    until mediaItemsSet, timeoutIn or expireTime
GET /api/photos/sessions/{key}
every 3 s → {state: "waiting"}
                                    mediaItemsSet → LogStartScan("google_photos"),
                                    collector lists items, sizes them, saves them,
                                    sessions.delete; progress over SSE by client_key
◄── {state: "scanning", scanId} ────
live progress (SSE), then a link to the scan's results
```

The backend owns the Picker session. The access token, the Picker session ID and the polling all stay server side. The UI only ever sees `pickerUri` and the backend's own session key. Anything the UI would need in order to talk to Google itself (Google's JS client, a browser access token) stays out of it.

**The popup:** browsers block `window.open` outside a click. So the click handler opens a blank window straight away, `POST`s, and then sets that window's `location` to `pickerUri + "/autoclose"`. If `window.open` returns `null`, the UI shows `pickerUri` as a link instead ("Open Google Photos").

**States** (`photos_picker_sessions.state`):

- `waiting`: created, not picked yet.
- `scanning`: picked, and the collector is running (`scan_id` set).
- `done`: the scan finished (`scan_id`'s status says whether it succeeded).
- `expired`: `timeoutIn` or `expireTime` passed before the user picked. There's no scan, and the UI offers **Pick again**.
- `cancelled`: `DELETE /api/photos/sessions/{key}` from the UI. The backend calls `sessions.delete` too.

Picking with no scan left behind (`expired`, `cancelled`) creates no `scans` row, so Request History shows only real scans. A Picker session that is left open is deleted when it expires or when the backend restarts: at startup, rows still in `waiting` are marked `expired`, and rows in `scanning` are marked `done`. `markInterruptedScans` already marks their scans Failed, as it does for every scan type. Picker sessions expire on Google's side by themselves.

### Linking: Photos as a service

- `be/db/accounts.go`: add `ServicePhotos = "photos"`, and map `https://www.googleapis.com/auth/photospicker.mediaitems.readonly` to it in `scopeServices`, in the fixed order Gmail, Drive, Photos. The old `photoslibrary.*` scopes still map to nothing: the existing test stays, and gains a case for the new scope.
- `ui/src/types/accounts.ts`: `Service = "gmail" | "drive" | "photos"`. `ui/src/googleLink.ts`: add `photos` to `serviceScopes`. Linking works as it does for Gmail and Drive (incremental, `include_granted_scopes`, and an account without Photos gets a **Grant Photos access** button).
- `/api/glink` is unchanged. It identifies the account by the `id_token` whatever was granted.

### Backend

**Routes** (in `be/web/photos.go`, all behind the session middleware and the owner checks Browse uses):

| Route | What it does |
|---|---|
| `POST /api/photos/sessions` `{clientKey}` | Checks that the account is the user's and has granted Photos (it reuses `checkScanRequest`'s messages), that the user has no other session in `waiting` or `scanning`, and creates a Picker session. Returns `{sessionKey, pickerUri, expireTime}` |
| `GET /api/photos/sessions/{key}` | `{state, scanId?, expireTime}`. 404 unless it's the user's |
| `DELETE /api/photos/sessions/{key}` | Cancels a `waiting` session |
| `GET /api/photos/{scan_id}?page=` | Rewritten: a page of a `google_photos` scan's `photos_picked_items`, as plain JSON values (no `sql.Null*` objects), like the other results routes since #33 |

`POST /api/scans` with `ScanType: "GPhotos"` answers `400` ("Start a Photos scan by picking photos on the Request page"). `GPhotosScan` and `DoScanRequest.GPhotosScan` are removed.

**Collector** (`be/collect/photos.go`, rewritten):

- `photosPickerConfig`: `sync.OnceValue`, like the others, using the linked account's refresh token through `resolveAccount`. Its `Scopes` field lists only the Picker scope. The token's grant is what counts.
- `photosApiBaseUrl` becomes `https://photospicker.googleapis.com/`, a package var that tests point at an `httptest` server (as `drive_test.go` and `gmail_test.go` do).
- **The poller** (one goroutine per session): calls `sessions.get` every `pollInterval` (at least 1 s), stops at `timeoutIn` or `expireTime`, and backs off on 429 or 5xx. When `mediaItemsSet` is true, it starts the scan.
- **The scan:** `LogStartScan("google_photos", userID)` and `SaveScanMetadata(name, clientKey, "Picked in Google Photos", "", scanId)`. It takes the same `lock` as the other collectors, and publishes progress under the account's **`client_key`** (gap 6).
  1. List every picked item (`pageSize=100`) into memory. There are at most 2000.
  2. Size them with a pool of 4 workers, behind the rate limiter. For each item:
     - Use `=d` for `PHOTO` and `=dv` for `VIDEO`, decided by `type`, not by slicing `mimeType` (gap 7). A video whose `processingStatus` isn't `READY` gets no size (`size_source = 'unavailable'`).
     - `HEAD` with the bearer token. A `Content-Length` of 0 or more gives `size_source = 'head'`.
     - Otherwise, a streamed `GET` that counts the bytes, and hashes them as it goes, gives `size_source = 'download'` and an MD5. The MD5 is of the served copy, and is only stored when the item was downloaded.
     - Retry 429, 5xx and network errors 3 times, with exponential backoff (1 s, 2 s, 4 s). A 401 or 403 fails that item (`size_source = 'unavailable'`) without retrying. Always close the body (gap 8).
     - No overall client timeout on downloads (gap 9). Each request has a context with a 10-minute limit instead.
  3. If sizing runs past 50 minutes from the listing, list the items again before carrying on, to get fresh `baseUrl`s (they work for 60 minutes).
  4. Save the rows in batches of 100, in one transaction per batch (`photos_picked_items`).
  5. Call `sessions.delete` and mark the scan completed. A scan with some items it couldn't size still completes, and its summary counts them.
- `ListAlbums`, `listMediaItems`, `listMediaItemsForAlbum`, `getContentSize*`, the old `photosConfig` and the Library API types are deleted, as are `db.PhotosMediaItem`, `PhotosMediaItemRead`, `SavePhotosMediaItemToDb` and `GetPhotosMediaItemFromDb`.

**Tables** (created in `database.go` next to the others; nothing is migrated):

```sql
CREATE TABLE IF NOT EXISTS photos_picker_sessions (
  id           serial PRIMARY KEY,
  session_key  TEXT NOT NULL UNIQUE,     -- random, what the UI holds
  picker_id    TEXT NOT NULL,            -- Google's session id; never sent to the UI
  user_id      BIGINT NOT NULL,
  client_key   VARCHAR(100) NOT NULL,
  picker_uri   TEXT NOT NULL,
  state        VARCHAR(20) NOT NULL,     -- waiting | scanning | done | expired | cancelled
  expire_time  TIMESTAMPTZ NOT NULL,
  scan_id      INT REFERENCES scans (id) ON DELETE SET NULL,
  created_on   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS photos_picked_items (
  id            serial PRIMARY KEY,
  scan_id       INT NOT NULL REFERENCES scans (id),
  media_item_id TEXT NOT NULL,           -- Google's persistent id
  media_type    VARCHAR(10) NOT NULL,    -- PHOTO | VIDEO
  mime_type     TEXT,
  filename      TEXT NOT NULL,
  create_time   TIMESTAMPTZ,
  width         INT,
  height        INT,
  camera_make   TEXT,
  camera_model  TEXT,
  focal_length  NUMERIC,
  f_number      NUMERIC,
  iso           INT,
  exposure_time TEXT,
  fps           NUMERIC,
  size          BIGINT,                  -- bytes of the served copy; NULL when unavailable
  size_source   VARCHAR(12) NOT NULL,    -- head | download | unavailable
  md5hash       TEXT,                    -- only when downloaded
  UNIQUE (scan_id, media_item_id)
);
CREATE INDEX IF NOT EXISTS photos_picked_items_scan ON photos_picked_items (scan_id);
```

One flat table, not an item table with a photo and a video side table: the metadata is small, and one table keeps the reads single queries.

**Around the scan:**

- `DeleteScan` deletes `photos_picked_items` rows before `scans`. `photos_picker_sessions.scan_id` is set to NULL on its own.
- `GetScanSummary`: a `google_photos` case, where `item_count` counts `photos_picked_items`, `total_bytes` sums `size`, and `folder_count` is 0.
- `ScanSummary` gains `unsized_count` (items with no size), which the results view shows.

### UI

**Request page** (`?type=photos`): a third tab next to Gmail and Google Drive, with an account picker that offers **Grant Photos access** where needed, and one button, **Pick photos in Google Photos**. The page explains, above the button, that:

- the scan covers only what you pick (up to 2000 items), and each scan is its own pick;
- sizes are of the copy Google Photos keeps: for items uploaded at Storage saver quality, that can be far smaller than the original file. Items uploaded at Storage saver before June 2021 take no quota at all, and Google doesn't say which items those are, so the total isn't your quota use.

While a session is `waiting`, the page shows "Waiting for you to pick in Google Photos…", a **Cancel** button, and the expiry time. Then come live progress (SSE by `client_key`, as with Gmail and Drive) and a link to the scan. On `expired`, it shows **Pick again**.

**Results view** (`scans.$scanId.tsx`) for `google_photos`: summary (items, total bytes, and "N without a size"), then a page of items: filename, type, taken (`create_time`), dimensions, camera, size (with a "~" and a tooltip saying how it was measured). There's no link to Google Photos, because the Picker gives no `productUrl`.

**Browse:** the Photos tab is enabled for accounts that have granted Photos, or that have Photos scans. It shows a sub-line of totals, like Gmail's, and a sortable list (`?sort=size|date`, 50 a page) from:

```
GET /api/browse/google/{client_key}/photos/items?sort=size|date&page=
```

Each `media_item_id` appears once, from its latest scan (`DISTINCT ON (media_item_id) … ORDER BY media_item_id, scan_id DESC`), under the same owner checks as `/gmail/messages`. `BrowseSources` gains `ServicePhotos` totals (distinct items, the sum of their latest sizes). An empty state says "Nothing picked yet" and links to the Request page.

### Google Cloud console

- Enable **Google Photos Picker API** in the OAuth client's project.
- While the client is in "Testing", its test users can grant the new scope as it is. Add it to the consent screen when the app is published (review item 2.11). The 7-day refresh token expiry of "Testing" (2.11) matters less here, since a pick is done in one sitting.
- Before relying on storing picked items' metadata for good, read the [Google Photos APIs user data policy](https://developers.google.com/photos/support/api-policy) for rules on keeping it. See [open questions](#open-questions).

## Tests

- `be/collect/photos_test.go` (fake Picker API through `httptest`): session creation; polling until `mediaItemsSet`, and until `timeoutIn`; paging through `mediaItems`; `HEAD` with a `Content-Length`, `HEAD` without one (falls back to download and MD5), `HEAD` 405, 403 (no retry), 429 then 200 (retried); a video not `READY`; an empty or short `mimeType` (no panic); the bearer token sent on `baseUrl` requests; progress published under the `client_key`; `sessions.delete` called.
- `be/db/photos_test.go` (needs `BE_TEST_DB`): saving and paging picked items; `(scan_id, media_item_id)` uniqueness; summary totals for `google_photos` and `photos`; Browse's one-row-per-item across scans; `DeleteScan`; marking sessions at startup.
- `be/web/photos_test.go`: owner checks on every route, the Photos grant check, one active session per user, `GPhotos` in `POST /api/scans` refused, and `/api/photos/albums` gone (404).
- `be/db/accounts_test.go`: the Picker scope maps to `photos`, and the old scopes still map to nothing.
- UI (`src/test/`): the Request page's Photos tab (grant button, popup-blocked fallback link, waiting, cancel, expired), the results view for both scan types, and the Browse Photos tab (enabled and disabled, sorting).

## Implementation plan

One PR for the whole feature (branch `photos-picker`), with a commit per step. Each step leaves the branch working.

0. **Check before building** (done 2026-09-28; see [Step 0 findings](#step-0-findings)). A throwaway script, not merged: with a test account, create a session, pick a few photos and a video, and record: whether `HEAD` on `=d` and `=dv` answers `200` with a `Content-Length`; how the sizes compare with the originals (checked by hand in Google Photos); how long a `=dv` download takes; the real `pollingConfig` values. Count old `photos` scans in production with the read-only query recipe. Record the findings in this spec.
1. **Remove the dead code and tables** (done on branch `photos-picker`; tested by `TestPhotosLibraryAPIIsGone` and `TestMigrateDropsPhotosLibraryTables`): `ListAlbums`, the albums route, the Library API listing and sizing, `photosConfig`, `ListPhotosHandler` and `GET /api/photos/{scan_id}`, the old save and read functions, `DeleteScan`'s three Photos deletions, and the three tables (`DROP TABLE IF EXISTS videometadata, photometadata, photosmediaitem` at startup, next to the `CREATE`s, which go). `POST /api/scans` refuses `GPhotos`. Close gap 12 here. Step 3 brings `GET /api/photos/{scan_id}` back for Picker scans.
2. **Photos as a linkable service** (done; the Picker API was enabled on 2026-09-28 for step 0): `ServicePhotos`, the scope map, the UI `Service` type, `serviceScopes`, and the tests. Enable the Picker API in the Cloud project.
3. **Backend Picker scan:** the tables, the collector, the session routes, the startup cleanup, `DeleteScan`, `GetScanSummary`, `/api/photos/{scan_id}` for `google_photos`, and the backend tests.
4. **Request page:** the Photos tab, picking, waiting, progress, and the UI tests.
5. **Results view and Browse:** the results view for both types, the Browse route and totals, the Photos tab enabled, and the tests.
6. **Docs:** `architecture.md` (Photos back in the diagrams, routes, scopes and tables), `README.md`, `CLAUDE.md` (routes, `photos.go`, tests), and this spec's status to implemented, with an "As built" section. Then move it to `docs/archive/`, and move 7.15 to `codebase-review-history.md` with a change-log row.

## Open questions

1. ~~**What do video sizes mean?**~~ Settled on 2026-09-28: the sizes are those of Google's stored copy (Storage saver). See [Step 0 findings](#step-0-findings). Record every size, show videos like photos, and use the wording above on the Request page, in the results view and on the Browse tab's totals.
2. **Keeping metadata:** can picked items' metadata be kept indefinitely under Google's user data policy, or only as long as the user keeps access? The policy (read 2026-09-28) sets no retention period. It limits use to features visible in the app, and requires the data to be encrypted at rest and in transit. Check whether the prod database's disk is encrypted, and whether that is enough. If a retention limit turns up, Browse's union across scans needs a retention rule.
3. **Items picked again:** the same item in two scans has two rows (one per scan). Browse deduplicates them, but the total over all Request History doesn't. That's intended, since each scan is its own pick. Say so if not.
