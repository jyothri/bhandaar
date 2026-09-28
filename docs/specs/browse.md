# Browse: Files and Folders by Account and Drive

**Status:** proposed. Written 2026-09-27, after [request-drive-scans.md](request-drive-scans.md) steps 1–5. Modelled on `driveagent`'s HTML report ([drive-comparison-agent.md](drive-comparison-agent.md#report)).

## Problem

A scan's results view (`/scans/<id>`) shows what one scan found. To see what's in a Google account's Drive, or on a disk, you have to know which scans covered which folders, and piece them together. `driveagent`'s HTML report shows the other way round: one collapsible tree per drive, with folder sizes, whatever scans built it. The web UI should show both kinds of source that way:
- **Google Drive accounts:** what the user's Drive scans found, merged into one current view per linked account.
- **`driveagent` drives:** what agents uploaded to `agentserver`. It's already one record per drive, kept current by every scan and `sync`.

The per-scan results view stays, for Gmail and for seeing what one scan found.

## Scope

In, for the first version:
1. A **Browse** page: pick a source (one of your Google accounts, or one of your agent drives), then a collapsible folder tree, loaded one folder at a time, with each folder's total size and file count, and each file's size and modified time. Drive files link to Drive.
2. A **living record per Google account**, updated by every Drive scan, which is what Browse shows for Drive.
3. **Duplicates:** files with the same content, within the selected source, or across all your sources of the same kind.

Out:
- Comparison badges (`common`, `diverged`, `missing`, `relocated`). `driveagent compare` computes them on the machine, and they aren't uploaded.
- Search by name or path.
- Duplicates between a Drive file and a disk file. Drive gives MD5 and `driveagent` hashes with BLAKE3, so no content hash is shared between the two kinds.
- Gmail and Photos.

## Sources

`GET /api/browse/sources` lists what the user can browse:

```json
[
  { "kind": "drive", "key": "JVVYZFCI0PaW", "name": "jyo****ri@gmail.com",
    "files": 504, "bytes": 3961548800, "updated_at": "2026-09-27T…" },
  { "kind": "agent", "key": "1", "name": "seagate1 (optiplex7070)",
    "files": 888902, "bytes": 1696…, "updated_at": "2026-09-27T…",
    "physical_drive": 1 }
]
```

- **Drive:** each of the user's linked accounts that has a living record. `key` is the `client_key`.
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

Each subfolder gets its totals from `agent_files` rows under its path.

Measured on the prod copy's largest drive (888,902 files), with no new index:
- the root's children with totals take 0.34 s
- a folder with 23,000 files under it takes 0.75 s

That's fine for one folder at a time. If it gets slow, `be` can keep its own totals table per drive, refreshed when the drive's highest `agent_files.row_version` changes. An index on `agent_files (drive_pk, relative_path text_pattern_ops)` would need an `agentserver` migration, since `agentserver` owns those tables.

Files with `status = 'error'` (unreadable when scanned) are shown with the error instead of a size.

## Browse API

Every route checks the source is the user's: 404 otherwise, like scans.

- `GET /api/browse/{kind}/{key}/children?folder=<id>&page=<n>`: one folder's contents.
  - `folder` is a Drive folder ID, or an agent folder's relative path (URL-encoded); empty means the root. For Drive, the roots are `My Drive` and `Shared with me`.
  - Returns the folder's path (for the breadcrumb), its subfolders with `files` and `bytes`, largest first, then its files, largest first.
  - Pages hold 200 entries, for folders with thousands of files.
- `GET /api/browse/{kind}/{key}/duplicates?across=source|all&page=<n>`: groups of files with the same content hash (MD5 for Drive, BLAKE3 for agents) and size over 0.
  - `across=source` looks within this source. `across=all` looks across all the user's sources of this kind, leaving out copies of the same physical drive, which would otherwise make every file on them a duplicate.
  - Groups are sorted by wasted bytes (size × (copies − 1)); each lists its copies' sources and paths.
  - Measured on the prod copy: 129,856 duplicate groups within seagate1, found in 0.76 s.

## Browse page

A new route `/browse`, in the nav after Request History, with search params `source` (`drive:<client_key>` or `agent:<id>`), `folder`, and `tab` (`tree` or `duplicates`).

```
 Source  [ seagate1 (optiplex7070) ▾ ]   888,902 files · 1.5 TB · updated 2h ago
 [ Tree ] [ Duplicates ]

 Jyo / media                                          (breadcrumb)
 ▸ Photos 2019/            412 GB   18,210 files
 ▸ Videos/                  51 GB      640 files
   IMG_0001.JPG            6.2 MB   2019-07-28
   …                                              [ More ]
```

- **Tree:** each folder is a `<details>`, as in the report. Expanding it loads its children (one call, cached by TanStack Query). Clicking a folder's name moves the breadcrumb there. Sizes use `formatBytes`. Drive entries link to Drive (as in the results view); agent entries show their path on the drive.
- **Duplicates:** a paged list of groups, largest waste first. Each group shows its size, how many copies there are, and each copy's source and path (linked to that folder in the tree). There's a toggle for this source or all sources.
- **Linking from the results view:** a Drive scan's results page gets a link to Browse for its account.

## Implementation order

One PR each, each checked on dev.sm against the prod copy:

1. **Drive living record:**
   - the `drive_items` table
   - requesting `ownedByMe` from Drive
   - upserts from scans
   - deletion for unfiltered complete scans
   - the one-off replay on the dev copy
   - tests with the fake Drive API: add, update, rename, move, delete in scope, no deletes from a filtered or failed scan
2. **Browse API:** sources, and children for both kinds, with tests against `drive_items` and an `agent_files` fixture.
3. **Browse page:** the tree and breadcrumb, with a link from Drive results.
4. **Duplicates:** the API and the tab.

## Open questions

- **`driveagent` on the web:** whether Browse should also show an agent drive's last scan runs (`agent_scan_runs`) and sync status, as `driveagent remote-status` does. Not needed for the tree.
- **Totals for very large Drive accounts:** `drive_items` totals are a recursive query over the account's rows. That's fine up to hundreds of thousands of rows; above that it would need the same kind of cached totals as agent drives.
