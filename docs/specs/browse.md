# Browse: Files and Folders by Account and Drive

**Status:** proposed. Written 2026-09-27, after [request-drive-scans.md](request-drive-scans.md) steps 1–5. Modelled on `driveagent`'s HTML report ([drive-comparison-agent.md](drive-comparison-agent.md#report)).

## Problem

A scan's results view (`/scans/<id>`) shows what one scan found. To see what's in a Google account's Drive, or on a disk, you have to know which scans covered which folders, and piece them together. `driveagent`'s HTML report shows the other way round: one collapsible tree per drive, with folder sizes, whatever scans built it. The web UI should show both kinds of source that way:
- **Google Drive accounts:** what the user's Drive scans found, merged into one current view per linked account.
- **`driveagent` drives:** what agents uploaded to `agentserver`. It's already one record per drive, kept current by every scan and `sync`.

The per-scan results view stays, for seeing what one scan found. Browse becomes the landing page: what you have, rather than how it was scanned.

## Scope

In, for the first version:
1. A **Browse** page, the default landing page and the first item in the nav. Pick a source: one of your Google accounts or one of your agent drives. For a Google account, pick a service (Google Drive or Gmail) too.
2. **Google Drive and agent drives:** a collapsible folder tree, loaded one folder at a time. Each folder shows its total size and file count, and each file its size and modified time. Drive files link to Drive.
3. **Gmail:** the account's messages across all its Gmail scans, largest or newest first.
4. A **living record per Google account**, updated by every Drive scan, which is what Browse shows for Drive.

Out:
- Comparison badges (`common`, `diverged`, `missing`, `relocated`). `driveagent compare` computes them on the machine, and they aren't uploaded.
- Search by name or path.
- **Duplicates** (files with the same content), for now. Notes for later:
  - They can only be found within each kind of source. Drive gives MD5 and `driveagent` hashes with BLAKE3, so no content hash is shared between a Drive file and a disk file.
  - Across agent drives, copies of the same physical drive (seagate1 is uploaded from three machines) have to be left out, or every file on them is a duplicate.
  - Measured on the prod copy: 129,856 groups of identical files within seagate1, found in 0.76 s with the existing `agent_files (drive_pk, content_hash)` index.
  - The data needed is already kept: `drive_items.md5` and `agent_files.content_hash`.
- Photos: shown as a service, but disabled, until review item 7.15 is decided.

## Sources

`GET /api/browse/sources` lists what the user can browse:

```json
[
  { "kind": "google", "key": "JVVYZFCI0PaW", "name": "jyo****ri@gmail.com",
    "services": {
      "drive": { "granted": true, "files": 504, "bytes": 3961548800, "updated_at": "2026-09-27T…" },
      "gmail": { "granted": true, "files": 5910, "bytes": 412…, "updated_at": "2026-09-27T…" } } },
  { "kind": "agent", "key": "1", "name": "seagate1 (optiplex7070)",
    "files": 888902, "bytes": 1696…, "updated_at": "2026-09-27T…",
    "physical_drive": 1 }
]
```

- **Google:** each of the user's linked accounts, whether or not it has been scanned. For each service: whether the account has granted it (as `/api/accounts` says), and the totals of what's recorded, or no totals if nothing is yet. Drive's come from the living record; Gmail's are the account's messages (count and `size_estimate`). `key` is the `client_key`.
- **Agent:** each `agent_drives` row of the user's agents (`agent_agents.user_id`), named `<drive_id> (<hostname>)`. `key` is the row's `id`. One per drive per machine, as the HTML report has one tab per drive: seagate1 uploaded from three machines is three sources. `physical_drive` is set when copies are linked, and the UI shows it (`linked copy of physical drive 1`).

## Drive: a living record per account

Drive scans keep their per-scan rows in `scandata`, for the results view. Each scan also updates one record per account, keyed by Drive file ID, like `driveagent`'s one record per drive.

### Table

`drive_items`, owned by `be`:

| Column | |
|---|---|
| `client_key` | the account; part of the key |
| `file_id` | the Drive ID; part of the key |
| `parent_id` | the first parent's Drive ID; NULL at a root |
| `name`, `is_dir`, `mime_type`, `size`, `md5`, `modified` | as Drive gives them |
| `owned_by_me`, `trashed` | from Drive's `ownedByMe` and `trashed` fields, which the scan starts requesting |
| `last_seen_scan`, `last_seen_at` | the last scan that saw it |

