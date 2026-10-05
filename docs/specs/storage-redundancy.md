# Storage Redundancy: Two Drives and One Cloud Copy

**Status:** proposed, 2026-10-05. Builds on [duplicates.md](../archive/duplicates.md) (content keys, the index and its builder), [browse.md](../archive/browse.md) and [photos-picker.md](../archive/photos-picker.md).

## Problem

The owner wants every file kept in exactly two places:

1. **Offline:** identical copies on **seagate1** and **seagate2**. Two physical drives, so one can fail.
2. **Online:** one copy in **one** of Google Drive, Google Cloud Storage or Google Photos. For Photos, the copy should be at the original resolution. A setting lets the owner accept a lower-resolution copy for chosen files instead (see [Accepting lower-resolution Photos copies](#accepting-lower-resolution-photos-copies)).

   **The cloud is one pool.** Every linked Google account belongs to the owner. A copy in any account's Drive, buckets or Photos is enough. Two copies anywhere in the pool are duplicates: a file in account 1's Drive and again in account 2's Drive is one too many, as is a file in Drive and in a bucket.

Bhandaar already records every source and finds identical files ([Duplicates](../archive/duplicates.md)). But it doesn't say how far the data is from this goal, or what to do about it. The data is a long way off (see [Step 0 findings](#step-0-findings)):
- 284k files (1.04 TB) on the seagates have no cloud copy;
- 82k files (47 GB) sit at different paths on the two drives;
- 615k extra copies (476 GB) exist within the drives.

A **Redundancy** page should list what breaks the goal, let the owner decide what to do about each item, and then turn those decisions into an ordered plan: shell commands and `rclone` or `gcloud storage` commands, plus either a checklist of steps to do in the Google Drive and Google Photos web apps or `rclone` commands for them, as chosen in Settings. The owner runs the plan; Bhandaar never changes anything itself. Once the next scans show the change, the items are marked resolved.

## Scope

In:
1. **Roles** for each source: the mirror pair, feeder drives, and cloud destinations.
2. **Issues**, found by an index rebuilt like Duplicates':
   - mirror gaps: missing, misplaced, conflicting or unreadable on one drive;
   - no cloud copy, or only a lower-resolution copy in Photos;
   - extra copies;
   - files only on a feeder drive;
   - files only in the cloud.
3. **Decisions** on files, folders and rules, including **Ignore**.
4. **Plans:** ordered commands for one machine, with Drive and Photos steps as instructions or `rclone` commands, and Cloud Storage steps as `rclone` or `gcloud storage` commands, as chosen in Settings.
5. **Verification:** an item is resolved when later scans no longer show the issue.
6. **driveagent 0.8.0:** capture time and dimensions of images and videos, so files can be matched with Google Photos.

Out:
- **Running anything.** Bhandaar's Google access stays read-only, and it never writes to a drive. The owner runs every command.
- **Gmail.** Attachments aren't scanned.
- **Photos the owner hasn't picked.** The Picker API only shows picked items, so only those count as cloud copies (see [Google Photos](#google-photos-as-a-cloud-copy)).
- **Near duplicates** (resized or re-encoded files other than the Photos match), as in Duplicates.
- **Running a plan's steps across machines** (for example, copying from the Mac onto a seagate attached to optiplex7070). See [Open questions](#open-questions).

## Step 0 findings

Measured on 2026-10-05 against the dev database: the prod copy in the dev Compose stack, with agent data synced up to 2026-10-04/05. The queries were read-only.

| Source | Files | Bytes | Notes |
|---|---|---|---|
| seagate1 (drive 10, optiplex7070, `/media/jyothri/Seagate1`, backup root `Jyo`) | 888,902 | 1,580 GB | 291 unreadable; MD5 on all but those |
| seagate2 (drive 11, optiplex7070, `/mnt/seagate2`, backup root `Jyo/Backup`) | 573,241 | 1,562 GB | all hashed |
| vmware-mbp (drive 12, Mac, `/Users/jyothri`) | 479,830 | 756 GB | not linked to a physical drive |
| Drive, `jyo****ga` | 78,196 | 67 GB | all with MD5 |
| Drive, `jyo****ri` | 4,299 | 5 GB | |
| GCS `jyo-archive` (ARCHIVE) / `jyo-pics` (STANDARD) | 328,200 / 4,716 | 214 / 91 GB | live objects; `jyo-coldline` is empty |
| Google Photos, picked | 1,989 | | sized by `HEAD`, no MD5 |

All files on both seagates are under their backup roots.

**The mirror**, comparing paths relative to each drive's backup root:

| Path on the two drives | Files | Bytes |
|---|---|---|
| Same content | 532,454 | 1,514 GB |
| Different content, same size and mtime (likely corrupted) | 5 | 2.6 GB |
| Different content | 9 | 58 kB |
| Only on seagate1 | 356,434 | 63 GB |
| … of which the content is on seagate2 at another path | 82,731 | 47 GB |
| Only on seagate2 | 40,773 | 46 GB |
| … of which the content is on seagate1 at another path | 40,706 | 45 GB |

So almost all of what is "missing" from one drive is really **misplaced**. About 274k files (16 GB) exist only on seagate1.

**Cloud coverage** of the seagates, by distinct MD5 and size:
- 200,513 distinct files (298 GB) have a copy in Drive or GCS.
- **283,760 (1,042 GB) don't.**

The largest folders without a cloud copy:

| Folder | Bytes |
|---|---|
| `media` | 309 GB |
| `public videos` | 228 GB |
| `from mac - to be sorted` | 168 GB |
| `from Win7` | 152 GB |
| `Suneetha` | 75 GB |
| `work places` | 47 GB |
| `public audio` | 42 GB |

By type: NEF 315 GB, MP4 123 GB, MKV 118 GB, JPG 95 GB.

**Extra copies:**
- **Within one drive:** seagate1 has 397,266 extra copies (242 GB) and seagate2 has 218,021 (234 GB). This is the largest saving.
- **In the cloud:** 36,229 files are stored twice across Drive and GCS (24 GB wasted), and about 19k more are stored 3–8 times (about 3 GB).

**Only in the cloud:** 53,207 files (46 GB) in `jyo****ga`'s Drive and 3,452 (5 GB) in `jyo****ri`'s Drive aren't on either seagate. GCS has 20 such objects (130 kB).

**The Mac:**
- 438,092 files (388 GB) aren't on the seagates: `Pictures` 178 GB, `Library` 98 GB, `Desktop` 57 GB, `Movies` 31 GB, `Documents` 9 GB, and tool caches (`.sdkman`, `.gradle`).
- 36,772 files (368 GB) are already on the seagates.

**Photos:** 1,741 of the 1,989 picked items have a file of the same name on a seagate.

**Timing:** the mirror comparison took 44 s, and a cloud-coverage pass a few seconds. A naive `LIKE` name join for Photos took minutes, so Photos matching needs an indexed lowercased name, as Duplicates already builds.

## Roles

Set once on the Redundancy page's **Setup** panel and stored per user (`red_config`).

| Role | What | Today |
|---|---|---|
| **Mirror A** and **Mirror B** | Two physical drives (`agent_physical_drives`), each with a root: the backup root of its uploads, which may be overridden | seagate1 (`Jyo`), seagate2 (`Jyo/Backup`) |
| **Feeder** | Any other agent drive. Files only there should be copied into the mirror; its copies of mirrored content can be deleted | vmware-mbp |
| **Out of scope** | Agent drives left out entirely | — |
| **Cloud destinations** | A list, each one: a Google account, a kind (`drive`, `gcs`, `photos`), and a place: a Drive folder path, a bucket and prefix, or nothing for Photos. GCS also takes an optional storage class. The `rclone` remote names are per account, in [Settings](#settings-how-cloud-steps-are-written). One destination is the default | for example, GCS `jyo-archive/seagate/`, ARCHIVE |

**The cloud pool:** every linked account's Drive, GCS buckets and picked Photos count as cloud copies, whether or not they are destinations. Accounts aren't told apart: what matters is whether the pool has the content, and how many times. Destinations are only where new backups go.

**Mirror paths:** a file's *mirror path* is its path relative to its mirror drive's root. seagate1's `Jyo/media/x.jpg` and seagate2's `Jyo/Backup/media/x.jpg` are both `media/x.jpg`. Everything below is keyed on mirror paths. Files on a mirror drive outside its root are treated as if the drive were a feeder.

A physical drive is uploaded by one or more agent drives (one per machine). The analysis uses the copy synced most recently. Earlier copies are only needed to locate the drive on other machines (see [Plans](#plans)).

## Issues

An **issue** is one way the data falls short of the goal. Each issue has a kind, a place (a mirror path, or a source and path), a size, and a suggested action.

### Mirror

Per mirror path, comparing A and B by content hash (BLAKE3, which both drives have):

| Kind | When | Suggested action |
|---|---|---|
| `missing_on_b` / `missing_on_a` | The path exists on one drive only, and its content isn't on the other at any path | Copy it across |
| `misplaced` | The path exists on one drive only, and its content is on the other at a path that isn't on the first. Reported once, as a pair (A's path, B's path) | Line up the layout: move the file on one drive to match the other |
| `conflict` | Both drives have the path, with different content | Keep one drive's copy and overwrite the other's |
| `corrupt_suspect` | A conflict where both copies have the same size and mtime. A third copy (cloud MD5 or feeder hash) that matches one side marks the other as bad | Keep the matching side, else ask |
| `unreadable` | The file's status is `error` on one drive | Copy over it from the other drive, if that one is readable |

When content moved on one drive and was also edited, it shows as one `missing_on_*` and one `missing_on_*` the other way, not as `misplaced`. Exact content keys only, as in Duplicates.

### Cloud

Per distinct content (MD5 and size) in the mirror, after the mirror's own issues:

| Kind | When | Suggested action |
|---|---|---|
| `no_cloud_copy` | No live Drive file and no live GCS object has the key, and no picked Photos item is an [original-resolution match](#google-photos-as-a-cloud-copy) | Back up to the default destination (or as a rule says) |
| `photos_lower_res` | The only cloud copy is a Photos item that matches by name and capture time, but at lower resolution or quality | Back up to a destination. With [Accept lower-resolution Photos copies](#accepting-lower-resolution-photos-copies) on, the owner may instead mark it backed up |

A file on only one mirror drive gets the cloud check too, so a copy left only on seagate1 is still backed up.

### Extra copies

The goal allows each content to be stored once per mirror drive and once in the cloud. Anything beyond that is an extra copy:

| Kind | When | Suggested action |
|---|---|---|
| `dup_in_mirror` | One content at two or more mirror paths. Since both drives should match, keeping a path keeps it on both | Pick the paths to keep; the rest are deleted from both drives |
| `dup_in_cloud` | One content in two or more places anywhere in the cloud pool: Drive files, GCS objects or Photos matches, in one account or across accounts (account 1's Drive and account 2's Drive count) | Pick the one to keep |
| `on_feeder` | A feeder's file whose content is on both mirror drives | Delete it from the feeder |

`dup_in_mirror` is also offered for whole folders: a group of identical folders from the Duplicates index (Merkle signatures), restricted to the mirror, is one issue. Keeping one folder deletes the others.

Copies of one physical drive uploaded from several machines aren't extra copies, as in Duplicates.

### Outside the mirror

| Kind | When | Suggested action |
|---|---|---|
| `feeder_only` | A feeder's file whose content isn't on either mirror drive | Copy it into the mirror at a chosen folder, then optionally delete it from the feeder |
| `cloud_only` | A cloud copy whose content isn't on either mirror drive | Download it into the mirror at a chosen folder |

Left out, as in Duplicates: empty files, system files (`.DS_Store`, `Thumbs.db`, `desktop.ini`, `Icon\r`, `._*`), Google Docs/Sheets/Slides (no MD5), and noncurrent or soft-deleted GCS versions.

## Google Photos as a cloud copy

Photos serves a copy without the original's bytes, so no hash matches. A picked item is a **match** for a mirror file when:
- the names are equal, ignoring case;
- the capture times agree within one minute, allowing the whole-quarter-hour zone offsets the Duplicates rule allows (EXIF times have no zone);
- and, with driveagent 0.8.0, the dimensions are known on both sides.

A match is **original resolution** when:
- its width and height equal the file's (either way round), *and*
- its served size (from `HEAD`) is at least 90% of the file's size. For an original-quality image, the served size is the original minus its location EXIF.

Otherwise it's **lower resolution**. Storage saver downscales images above 16 MP and re-encodes videos to 1080p, so the dimensions catch those. For images at or below 16 MP, only the size test can tell Storage saver from Original. The 90% threshold is checked in [step 0 of the build](#implementation-order).

### Accepting lower-resolution Photos copies

A setting, **Accept lower-resolution Google Photos copies** (`photos_accept_lower_res`, off by default; see [Settings](#settings-how-cloud-steps-are-written)), lets the owner count a Storage-saver copy as the backup for some files, such as phone snapshots.

- **Off:** a lower-resolution match never counts. The only actions on `photos_lower_res` are **Back up to** and Ignore.
- **On:** `photos_lower_res` gains the action **Mark as backed up (Photos copy)**.
  - It works like any other decision: on a file, a folder (covering files added later) or a rule, such as `**/phone/**`.
  - The issue is **Resolved** at once, with no plan step, as with Ignore. Unlike Ignore, it's counted as backed up: the summary's cloud progress includes it, and the resolved count says "marked backed up (Photos)".
  - The accepted Photos item joins the cloud pool as that file's copy. If the same file also has a Drive or GCS copy, the group shows up as `dup_in_cloud`, with the Photos copy labelled "Lower resolution, accepted". Keeping only the Photos copy asks the owner to confirm, since the full-resolution cloud copy would go.
  - It never applies to a file with no Photos match at all. That's still `no_cloud_copy`.
- **Turning it off later:** the Mark-as-backed-up decisions are kept but stop applying, and their files are `photos_lower_res` again, Open. Turning it back on restores them. Setup lists these decisions, as it does Ignore decisions, so they can be cleared.
- **If the Photos item goes:** when a later Photos scan no longer finds the matching item (it was deleted, or not picked again), the decision has nothing to accept, and the file becomes `no_cloud_copy`.

**Only picked items count.** After the owner uploads files to Photos as a plan says, they pick them in a new Photos scan so Bhandaar can see them. The plan's checklist says so.

### driveagent 0.8.0: media metadata

Agent files have only an mtime, which for copied photos is often the copy's time, not the capture time. So `driveagent` reads:
- `capture_time`: EXIF `DateTimeOriginal`, or for MP4/MOV, the `mvhd` creation time;
- `width` and `height`.

How:
- **Which files:** by extension: JPEG, HEIC/HEIF, PNG, TIFF, DNG, NEF, ARW, CR2, CR3, RAF, ORF, RW2; MP4, MOV, M4V, 3GP.
- **Reading:** headers only, at most the first 1 MB and, for MP4/MOV, the `moov` atom wherever it is. This is a separate read, after hashing, so it isn't a second full read. Pure Go, standard library plus a small EXIF parser, so cross-compiling stays as it is.
- **State:** `state.db`'s `files` table gains `capture_time`, `width`, `height` and `media_probed` (the version of the probe), in a migration of the local schema.
- **Backfill:** a media file with an older `media_probed` is probed on the next scan, without re-hashing. That's about 60k files on the seagates, at header cost.
- **Upload:** `wire.Change` gains the three fields. agentserver migration 5 adds them to `agent_files`. Older agents don't send them.
- **Release:** `version.Version` 0.8.0, then raise `AGENTSERVER_LATEST_AGENT_VERSION`, and agents update themselves ([agent-auto-update.md](agent-auto-update.md)).
- **Docs:** `drive-comparison-agent.md`, `remote-sync-agent.md` and `remote-sync-server.md` gain the columns.

Duplicates' Photos matching then uses agent files' capture times too.

## Decisions

A **decision** is an action the owner chose for some issues. It's stored, not computed, so it survives rebuilds.

### Where a decision applies

- **A file:** one mirror path, or one source and path.
- **A folder:** everything below it, now *and later*. A file that lands in the folder after the decision gets the same action in the next plan. This keeps "back up `nikon z6 videos` to GCS" true as the folder grows.
- **A rule:**
  - a glob on mirror paths or feeder paths (`**/*.nef`, `Library/**`, `**/.gradle/**`);
  - optionally limited to some issue kinds and to one source;
  - with an action and its options.

  Rules are ordered, and the first match wins.
- **A content group** (`dup_in_mirror`, `dup_in_cloud`): the paths or places to keep. It's keyed on the content key, or for folder groups on the signature, like Duplicates' groups.

**Precedence:** file > deeper folder > shallower folder > rule. So a decision on `media` can be overridden for `media/2014`, and that again for one file in it.

### Actions

| Issue kinds | Actions |
|---|---|
| `missing_on_*`, `unreadable` | **Copy across**; **Delete from the other drive** (it was meant to go); Ignore |
| `misplaced` | **Use A's layout** or **use B's layout** (move on the other drive); Ignore |
| `conflict`, `corrupt_suspect` | **Keep A's** or **keep B's** (overwrite the other); **Keep both** (rename B's to `name (seagate2).ext`, then copy each across); Ignore |
| `no_cloud_copy` | **Back up to** a destination; Ignore |
| `photos_lower_res` | **Back up to** a destination; **Mark as backed up (Photos copy)**, only with the setting on; Ignore |
| `dup_in_mirror` | **Keep** these paths (the rest are deleted from both drives); Ignore |
| `dup_in_cloud` | **Keep** this place (the rest are deleted); Ignore |
| `on_feeder` | **Delete from the feeder**; Ignore |
| `feeder_only` | **Copy into the mirror** at a folder (default: the source's own path under `from <drive>/`), then optionally delete from the feeder; Ignore |
| `cloud_only` | **Download into the mirror** at a folder; **Delete from the cloud**; Ignore |

**Guards when deciding:**
- A decision that would leave content with no copy on a mirror drive is refused.
- A decision that would leave content with no cloud copy is allowed, with a warning, and the content then shows as `no_cloud_copy`.
- Deleting GCS objects within their class's minimum storage duration (Nearline 30 days, Coldline 90, Archive 365) warns that early-deletion charges apply, with the date when they stop.

### States

Each issue is in one state:

| State | Meaning |
|---|---|
| **Open** | No decision applies |
| **Decided** | A decision applies, and no plan includes it yet |
| **Planned** | It's in a plan that hasn't been verified yet |
| **Resolved** | Either an Ignore or Mark-as-backed-up decision applies (at once, with no plan), or it was Decided or Planned and a rebuild no longer finds the issue |

Resolved issues, ignored ones included, leave the view. The summary keeps a count, such as "1,204 resolved in the last 30 days (310 ignored)", from `red_resolved` (purged after 90 days). An ignored item counts toward the goal's progress, as the owner has chosen to accept it.

**Undoing an Ignore:** ignored items aren't listed, but the Ignore *decisions* are: in Setup, under **Ignored**, each with what it covers (a file, a folder, or a rule) and its file count. Clearing one brings its items back as Open.

An issue that disappears without a decision (for example, the owner fixed it by hand) simply goes away.

A **file** decision on content that has since changed (a different hash at that path) is dropped, and the issue is Open again. Folder decisions and rules aren't dropped.

## The index

The same pattern as Duplicates: worked out ahead of time per user, replaced in one transaction, read by the page.

| Table | Rows |
|---|---|
| `red_config` | per user: mirror A and B (physical drive, root), feeder and out-of-scope agent drives, default destination |
| `red_destinations` | per user: `id`, `client_key`, `kind`, place, `storage_class`, `is_default` |
| `red_issues` | per user and issue: `kind`, `scope` (`mirror` or a source), `path` (or the pair for `misplaced`), `key` (content key or folder signature), `size`, `details` (JSONB: per-side hash, mtime, status, the third copy's verdict, Photos match), `state`, `decision_id` |
| `red_folders` | per user, mirror folder and source folder: counts and bytes per issue kind and state, for the tree, plus `uniform_kind` (see [The page](#the-page)) |
| `red_decisions` | `id`, user, `target` (`file`, `folder`, `group`, `rule`), `scope`, `path` or `pattern` or `key`, `kinds`, `action`, `params` (JSONB: destination, layout side, keep list, target folder), `hash_at_decision` (file decisions), `rule_order`, `created_at` |
| `red_plans` / `red_plan_steps` | see [Plans](#plans) |
| `red_state` | per user: `fingerprint`, `built_at`, `building`, `error`, as in `dup_state` |
| `red_resolved` | per user: resolved issues, by kind and day, with bytes |

**Rebuilds:** the builder shares Duplicates' triggers and fingerprint: after a scan or deletion ends, every 10 minutes, and once agent drives have been still for 5 minutes. It also runs when the config or a decision changes, since that changes the states. It runs after the duplicates index, and reads its folder groups.

**Cost:** the mirror join took 44 s on the dev box. That's acceptable next to Duplicates' 2.5 minutes, so the first version is a full rebuild. `red_issues` is expected to hold about 700k rows today, mostly `dup_in_mirror` members and `no_cloud_copy`. Both shrink as the owner works through them.

## The page

A nav tab, **Redundancy**, at `/redundancy`.

```
 Redundancy
 ┌─────────────────────────────────────────────────────────────────────────┐
 │ Goal: seagate1 = seagate2, and one cloud copy                           │
 │ Mirror ▓▓▓▓▓▓▓▓▓░ 94% matched  · Cloud ▓▓▓░░░░░░░ 22% backed up          │
 │ 476 GB of extra copies · 1,204 resolved in 30 days · Updated 3m ago     │
 └─────────────────────────────────────────────────────────────────────────┘
 [ Mirror 397k ] [ Cloud 284k ] [ Extra copies 652k ] [ Mac 438k ] [ Cloud only 57k ] [ Plans ]
 State [ Open ▾ ]  Kind [ All ▾ ]  Min size [ Any ▾ ]  Find [ path…        ]
 ▾ media                                   mixed · 309 GB
   ▸ media/nikon z6/2019            no_cloud_copy · 1,204 files · 48 GB   [ Back up to… ▾ ] [ Ignore ]
   ▸ media/nikon z6/2020            no_cloud_copy ·   980 files · 41 GB   [ Back up to… ▾ ] [ Ignore ]
   ▾ media/phone                             mixed · 12 GB
       IMG_2041.JPG                 photos_lower_res · 4.1 MB             [ Back up to… ▾ ] [ Ignore ]
       …
```

- **Summary card:**
  - progress toward each half of the goal, by bytes;
  - extra copies' bytes;
  - resolved in the last 30 days;
  - when the index was built (with "Updating…" while it rebuilds).
- **Tabs** by issue group, each with its count, and **Plans**. Ignoring a row removes it from view at once (with an **Undo** toast).
- **The tree never stops at a mixed folder.** A folder is shown collapsed, as one decidable row, only when it is **uniform**: every open issue below it is of one kind with one suggested action. That is the *topmost* uniform folder, worked out at build time (`red_folders.uniform_kind`). A mixed folder is always shown expanded, down to its uniform subfolders and loose files. So `media` (mixed) is never offered as a single row, while `media/nikon z6/2019` (all `no_cloud_copy`) is. Any uniform row can still be expanded to decide its subfolders or files differently.
- **Rows:**
  - path, kind, file count, bytes, and the decision controls;
  - a decided row shows its decision and where it came from ("from rule `**/*.nef`", "from folder `media`"), with **Change** and **Clear**;
  - mixed folders show a breakdown by kind.
- **Bulk:** checkboxes on rows, and "Apply to selected".
- **Groups** (`dup_in_mirror`, `dup_in_cloud`) expand to their copies, as on Duplicates, with a **Keep** radio or checkbox per copy. Groups of identical folders are listed above single files.
- **Conflicts** show both sides: hash, size and mtime, and the third copy's verdict ("GCS matches seagate1").
- **Rules:** in the Setup panel, an ordered list with add, edit, reorder and delete. Each rule shows how many issues it currently covers. On first setup, these rules are *suggested*, and only saved if the owner accepts them:
  - Ignore on feeders: `Library/**`, `.*/**`;
  - Ignore `**/$RECYCLE.BIN/**` and `**/System Volume Information/**`.
- **Search params:** `tab`, `state`, `kind`, `min_size`, `q`, `folder`, `page`.
- **Phones:** rows stack as cards; controls go in a menu.
- **Elsewhere:** Duplicates groups that involve the mirror link to this page.

## Plans

A **plan** turns decided issues into steps that one machine can run.

### Making a plan

1. The owner picks **a machine**: an agent (box) that has uploaded the drives involved. Each drive's local path is that agent's `drive_root` for it, joined with the file's path.
2. Optionally, **a selection**: tabs, folders or decisions. By default, everything Decided.
3. Bhandaar builds the steps from the current index. It shows a preview before saving: steps per phase, files, bytes, and what is left out and why.
4. **Left out of the plan, and listed:**
   - steps needing a drive this machine hasn't uploaded, such as Mac → seagate copies on optiplex7070 ("needs vmware-mbp on this machine");
   - issues whose data changed since the decision.

### Order

Phases run in this order, so nothing is deleted before its copies exist:

| Phase | Steps |
|---|---|
| 1. Repair the mirror | Copies across (`missing_on_*`, `unreadable`, conflicts: keep a side), and renames for "keep both" |
| 2. Align the layout | Moves for `misplaced` |
| 3. Fill the mirror | Feeder → mirror copies and cloud → mirror downloads, to **both** drives |
| 4. Back up | Uploads to destinations |
| 5. Remove extra copies | Deletes: mirror duplicates (on both drives), cloud duplicates, feeder copies, and drives' "delete from the other drive" |
| 6. Rescan | `driveagent scan` of each changed folder; and a reminder to run Drive, GCS and Photos scans from the Request page |

Within a phase, steps are sorted by path.

### Commands

The plan downloads as `bhandaar-plan-<id>.sh`. The script starts with a header comment: the plan's ID, the machine, when it was made, and the counts per phase. Each phase is a commented section.

- **Local copies:**
  - `mkdir -p "<dest dir>"`, then `cp -p "<src>" "<dest>"`; `cp -p` keeps mtimes, which the mirror's checks compare.
  - A whole uniform folder that's missing on the other drive is one step: `cp -a "<src dir>" "<dest parent>/"`.
- **Moves:** `mkdir -p` the target's folder, then `mv "<old>" "<new>"`. Empty folders left behind are removed at the end with `rmdir` (which only removes empty ones).
- **Deletes:** plain `rm "<path>"` per file, and `rm -r` for a decided folder group. As chosen, there's no check at run time: the plan's order and the guards when deciding are the safety.
- **Cloud Storage:** with the tool chosen in [Settings](#settings-how-cloud-steps-are-written):

  | Step | `rclone` (default) | `gcloud storage` |
  |---|---|---|
  | Upload | `rclone copyto '<local>' '<remote>:<bucket>/<prefix><mirror path>' --checksum [--gcs-storage-class ARCHIVE]` | `gcloud storage cp '<local>' 'gs://<bucket>/<prefix><mirror path>' [--storage-class=ARCHIVE]` |
  | Download | `rclone copyto '<remote>:<bucket>/<object>' '<local>'` | `gcloud storage cp 'gs://<bucket>/<object>' '<local>'` |
  | Delete | `rclone deletefile '<remote>:<bucket>/<object>'` | `gcloud storage rm 'gs://<bucket>/<object>'` |
  | A whole folder | `rclone copy '<dir>' '<remote>:<bucket>/<prefix><dir>' --checksum` | `gcloud storage cp -r '<dir>' 'gs://<bucket>/<prefix><parent>/'` |

  - `gcloud storage cp` checks the MD5 or CRC32C after each transfer by default.
  - With `gcloud`, the script starts with `gcloud config set project <the bucket's project>` before each bucket's steps. With `rclone`, the project is part of the remote.
  - Bhandaar never sees either tool's credentials.
- **Google Drive and Google Photos:** a checklist or `rclone` commands, as chosen in Settings (below).
- **Rescan:** `driveagent scan --drive-id <id> --path "<drive_root>/<folder>"` for the topmost changed folders, at most one per top-level folder of the mirror.
- **Quoting:** every path is single-quoted. A path that isn't valid UTF-8 (`agent_files.raw_path`) is written as `$'…'` with `\xHH` escapes.
- **Mac paths:** on a macOS agent, `cp -p` and `mkdir -p` work as on Linux. `cp -a` is BSD's, and does the same here.

### Checklist (Google Drive and Google Photos)

Bhandaar's Google access is read-only, and these steps are done in Google's web apps. The plan page lists them by phase, as plain-English sentences:

- **Deletions** give the file's name, where it is, and what to do. Examples:
  - "In Google Drive for jyo\*\*\*\*ga@gmail.com, delete `IMG_2041.JPG` (4.1 MB) in `Photos/2016/Goa`." The step links to the file.
  - "In Google Photos for jyo\*\*\*\*ri@gmail.com, delete `VID_4648.MOV`, taken 12 Mar 2019 at 18:04." The Picker gives no link, so the step names the file and its capture time.
- **Uploads and downloads** give the source and the destination. Examples:
  - "Upload `/media/jyothri/Seagate1/Jyo/media/phone/IMG_2041.JPG` to Google Drive for jyo\*\*\*\*ga@gmail.com, into the folder `Backup/media/phone`."
  - "Upload `/media/jyothri/Seagate1/Jyo/media/phone/IMG_2041.JPG` to Google Photos for jyo\*\*\*\*ri@gmail.com, at **Original quality**."
  - "Download `Scans/tax-2019.pdf` from Google Drive for jyo\*\*\*\*ga@gmail.com to `/media/jyothri/Seagate1/Jyo/from drive/Scans/` and `/mnt/seagate2/Jyo/Backup/from drive/Scans/`."
- **Grouping:** a whole uniform folder is one sentence ("Upload the folder … (1,204 files, 48 GB) to …"), with its files listed on expand.

Each step has a **Done** checkbox, stored in `red_plan_steps.done_at`. Uploads to Photos end with a step: "Pick the uploaded items in a new Photos scan (Request → Google Photos), so Bhandaar can see them."

### Settings: how cloud steps are written

The Settings page (`/settings`, from the user menu) gains a **Redundancy plans** section, next to "Show Google Cloud Storage":

```
 Redundancy plans
 Google Drive and Google Photos steps   (•) Instructions to follow in the web app
                                        ( ) rclone commands
 Google Cloud Storage commands          (•) rclone
                                        ( ) gcloud storage
 Google Photos                          [ ] Accept lower-resolution Google Photos copies
                                            Lets you mark a file as backed up when Google Photos
                                            only has a reduced-resolution copy of it
 rclone remotes                         jyo****ga@gmail.com   Drive [ gdrive-ga ]  Photos [            ]  Cloud Storage [ gcs-ga ]
 (shown when either uses rclone)        jyo****ri@gmail.com   Drive [ gdrive-ri ]  Photos [ gphotos-ri ]
```

- **Drive and Photos steps:**
  - `instructions` (default): the [checklist](#checklist-google-drive-and-google-photos).
  - `rclone`: the steps go in the script, in their phase, using the account's remote:
    - **Drive:**
      - upload: `rclone copyto '<local>' '<remote>:<folder>/<name>' --checksum`;
      - download: `rclone copyto '<remote>:<path>' '<local>'`;
      - delete: `rclone deletefile '<remote>:<path>'` (to Drive's trash, rclone's default).
      - Drive allows two files of one name in one folder, which a path can't tell apart. Such a file is addressed by ID instead: `rclone backend copyid` for downloads, and for deletes, `--drive-root-folder-id <parent id>` when its name is unique there. If neither works, the step stays an instruction.
    - **Photos:** uploads only: `rclone copy '<local>' '<remote>:upload'`. The Library API uploads at original quality. Since Google's 2025 API changes, rclone can't see or delete items it didn't upload, so Photos **deletions always stay instructions**. The plan page says so, and the "pick the uploaded items" step stays.
- **Cloud Storage commands:** `rclone` (default) or `gcloud` (`gcloud storage`), as in [Commands](#commands).
- **Accept lower-resolution Google Photos copies:** off by default. See [Accepting lower-resolution Photos copies](#accepting-lower-resolution-photos-copies). Changing it rebuilds the index, since it changes which decisions apply.
- **rclone remotes:**
  - one name per account and service, shown only when a setting uses `rclone`;
  - a step whose account has no remote for its service stays an instruction (Drive, Photos), or blocks the plan's preview with "name the rclone remote for …" (Cloud Storage);
  - the names are labels only: the owner sets up the remotes with `rclone config`, and Bhandaar never sees their tokens.
- **Storage:** `user_settings` gains:
  - `plan_drive_steps` (`instructions` | `rclone`, default `instructions`);
  - `plan_gcs_tool` (`rclone` | `gcloud`, default `rclone`);
  - `rclone_remotes` (JSONB: `{<client_key>: {drive, photos, gcs}}`);
  - `photos_accept_lower_res` (boolean, default false).

  This makes Settings more than display preferences, so `db/settings.go`'s comment and the CLAUDE.md line about it change too.
- **API:** `GET` and `PUT /api/settings` carry the new fields. `PUT` checks:
  - the enums;
  - that each remote name matches `^[A-Za-z0-9_. -]+$`;
  - that each remote's account is the user's.
- **Plans keep their tools:** a plan records the settings it was made with (`red_plans.tools`, JSONB), and its steps are written when it's made. Changing a setting affects later plans only. The plan page shows which tools a plan uses, and **Remake** builds a new plan with the current settings.

### Plan tables

- `red_plans`: `id`, user, agent (machine), `tools` (the settings it was made with), `created_at`, `state`:
  - `ready`;
  - `verified` when every step is;
  - `superseded` when the owner discards it.
- `red_plan_steps`: `plan_id`, `seq`, `phase`, `kind` (`shell`, `ui`), `text`, the issues it covers, `done_at` (UI steps), `verified_at`.

### Verification

After each rebuild, a plan's step is **verified** when the issues it covers are gone:
- a copy: the file is at the target with the source's hash;
- a delete: the file is gone;
- an upload: the key is in the destination (for Photos, a picked item matches at original resolution).

When all of a step's issues are resolved, the step is verified. The plan page shows verified, pending and done-but-not-seen counts. A UI step marked Done and not seen after the next scan of its source is flagged "Done, but not found by the last scan".

## API

All need the session cookie, and see only the user's own sources.

- `GET /api/redundancy/config`, `PUT /api/redundancy/config`: roles and destinations.
- `GET /api/redundancy/summary`: progress, counts by tab, kind and state, resolved counts, `built_at`, `updating`.
- `GET /api/redundancy/tree?tab=&folder=&state=&kind=&min_size=&q=&page=`: a page (200) of one folder's rows: uniform subfolders, mixed subfolders (with their breakdown), and files, each with its effective decision.
- `GET /api/redundancy/groups?kind=dup_in_mirror|dup_in_cloud|on_feeder&page=`: a page (50) of groups, largest first, with their copies.
- `POST /api/redundancy/decisions`: `{target, scope, path|pattern|key, kinds?, action, params}` → the decision and the count it covers. It answers `409` if a guard refuses it, with the reason.
- `DELETE /api/redundancy/decisions/{id}`: clears it.
- `GET /api/redundancy/rules`, `PUT /api/redundancy/rules`: the ordered rules.
- `POST /api/redundancy/plans/preview`: `{agent_id, selection}` → counts per phase and what's left out.
- `POST /api/redundancy/plans`: the same body → the saved plan.
- `GET /api/redundancy/plans`, `GET /api/redundancy/plans/{id}?phase=&page=`: plans and their steps.
- `GET /api/redundancy/plans/{id}/script`: the shell script (`text/x-shellscript`, as an attachment).
- `PUT /api/redundancy/plans/{id}/steps/{seq}`: `{done}` for a UI step.
- `DELETE /api/redundancy/plans/{id}`: marks it superseded; its issues return to Decided.

## Implementation order

Two PRs. The first is release-gated, like 0.6.0 was.

1. **driveagent 0.8.0 and agentserver migration 5:** media metadata (probe, state migration, backfill without rehash, wire fields, server columns). Tests:
   - probing fixtures of each format, including a truncated file and a `moov` at the end;
   - backfill skips hashing;
   - an old agent without the fields.
2. **Redundancy**, one commit per step, checked on dev.sm against the prod copy:
   1. **Measure:** the Photos 90% threshold against the owner's known Original and Storage-saver items. Also the full rebuild's time and `red_issues` row count.
   2. **Config and the index:**
      - the roles, the issue kinds and the topmost-uniform rollup;
      - the builder, sharing Duplicates' triggers;
      - the summary and tree API.
   3. **Decisions and rules:** precedence, the guards, states, dropping stale file decisions; the page with decision controls.
   4. **Plans:**
      - steps, phases and folder collapsing;
      - the script and its quoting;
      - the UI checklist;
      - the left-out list;
      - the Settings section (tools and `rclone` remotes) and `red_plans.tools`.
   5. **Verification and resolved counts.**

   Tests:
   - `be/db/redundancy_test.go`, against fixtures of all sources (needs `BE_TEST_DB`):
     - each issue kind;
     - the third-copy verdict;
     - the topmost-uniform rule;
     - precedence;
     - Ignore covering new files under an ignored folder;
     - a stale file decision dropped;
     - resolution after a fixture "rescan".
   - `be/web/redundancy_test.go`:
     - script generation, including phase order, `$'…'` quoting for non-UTF-8 paths, and folder collapsing;
     - each tool setting: `rclone` and `gcloud storage` for Cloud Storage; instructions and `rclone` for Drive (a same-named file by ID, else an instruction); Photos deletions always instructions; a missing remote;
     - a plan keeping its tools after the settings change;
     - Mark as backed up: refused with the setting off; resolved and counted as backed up with it on; inactive (Open again) after turning it off, and back on turning it on; `no_cloud_copy` once the Photos item is gone; the accepted copy in `dup_in_cloud`;
   - `be/web/settings_test.go` and `be/db/settings_test.go`: the new fields, their defaults, the checks on remote names and accounts;
     - steps left out for a machine without a drive;
     - the guards.
   - Route tests for the page in `ui/src/test/`.

## Decisions

Made 2026-10-05:
- **Mirror:** identical paths *and* content, relative to each drive's root. Content at different paths is `misplaced`, and the owner picks the layout that wins.
- **Photos:** matched by name, capture time and dimensions, using driveagent 0.8.0's media metadata, plus served size for quality. Lower resolution doesn't count as a cloud copy, unless the owner turns on the setting to accept lower-resolution copies and marks the file.
- **The Mac (vmware-mbp):** a feeder. Its files missing from the mirror are flagged, and its copies of mirrored content can be deleted.
- **Granularity:** folders plus rules, with overrides down to files. A mixed folder is never offered as one row; the page shows the topmost *uniform* folders.
- **Tools, in Settings:**
  - Drive and Photos steps are instructions (default) or `rclone` commands; Photos deletions are always instructions.
  - Cloud Storage commands use `rclone` (default) or `gcloud storage`.
  - A plan keeps the tools it was made with.
- **Lower-resolution Photos copies:** a setting, off by default. With it on, the owner may mark a file whose only cloud copy is a lower-resolution Photos item as backed up: Resolved, and counted as a cloud copy.
- **Machine:** picked per plan; paths come from that machine's `drive_root`s.
- **Deletes:** plain `rm` (and `rclone deletefile`), always in the last phase before rescans.
- **Ignore:** an ignored item is marked Resolved at once and removed from view. A folder or rule ignore covers files that land there later. The Ignore decisions are listed in Setup, where they can be cleared.
- **Cloud pool:** all linked accounts are one pool. A copy in any of them is enough, and two copies anywhere in the pool, including across accounts, are duplicates.
- **Drive and Photos steps:** plain-English checklist sentences: name, place and operation for deletions; source and destination for uploads and downloads.

## Open questions

1. **Copies between machines:** a Mac → seagate copy needs both on one machine. Plans could emit `rsync -a mac:'<path>' '<dest>'` over SSH from optiplex7070 instead of leaving these steps out.
2. **Which layout should win** for `misplaced`? The 82k pairs could be pre-suggested, for example the side whose folder has more of its siblings in place. The first version leaves the choice to the owner, per folder.
3. **`dup_in_mirror` default:** which copy should be pre-selected to keep? A suggestion is the copy outside folders named like `from …` or `… to be sorted`, else the shallowest path. The first version doesn't pre-select; the owner picks.
4. **Cloud-only Drive files (51 GB):** should `cloud_only` default to *download into the mirror*, at `from drive/<account>/<path>`, or to no suggestion?
