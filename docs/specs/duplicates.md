# Duplicates: Identical Files and Folders Across Every Source

**Status:** in progress: step 0 done, step 1 (driveagent 0.6.0, agentserver migration 4) implemented. Written 2026-10-02, after [browse.md](../archive/browse.md), [gcs-scans.md](../archive/gcs-scans.md) and [photos-picker.md](../archive/photos-picker.md) were built.

## Problem

The same file is often stored more than once: twice on one drive, on both seagate1 and seagate2, in Google Drive and in a Cloud Storage bucket, or as a photo in Google Photos and a JPEG on a disk. Whole folders get copied too. Bhandaar records all of these sources, but nothing compares them, so the space a copy wastes is invisible.

A **Duplicates** page should list what is stored more than once, within a source and across sources, largest reclaimable space first.

## Scope

In, for the first version:
1. **Identical files**, by content hash, within and across: Google Drive, Cloud Storage, and agent drives.
2. **Identical folders**: every file below matches, at the same relative paths.
3. **Likely duplicate photos**: Google Photos items matched to files elsewhere by name and capture time, labelled as likely.
4. **MD5 in `driveagent`**, so agent drives can be matched exactly with Drive and Cloud Storage.
5. **An index** of duplicates per user, rebuilt right after each scan ends and by a periodic job.
6. **A Duplicates tab** in the UI, and a link to it from a scan's page.