Folders are rows too (`is_dir`), from every folder a scan walks or lists, and from the parents a folder scan looks up for its path. The tree is kept by `parent_id`, not by path. So a folder renamed or moved in Drive is right as soon as a scan sees the folder itself, and paths are built when browsing, from the parents. The roots are `My Drive` (its folder ID, from `files.get("root")`, is kept per account) and `Shared with me` (items whose parent isn't in the record).

### Updating it

Each file and folder a scan saves is upserted into `drive_items`, with `last_seen_scan` set to the scan. This happens in the same goroutine that writes `scandata`.

**Deletions** are applied only when a scan completes, and only by a scan that saw everything in its scope. Otherwise a filtered scan would delete everything its filter left out. A scan saw everything when its query is one of the Request page's unfiltered defaults:
- `mimeType != '<folder>' and trashed = false`
- the same `and 'me' in owners`

These are compared as exact strings. Anything else (file types, dates, an edited query) adds and updates rows but never deletes. Its scope is the scanned folder's subtree in `drive_items` (or the whole account for a whole-Drive scan). There, every row not seen by this scan is deleted, among files that its query would have matched: not trashed, and `owned_by_me` if it had `'me' in owners`. Folders in scope not seen are deleted too, if they're empty afterwards.

A scan that fails leaves the record with whatever it upserted, and deletes nothing.

### Existing scans

Production has none. On the dev copy, scans 8–11 can be replayed into `drive_items` once (oldest first) to try this out; it isn't needed in production.

## Agent drives

Read straight from `agentserver`'s tables, which `be` already shares the database with (as it reads `agent_users`): `agent_files` (one row per file: `relative_path`, `size`, `mtime`, `content_hash`, `status`) and `agent_dir_listings` (every directory's entries, including folders never scanned). `be` never writes them.

Paths are relative to the drive's root. A folder's children are:
- its subfolders, from `agent_dir_listings` (`parent_key` = the folder's key, `is_dir`), so folders with nothing scanned under them still show, as in the report
- its files, from `agent_files`

Each subfolder's totals come from the cache below.

Files with `status = 'error'` (unreadable when scanned) are shown with the error instead of a size.

## Folder totals cache

Every folder's total size and file count are cached from the start, for both kinds of source, so opening a folder never adds up its whole subtree.

**Table** `browse_folder_totals`, owned by `be`: `(source, folder, files, bytes)`, where:
- `source` is `drive:<client_key>` or `agent:<id>`
- `folder` is a Drive folder ID, or an agent folder's relative path (`''` for the root)

A second table, `browse_totals_state (source, version, built_at, building)`, records what each source's totals were built from.

**Agent drives:**
- **Version check:** a drive's version is its highest `agent_files.row_version`, which every upload raises.
- **Background checker:** a goroutine in `be`, every 10 minutes, rebuilds any drive whose version differs from the one recorded. One drive at a time, in a transaction that replaces its rows.
- **Rebuild query:** each file counts in every folder above it, via `string_to_array(relative_path, '/')` and `generate_series`, grouped by folder.
- **Measured on the prod copy's largest drive** (888,902 files):
  - a rebuild takes 23 s and yields 99,371 folders
  - the version check takes 74 ms
  - for comparison, adding up one folder live took 0.34–0.75 s, which is what a cache hit saves on every open

**Drive accounts:** rebuilt from `drive_items` when a Drive scan finishes, by a recursive query up `parent_id`. This runs in the goroutine that saved the scan, after the upserts and deletions.

**While a rebuild runs, or after a change,** Browse shows the totals already cached, with "updating totals…" next to the source's totals. Only a source with no cached totals yet (first time) computes a folder's totals live, and the next check builds the cache.

**When `be` starts,** the first check runs at once, so a new or restarted `be` builds any missing totals within about a minute per large drive.

If rebuilds of whole drives ever get too slow, they can become incremental: re-add only the folders above files whose `row_version` is newer than the last build. Not needed at 23 s.

## Gmail

A Gmail scan saves each message once per account: a message a later scan finds again isn't saved again (review 7.9's decision). So an account's messages are the `messagemetadata` rows of all the account's Gmail scans (`scanmetadata.client_key`), with no repeats, whatever filters the scans used. Browse lists them as they are. It can't tell which were deleted in Gmail since, which the page says. Nothing new is stored.

## Browse API

Every route checks the source is the user's: 404 otherwise, like scans. `{source}` is `google/<client_key>` or `agent/<id>`.

- `GET /api/browse/google/<client_key>/drive/children?folder=<id>&page=<n>` and `GET /api/browse/agent/<id>/children?folder=<path>&page=<n>`: one folder's contents.
  - `folder` is a Drive folder ID, or an agent folder's relative path (URL-encoded); empty means the root. For Drive, the roots are `My Drive` and `Shared with me`.
  - Returns the folder's path (for the breadcrumb), its subfolders with `files` and `bytes`, largest first, then its files, largest first.
  - Pages hold 200 entries, for folders with thousands of files.
- `GET /api/browse/agent/<id>/status`: the drive's last scan run (`agent_scan_runs`: start, end, folder, files seen, outcome), `last_synced_at`, and its physical drive, if linked. Also whether the drive's folder totals are being rebuilt.
- `GET /api/browse/google/<client_key>/gmail/messages?sort=size|date&page=<n>`: the account's messages, 50 a page: from, subject, date, size, labels, and the scan that found each (linked to its results).

## Browse page

Browse is the index route, `/`: the landing page after login, and the first nav item (**Browse · Request · Request History**). It replaces the placeholder "Data" page. Search params: `source` (`google:<client_key>` or `agent:<id>`), `service` (`drive` or `gmail`, for a Google source), `folder`, and for Gmail `sort`.

**Agent drive status:** under an agent drive's totals, a status line, **collapsed by default** (a `<details>`). Its summary reads e.g. `Last scan 2h ago · synced 2h ago`. Expanded, it shows:
- the last scan: when it started and ended, the folder it covered, the files it saw, and whether it completed or was interrupted (`agent_scan_runs`)
- the last sync (`agent_drives.last_synced_at`)
- the linked physical drive, if any

It comes from `GET /api/browse/agent/<id>/status`. There's no separate status page; `driveagent remote-status` stays the full view.

**Picking what to browse:**
1. **Source:** one dropdown, with two groups: *Google accounts* (by display name, with the key suffix when two share one) and *Drives* (`seagate1 (optiplex7070)`, …). With no source in the URL, the page opens on the last one used (kept in `localStorage`, a per-browser convenience), else the first. With no sources at all, it points to the Request page to link an account, and to `driveagent` for drives.
2. **Service**, for a Google account: a row of tabs, **Google Drive · Gmail · Photos**. Each shows its totals (`504 files · 3.7 GB`).
   - A service the account hasn't granted, or hasn't scanned, still shows. It says so, with a link to the Request page with that service and account already chosen (`/request?type=drive&account=<client_key>`).
   - Photos is always disabled, with a note (7.15).
   - The service defaults to Google Drive, or the last one used for that account.

   Agent drives go straight to their tree.

```
 Source  [ jyo****ri@gmail.com ▾ ]            Google accounts / Drives
 [ Google Drive  504 files · 3.7 GB ]  [ Gmail  5,910 · 412 MB ]  [ Photos ]

 Browse › jyo****ri@gmail.com › Google Drive › My Drive › from Apple Mac
 ▸ Desktop/                6.7 MB       13 files
 ▸ Pictures/               3.1 GB      402 files
   notes.txt               2.0 KB   2026-09-01
   …                                              [ More ]
```

- **Trail:** as on Request History, a trail under the nav, `Browse › <source> › <service> › <folder path>`. Each part links back up.
- **Tree:** each folder is a `<details>`, as in the report. Expanding it loads its children (one call, cached by TanStack Query). Clicking a folder's name moves the breadcrumb there. Sizes use `formatBytes`. Drive entries link to Drive (as in the results view); agent entries show their path on the drive.
- **Gmail:** a table of messages (From, Subject, Date, Size), sorted by size (largest first) or by date, 50 a page. Each row links to the scan that found it.
- **Linking from the results view:** a scan's results page links to Browse for its account and service (Drive or Gmail).

## Implementation order

One PR each, each checked on dev.sm against the prod copy:

1. **Drive living record:**
   - the `drive_items` table
   - requesting `ownedByMe` from Drive
   - upserts from scans
   - deletion for unfiltered complete scans
   - the one-off replay on the dev copy
   - tests with the fake Drive API: add, update, rename, move, delete in scope, no deletes from a filtered or failed scan
2. **Browse API and totals cache:**
   - sources, folder children for Drive and agent drives, and Gmail messages
   - an agent drive's status
   - `browse_folder_totals`, with the background checker and the rebuild after Drive scans
   - tests against `drive_items`, an `agent_files` fixture and `messagemetadata`, including a rebuild when the version changes and the live fallback before the first build
3. **Browse page:**
   - `/` becomes Browse (the "Data" placeholder goes), first in the nav
   - the source dropdown and service tabs
   - the tree, the Gmail list and the trail
   - links from the results view

## Decisions

Resolved 2026-09-28:
- **Agent drive status:** a status line under the drive's totals, collapsed by default, with the last scan, last sync and linked physical drive. See [Browse page](#browse-page).
- **Folder totals:** cached from the start, for every source. See [Folder totals cache](#folder-totals-cache).