Out:
- **Acting on duplicates.** Bhandaar's Google access is read-only, so the page reports and never deletes. Removing a copy is done in Drive, the Cloud console, or on the disk.
- **Near-duplicate folders** (most but not all files shared), and **similar images** (resized, re-encoded).
- **Removing an agent drive's extra uploads.** When one physical drive is uploaded from several machines, deleting all but one upload from `agentserver` is a separate, planned feature. Until then, those copies show as "Same physical drive".
- **Gmail** (attachments aren't scanned) and **local scans** (`scandata` from `be`'s own filesystem scans, which have no living record).
- **Duplicates while a scan runs.** They appear when it ends; see [When the index is built](#when-the-index-is-built).

## Matching

### What each source can be matched by

| Source | Exact key | Notes |
|---|---|---|
| Google Drive (`drive_items`) | MD5 and size | Google Docs, Sheets and Slides have no MD5, so they're never matched |
| Cloud Storage (`gcs_objects`) | MD5 and size | Composite and CMEK-encrypted objects have no MD5 |
| Agent drives (`agent_files`) | MD5 and size, once recorded; until then BLAKE3 | BLAKE3 matches agent drives only with each other |
| Google Photos (`photos_picked_items`) | none | Google serves a re-encoded copy, so no hash matches the original. See [Photos](#photos-likely-duplicates) |

An exact key is `md5:<hex>:<size>`, or `blake3:<hex>:<size>` for an agent file whose MD5 isn't recorded yet. Including the size guards against a malformed hash.

### What counts

- **Live copies only:** Drive items not trashed, Cloud Storage objects whose `state` is live (not noncurrent or soft-deleted), and agent files whose `status` is `hashed`.
- **Left out:**
  - empty files, which all share one hash;
  - system files, by name: `.DS_Store`, `Thumbs.db`, `desktop.ini`, `Icon\r`, and macOS resource forks (`._*`).
- **Small files** are included. The page has a minimum-size filter (default: none).
- **Copies of one physical drive** (seagate1 uploaded from three machines, linked by `agent_drives.physical_drive_id`) are included, as chosen, but labelled. A group whose copies are all the same relative path on copies of one physical drive is shown as "Same physical drive". It counts zero reclaimable bytes, since deleting one copy deletes them all. The page has a filter to hide such groups, off by default.

### Groups and reclaimable space

- A **group** is every live copy with the same key, across all of the user's sources.
- A group's **reclaimable space** is its size × (copies − 1), counting each physical drive once.
- The page sorts groups by reclaimable space, largest first.

### Folders

A folder's **signature** identifies everything below it: the same signature means the same files at the same relative paths, at any depth, after the left-out files above. It's computed bottom up, as a Merkle tree: a SHA-256 over the folder's direct children, sorted by name, each as `<name>\0<f|d>\0<key or the subfolder's signature>\n`. Each file is read once, not once per folder above it (see [step 0](#implementation-order)).
- Two folders are **duplicates** when their signatures match. Their names may differ; their contents, paths below them and file contents may not.
- A folder with any file that has no key isn't matched, and its page entry says why. Examples: a Google Doc, a composite Cloud Storage object, an agent file not hashed yet.
- **Only the topmost match is reported:** when folders A and B match, their subfolders match too, so pairs inside a reported pair aren't listed again.
- **Empty folders**, and folders holding only left-out files, aren't matched.
- A folder's tree comes from:
  - Drive: `parent_id`
  - Cloud Storage: object names, split at `/`, as in Browse
  - agent drives: `relative_path`
- **Across sources,** folders match only when their files' keys are the same kind. MD5 everywhere does this once agent drives have MD5.

### Photos (likely duplicates)

A picked Google Photos item is a **likely duplicate** of a file elsewhere when:
- the names are equal, case-insensitively, and
- the times agree within one minute. The photo's time is its capture time (`create_time`). The file's time is:
  - for Drive images, the capture time Drive reads from the file's metadata (see below)
  - otherwise, its modified time

Matches on name alone aren't reported: `IMG_0001.JPG` is far too common. Likely matches are listed separately and never count toward reclaimable space, since they aren't proven identical.

For a better time on Drive images, the Drive scan starts requesting `imageMediaMetadata(time, width, height)`. That's part of the file's metadata, so `drive.metadata.readonly` allows it. The scan stores it in `drive_items` as new columns: `capture_time`, `width` and `height`. A photo whose width and height are known on both sides must also match on them.

Only picked items take part: Bhandaar never sees photos you didn't pick (see the privacy policy).

## MD5 in driveagent

So that agent drives match exactly with Drive and Cloud Storage, `driveagent` records MD5 next to BLAKE3. This is driveagent **0.6.0**.

- **Hashing:** `hashFile` feeds each file's bytes to both hashes in one read (`io.MultiWriter`), so it costs CPU but no extra I/O. MD5 is cheap next to the read.
- **State:** `state.db`'s `files` table gains `md5 TEXT`, in a migration of the local schema.
- **Backfill, automatic:** a file is skipped only if its size and mtime are unchanged *and* it has an MD5. So every drive's next scan re-reads every file once, which takes as long as a first full scan: hours for seagate1. Later scans skip unchanged files as now. Interrupting and resuming works as usual: files already given an MD5 aren't read again.
- **Upload:**
  - `wire.Change` gains `md5` for `file` upserts.
  - `agentserver` gains a migration that adds `agent_files.md5 TEXT` and an index on `(drive_pk, md5)`, and stores the field.
  - An older agent simply doesn't send it.
  - The next free migration number is used; `remote-sync-cross-os-linking.md` also proposes one.
- **Release:** `agent/client/internal/version.Version` goes to 0.6.0, and then `AGENTSERVER_LATEST_AGENT_VERSION` in the prod `.env`. The handshake's minimum version doesn't change, so 0.5.0 agents keep working, without MD5.

The remote-sync references (`remote-sync-agent.md`, `remote-sync-server.md`) and `drive-comparison-agent.md` are updated with the new column and the rehash-on-missing-MD5 rule.

As built (step 1):
- **Comparison results kept:** a file re-read only for its MD5 (same size and BLAKE3 hash) keeps its `compare` result. So the first 0.6.0 scan doesn't wipe the results `compare` computed.
- **Server checks:** agentserver rejects an `md5` that isn't 32 lowercase hex digits, and one sent for an unreadable file.
- **Upgrade, checked live (2026-10-03):** a test drive was scanned by 0.5.0 (no MD5 on the server), then by 0.6.0 with the same state dir. The 0.6.0 scan re-read all 3 files and uploaded MD5s equal to `md5sum`'s, and the next scan skipped them.
- **No downgrade:** 0.5.0 then refuses that state dir (schema version 2), as with any newer schema.

## The index

The duplicates of each user are worked out ahead of time, so the page reads them instead of grouping 1.6 million rows on every request. All tables are owned by `be`; agent tables are read, never written.

| Table | Rows |
|---|---|
| `dup_groups` | one per group of a user: `user_id`, `id`, `kind` (`file`, `folder`, `photo`), `key` (the file key or folder signature), `size` (one copy), `copies`, `reclaimable`, `same_physical` (all copies on one physical drive), `sources` (the distinct sources, for filtering), `name` (a representative name) |
| `dup_members` | one per copy: `group_id`, `source_kind` (`drive`, `gcs`, `agent`, `photos`), `source_key` (client key, `client_key/bucket`, or the agent drive's id), `item` (Drive file ID, object name, relative path or media item ID), `path` (for display), `size`, `modified`, `physical_drive` |
| `dup_state` | per user: `fingerprint` (of the inputs, below), `built_at`, `building`, `took_ms` |

Only groups with two or more copies are stored. A rebuild replaces a user's rows in one transaction, so the page always shows a complete index: the old one until the new one commits.

### When the index is built

- **Right after a scan ends:** when a Drive, Cloud Storage or Photos scan finishes, after its living record and totals are updated, its saver marks the owner's index stale and wakes the builder. The rebuild usually lands within a minute. A failed scan marks it stale too, since its upserts stay.
- **Periodically:** a goroutine in `be`, started by `main` like the folder totals checker, runs every 10 minutes. It rebuilds the index of any user whose fingerprint changed. The fingerprint combines:
  - every agent drive's version (the same one the totals cache uses, so agent uploads and syncs are caught)
  - each Drive account's `updated_at`
  - each bucket's `updated_at`
  - the newest Photos scan
  - the index's own code version, so a change to the rules rebuilds everyone
- **At startup,** the first check runs at once.
- **One build at a time,** one user at a time. While one runs, the page shows the last index with an "Updating…" badge.
- **First version: a full rebuild per user**, if [step 0](#implementation-order) shows it's fast enough (expected: tens of seconds for 1.6 million files). An incremental build, regrouping only the keys a scan touched, is the fallback.

### Building it

1. **Files:**
   - Gather the exact keys of every live, counted file of the user, from the three sources' tables.
   - Group by key, keep groups of two or more copies, and write them with their members.
   - On the prod copy, seagate1 alone had 129,856 groups of identical files, found in 0.76 s with `agent_files`' hash index ([browse.md](../archive/browse.md#scope)).
2. **Folders:**
   - Walk each source's tree bottom up in `be`, computing signatures in one streaming pass per source, ordered by path.
   - Group the signatures, and drop pairs nested inside a reported pair.
3. **Photos:** join the picked items with Drive images and with other files by lowercased name, then filter on time and dimensions.

## API

All need the session cookie, and see only the user's own sources.

- `GET /api/duplicates/summary`: reclaimable bytes, group counts by kind, reclaimable per source, `built_at`, and `updating`.
- `GET /api/duplicates/groups?kind=file|folder|photo&source=&across=1&min_size=&hide_same_physical=1&page=`: a page (50) of groups, largest reclaimable first. Each group carries up to 10 members, plus the count of the rest.
  - `source` limits the page to groups with a copy in that source (`google:<client_key>:drive`, `google:<client_key>:gcs:<bucket>`, `agent:<id>`).
  - `across` keeps only groups that span two or more sources.
- `GET /api/duplicates/groups/{id}/members?page=`: all copies of one group.

## Duplicates page

A fourth nav tab, **Duplicates**, at `/duplicates`. Search params: `kind`, `source`, `across`, `min_size`, `hide_same_physical`, `page`.

```
 Duplicates
 ┌────────────────────────────────────────────────────────────────┐
 │ 182 GB reclaimable · 131,204 groups · Updated 4m ago            │
 │ seagate1 96 GB · seagate2 61 GB · Google Drive 18 GB · …        │
 └────────────────────────────────────────────────────────────────┘
 [ Files ] [ Folders ] [ Photos (likely) ]
 Source [ All ▾ ]  [ ] Across sources only  Min size [ Any ▾ ]  [ ] Hide same physical drive
                                              ‹ Previous · Page 1 of 2,625 · Next ›
 ▸ 🎞 hd_dts_hd_master_audio.m2ts   827 MB × 3   1.6 GB reclaimable   seagate1 · seagate2 · Drive
     seagate2 (optiplex7070)   Jyo/media/hd_dts_hd_master_audio.m2ts   Browse ↗
     …
```

- **Summary card:** the reclaimable total, large, then the group count and when the index was built, plus an "Updating…" badge while it rebuilds. Below that, reclaimable space per source, each linking to the page filtered by that source.
- **Kind tabs:** Files, Folders, and Photos (likely), each with its count.
- **Filters:**
  - source (grouped like Browse's source picker)
  - across sources only
  - minimum size: any, 1 MB, 10 MB, 100 MB, 1 GB
  - hide same physical drive (off by default)
- **Groups:** one row per group: icon (the file-type icons of Browse), name, size × copies, reclaimable, and the sources as badges. Expanding a row lists its copies.
  - Each copy shows its source, path and modified time, and links to where it lives:
    - Drive: the Drive link
    - Cloud Storage: the object in the Cloud console
    - all sources: Browse, at its folder
  - A group with more than 10 copies loads the rest on demand.
  - Same-physical-drive groups carry a "Same physical drive" badge. Likely photo groups carry "Likely", with what matched (name, capture time, dimensions).
- **Folders:** like files, with each folder's file count and total size. Folders not matched because of an unhashed file are listed under a disclosure at the bottom, "N folders couldn't be compared", with the reason.
- **Paging:** the pager above the list, as on the other pages.
- **Phones:** groups stack as cards, like the other tables.
- **Elsewhere:** a scan's results page links to Duplicates filtered by its source.

## Implementation order

One PR each, checked on dev.sm against the prod copy:

0. **Measure** on the prod copy. Done 2026-10-03, on a restored copy of the prod database (dump of 2026-10-03):
   - **Hash formats:** all 60,186 Drive MD5s and all 332,916 Cloud Storage MD5s are 32-character lowercase hex, so they compare directly. All 1,462,042 hashed agent files are BLAKE3.
   - **Files:** one grouping query over every counted live file of all sources (about 1.86 million) took **3.9 s**. It found **429,549 groups** with **1,466,503 copies**, and 1.8 TB reclaimable. That total is inflated by seagate1 being uploaded from three machines (shown, labelled "Same physical drive").
   - **Index size:** about 1.5 million `dup_members` rows. A full rebuild writes them all, so the builder writes in batches, and the step 2 PR measures its time.
   - **Folders:** computing signatures in SQL, by aggregating every file into every folder above it, took **134 s** for the largest drive alone (98,452 folders, 15,046 groups of identical folders). So signatures are computed bottom up in `be` as a Merkle tree, as above, in one pass over each source's files sorted by path.
1. **driveagent 0.6.0** and **agentserver**:
   - MD5 alongside BLAKE3, and the state.db column
   - rehash when MD5 is missing
   - the wire field, and the server migration and index
   - tests: both hashes from one read, the rehash rule, upload and storage, an old agent without the field
2. **Files:**
   - the index tables and the builder (exact keys, groups, members)
   - the stale-after-scan hook and the periodic checker
   - the summary, groups and members API
   - the Duplicates tab with Files
   - tests against fixtures of all three sources, including the left-out files, same physical drive, live-only, and a rebuild when a fingerprint changes
3. **Folders:** signatures, the topmost-only rule, the uncomparable list, and the Folders tab.
4. **Photos:**
   - Drive's `imageMediaMetadata` and the new `drive_items` columns
   - the likely matching
   - the Photos tab

## Decisions

Made 2026-10-02:
- **Hashes:** MD5 added to `driveagent`; matches across sources are exact only (no name-and-size guesses for files).
- **Backfill:** automatic, on each drive's next scan.
- **Photos:** matched by metadata, as likely duplicates.
- **Folders:** exact duplicates only.
- **Live scans:** duplicates are found right after a scan ends, plus a periodic job.
- **Left out by default:** empty files and system files. Small files and copies of one physical drive are included.
- **Copies of one physical drive:** shown by default, labelled "Same physical drive", with zero reclaimable space; the hide filter starts off. Deleting the extra uploads, keeping one per physical drive, is a planned separate feature.
- **Deleted copies:** live copies only.
- **UI:** a Duplicates tab.
