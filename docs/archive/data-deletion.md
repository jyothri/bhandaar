# Deleting Data: Drives, Services, and Google Accounts

**Status:** implemented 2026-10-02 (see [As built](#as-built)). Written the same day.

## Problem

Bhandaar keeps adding data and offers no way to remove it, other than deleting one scan at a time from Request History. Three removals are missing:
1. **A drive uploaded from one box**, e.g. seagate1 as uploaded from `JyothriingasMBP.attlocal.net`. One physical drive is often uploaded from several machines, and only one copy is wanted.
2. **One service's data of an account** (Gmail, Google Drive, Cloud Storage or Google Photos), while keeping the account and its other services.
3. **A whole Google account**: disconnect it, and delete everything recorded for it (Gmail, Google Drive, Cloud Storage, Google Photos).

The privacy policy promises deletion on request by email (section 8). These let users do it themselves.

## Scope

In:
- A **Manage data** page, a nav tab, listing the user's linked Google accounts and uploaded drives, with the three deletions.
- **Confirmation before every deletion:** a dialog saying exactly what goes. Disconnecting an account also requires typing its name, like deleting a GitHub repository.
- Deletions run as **background jobs**, with progress on the page.
- **Revoking Bhandaar's access at Google** when an account is disconnected.
- The **privacy policy** updated to describe self-service deletion.

Out:
- **Undo.** Deletions are immediate and final, and the dialogs say so.
- **Deleting anything at Google or on a disk.** Bhandaar removes only its own records. Its Google access is read-only, and agents' local `state.db` files are left alone.
- **Stopping a box from re-uploading a deleted drive.** As decided, if that box scans or syncs the drive again, it's uploaded again in full. The dialog says so.
- **Deleting a Bhandaar user.** That stays `agentserver user disable`, plus an email request.

## What each deletion removes

### A drive from one box

The `agent_drives` row of that box's agent (`agent_agents.hostname`) for that `drive_id`. Its files, directory listings, scan runs, tombstones and acked ranges go with it, through `ON DELETE CASCADE` (`agentserver` migration 0003). Also removed:
- Browse's cached folder totals for `agent:<id>` (`browse_folder_totals`, `browse_totals_state`)
- once [duplicates.md](duplicates.md) is built, the user's duplicates index is marked stale

Kept:
- **The agent** (`agent_agents`), its login, and its other drives.
- **The drive's `agent_physical_drives` row**, which links copies of one physical drive. It's kept even if no drive points at it any more, so a later upload links to the same physical drive again.
- **The same physical drive as uploaded from other boxes:** those are separate `agent_drives` rows.

`be` has only read `agentserver`'s tables so far. This is the one place it writes them: one `DELETE` of one `agent_drives` row. The data the cascade removes is what `agentserver` itself clears when a drive's stream restarts ([remote-sync-server.md](../specs/remote-sync-server.md), `PUT /agent/v1/drives`). Here the row goes too, so the drive leaves Browse.

The `DELETE` takes the drive row's lock, so it waits for any upload batch in progress to commit, then removes everything.

**If the box uploads again**, the drive comes back. Step 0 checked this with `driveagent` 0.5.0 against a test drive:
- **A `scan`** re-opens the drive under a new stream, creating a new `agent_drives` row. It uploads only its own changes, so the drive shows with only what that scan found changed, often no files. It ends with `driveagent`'s usual hint, `1 drive(s) have history not yet uploaded; run "driveagent sync"`.
- **A `sync`** sees that the server's acked ranges no longer cover what it acknowledged before. It starts a new stream and uploads the whole drive. Its message blames a server restored from an older copy, which is wrong here but harmless.

So `driveagent` needs no change. The dialog warns that the drive comes back.

### One service's data of an account

The account's scans of that service, with their rows as `DeleteScan` removes them, and the service's living records:

| Service | Its scans (`scan_type`) | Its living records |
|---|---|---|
| Gmail | `gmail` (with their `messagemetadata`) | none |
| Google Drive | `google_drive` | `drive_items`, `drive_accounts`, Browse's totals for `drive:<client_key>` |
| Cloud Storage | `gcs` | `gcs_objects`, `gcs_prefix_totals`, `gcs_buckets` |
| Google Photos | `google_photos` (with their `photos_picked_items`) | `photos_picker_sessions` |

The account stays linked, with the service's access, so it can be scanned again. Its other services are untouched. Gmail was the first of these; the other three were added on review (2026-10-02).

### A Google account (disconnect)

In order:
1. **Read the refresh token**, before its row goes.
2. **Delete every scan of the account,** whatever its type. These are `scans` rows of the user whose `scanmetadata.client_key` is the account. Each is deleted with its rows, as `DeleteScan` does: `scandata`, `messagemetadata`, `photos_picked_items`, the old Photos tables, `gcs_scan_buckets`, `scanmetadata`.
3. **Delete the living records:**
   - Google Drive: `drive_items`, `drive_accounts`
   - Cloud Storage: `gcs_buckets`, `gcs_objects`, `gcs_prefix_totals`
   - Google Photos: `photos_picker_sessions`
   - Browse's totals for the account's sources (`drive:<client_key>`, and Cloud Storage's)

   All of these are keyed by `client_key`.
4. **Delete the linked account,** the `privatetokens` row, which holds its tokens.
5. **Revoke at Google, once 2–4 have committed:** `POST https://oauth2.googleapis.com/revoke` with the refresh token (form-encoded `token`). This removes Bhandaar from the account's third-party access list at once.
   - A `200`, or a `400 invalid_token` (already revoked or expired), counts as done.
   - Anything else, including no answer within 10 s, is recorded on the job as "Couldn't revoke at Google", with Google's HTTP status. The page tells the user to remove access at myaccount.google.com/permissions.
   - If the deletion fails, nothing is revoked, so the account still works. Revoking first would have left a dead but still-linked account, which could only be fixed by linking it again.
   - The endpoint is a package variable, as `tokenEndpoint` is, so tests can fake it.
6. **Mark the duplicates index stale** (once it's built).

Steps 2–4 run in one transaction, so an account is never left half deleted. If a later account linking adds the same Google account again, it's a new account, with a new `client_key` and no history.

## Rules

- **Only your own:** every deletion checks that the account or drive belongs to the logged-in user, and answers `404` otherwise, as other routes do.
- **Not during a scan, and no scan during a deletion:** deleting a service's data, or disconnecting an account, is refused (`409`) while one of that account's scans is `Running`. The dialog names the scan. Both sides take a lock on the account's `privatetokens` row:
  - **Starting a scan** (`db.StartScan`) takes `FOR SHARE` on the row. It refuses if a deletion of the account is queued or running, or if the account is gone. It then inserts the `scans` and `scanmetadata` rows in the same transaction, so a running scan always has its `client_key`.
  - **A deletion's transaction** takes `FOR UPDATE` on the row first, waiting for any scan being started, then checks for running scans inside the transaction.

  So no scan can start between the check and the deletion, and none can be left pointing at a deleted account. The handler's own check before queuing only answers early, with the scan's number. Agent drives have no such check, since uploads are handled as above.
- **One job per account, and one per drive:** a second request for the same job returns it. A request of another kind for the same account while one runs (the account, or another of its services) is `409`. The running-job index is on `(kind = 'agent_drive', target)`.
- **No orphaned totals:** a totals rebuild takes `FOR SHARE` on its source's row (`agent_drives` or `drive_accounts`) inside its transaction. If the row is gone, it writes nothing and removes what it marked. Deletions delete the source's row before its totals.
- **Typed confirmation is checked by the server too:**
  - Disconnecting sends the typed text.
  - The server compares it with the account's name, exactly as Manage data shows it: the masked email (`jyo****ri@gmail.com`), plus the key suffix when two accounts share a name (`jyo****ri@gmail.com · JVVY`, as `accountLabels` does).
  - A mismatch is `400` and nothing happens, so a UI bug can't skip the safeguard.
- **Logged:** every deletion is logged, with user, target and counts. Its job row is kept for 30 days, then deleted by `be` at startup and daily.

## Deletion jobs

Deleting seagate1 means removing about 2 million rows, and `be`'s HTTP writes time out after 10 s. Measured on a restored copy of the prod database (step 0):

| Deletion | Rows | Time |
|---|---|---|
| seagate1 from optiplex7070 | 888,902 files, 1,013,808 listings, plus 99,371 cached totals | 22 s |
| One account's Gmail data | 37,004 messages, 2 scans | 0.6 s |
| A whole account (Drive, Cloud Storage, all scans) | about 640,000 rows | 6 s |

So deletions run in the background:

```sql
deletions (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES agent_users(id),
  kind        TEXT NOT NULL,          -- 'agent_drive' | 'gmail' | 'account'
  target      TEXT NOT NULL,          -- the agent_drives id, or the client_key
  label       TEXT NOT NULL,          -- what Manage data showed, e.g. 'seagate1 (JyothriingasMBP.attlocal.net)'
  status      TEXT NOT NULL,          -- 'running' | 'done' | 'failed'
  counts      JSONB NOT NULL DEFAULT '{}',   -- e.g. {"files": 888902, "scans": 7}
  revoke      TEXT NOT NULL DEFAULT '',      -- for accounts: 'revoked' | 'already revoked' | 'failed: …'
  error       TEXT NOT NULL DEFAULT '',
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ
)
```

- A request inserts the row and starts a goroutine, then answers `202` with the job.
- One deletion runs at a time per user, behind a lock per user, so two big deletes of one user don't compete, and one user's never waits behind another's.
- A job left `running` by a restart is marked `failed` ("Interrupted") at startup. Every deletion is one transaction, so an interrupted one has deleted nothing, and can simply be run again.

## API

All need the session cookie, and see only the user's own data. The three `DELETE`s change state, so they go through the existing origin check.

| Route | Does |
|---|---|
| `GET /api/manage-data` | The user's linked accounts and uploaded drives, with what a deletion would remove (below), and any job running for each |
| `DELETE /api/agent-drives/{id}` | Deletes a drive from one box → `202 {job}` |
| `DELETE /api/accounts/{client_key}/{service}` | Deletes one service's data (`gmail`, `drive`, `gcs`, `photos`) → `202 {job}`; `409` while one of the account's scans runs |
| `DELETE /api/accounts/{client_key}` with `{"confirm": "<typed name>"}` | Disconnects the account → `202 {job}`; `400` if the text doesn't match; `409` while one of its scans runs |
| `GET /api/deletions/{id}` | A job's status, counts, and revoke result |

`GET /api/manage-data`:

```json
{
  "accounts": [{
    "client_key": "JVVYZFCI0PaW", "label": "jyo****ri@gmail.com",
    "services": ["gmail", "drive"],
    "recorded": {
      "gmail": {"granted": false, "files": 5744, "bytes": 516703723, "updated_at": "…"},
      "drive": {"granted": false, "files": 504, "bytes": 3961682114, "updated_at": "…"},
      "gcs": {"granted": false},
      "photos": {"granted": false}
    },
    "gmail_scans": 4, "scans": 9, "running_scan": 12, "job": null
  }],
  "drives": [{
    "id": 5, "drive_id": "seagate1", "hostname": "JyothriingasMBP.attlocal.net",
    "files": 29, "bytes": 625183198, "last_synced_at": "2026-09-27T22:22:55Z",
    "physical_drive": 1, "other_copies": ["optiplex7070", "jkurapati-apple.local"],
    "job": null
  }]
}
```

`service_scans` counts the account's scans per service (`scans` is them all). `recorded` has one entry per service, with the same totals Browse's tabs show (`files` is messages for Gmail, objects for Cloud Storage, items for Photos). It has none when nothing is recorded, and `granted` isn't used here. `running_scan` is there only while one runs. The counts come from Browse's totals where cached, otherwise they're counted.

## Manage data page

`/manage-data`, the fourth nav tab, after Request History:

```
 Manage data
 ┌ Linked Google accounts ──────────────────────────────────────────────┐
 │ jyo****ri@gmail.com                                                   │
 │ Gmail          5,744 messages · 493 MB     [ Delete Gmail data ]       │
 │ Google Drive   504 files · 3.7 GB          [ Delete Google Drive data ]│
 │ Cloud Storage —   Photos —      9 scans                               │
 │                                            [ Disconnect account ]     │
 ├───────────────────────────────────────────────────────────────────────┤
 │ jyo****i2@gmail.com  …                                                │
 └───────────────────────────────────────────────────────────────────────┘
 ┌ Uploaded drives ─────────────────────────────────────────────────────┐
 │ JyothriingasMBP.attlocal.net                                          │
 │   seagate1   29 files · 596 MB · synced 4d ago                        │
 │              Also uploaded from optiplex7070, jkurapati-apple.local   │
 │                                                     [ Delete… ]       │
 │ optiplex7070                                                          │
 │   seagate1   888,902 files · 1.5 TB …                  [ Delete… ]    │
 │   seagate2   …                                                        │
 └───────────────────────────────────────────────────────────────────────┘
```

- **Accounts:** a card per linked account: its name, what's recorded per service, each with a "Delete … data" button when it has data, the scan count, and "Disconnect account". The buttons use the danger style, outlined, not filled. "Delete Gmail data" is disabled when there's none.
- **Drives:** grouped by box (hostname), one row per drive: files, size, last sync. A drive linked to the same physical drive on other boxes lists those boxes, which helps pick the copy to delete.
- **A deletion in progress** replaces the target's buttons with "Deleting…" and a spinner. The page polls the job every 2 seconds. When it ends, the page shows the result:
  - **Done:** for example "Deleted seagate1 from JyothriingasMBP.attlocal.net: 29 files". The account or drive then leaves the list.
  - **Failed:** the error, and the target stays.

  After either, Browse, Request, Request History and Duplicates refetch.
- **Phones:** the cards stack, and the buttons go full width.

### Confirmation dialogs

A modal dialog (`role="dialog"`, `aria-modal`, focus trapped, Escape and Cancel close it). It opens with **Cancel focused**, so Enter alone never deletes.

**Delete a drive from one box** (a simple confirmation):

```
 Delete seagate1 from JyothriingasMBP.attlocal.net?
 This deletes what Bhandaar holds for this drive as uploaded from this box:
 29 files (596 MB recorded) and its scan history. Nothing on the drive, or
 in the copies uploaded from optiplex7070 and jkurapati-apple.local, is touched.
 If JyothriingasMBP.attlocal.net scans or syncs seagate1 again, it comes
 back: a scan re-creates it, and the next sync uploads all of it. This
 can't be undone.
                                   [ Cancel ]  [ Delete drive ]
```

**Delete a service's data** (a simple confirmation; Gmail shown):

```
 Delete Gmail data for jyo****ri@gmail.com?
 This deletes 5,744 messages and 4 Gmail scans from Bhandaar. Nothing in
 Gmail is touched, and the account stays connected, so you can scan it
 again. This can't be undone.
                                   [ Cancel ]  [ Delete Gmail data ]
```

**Disconnect account** (typed confirmation):

```
 Disconnect jyo****ri@gmail.com?
 This revokes Bhandaar's access at Google and deletes everything Bhandaar
 has recorded for this account: 5,744 messages, 504 Drive files, and all
 9 of its scans. Nothing in your Google account is touched. This can't be
 undone.
 To confirm, type jyo****ri@gmail.com below:
 [                                    ]
                                   [ Cancel ]  [ Disconnect account ]
```

- The **Disconnect account** button stays disabled until the text matches the name exactly. Leading and trailing spaces are ignored; case and the `*`s are not.
- Pressing Enter in the field submits only once the text matches.
- Pasting is allowed, as on GitHub.
- The name to type is shown in monospace, so the `*`s are clear.

## Privacy policy

Section 8 ("Your choices, and deleting your data") changes when this ships:
- Removing a Google account, or one service's data of it, can be done in **Manage data**, which also revokes access at Google. The email address stays for anything else, including deleting a whole Bhandaar account.
- Uploaded drives can be deleted per box in Manage data.

The effective date moves to the release date.

## Implementation order

One PR each, checked on dev.sm against the prod copy:

0. **Measure and check** on the prod copy (done 2026-10-02; results above):
   - how long deleting the largest agent drive takes (about 888,902 files and 99k folders' listings), and deleting an account with everything, which confirms the background jobs
   - what a `driveagent` 0.5.0 does after its drive is deleted on the server: it should re-open the drive and upload all of it, on both its next `scan` and its next `sync`

   The results go into this spec.
1. **Backend:**
   - the `deletions` table and job runner, including the restart and 30-day clean-up
   - the three deletions, in transactions
   - revoking at Google
   - the Manage data and job API, with the owner, running-scan and typed-text checks
   - tests (`BE_TEST_DB`): each deletion removes exactly its target and leaves other accounts, drives and users alone; the cascade from `agent_drives`; a scan running → `409`; a wrong typed name → `400` and nothing deleted; revoke against a fake endpoint (`200`, `400 invalid_token`, a failure, a timeout); an interrupted job marked failed at startup
2. **Manage data page:**
   - the nav tab, the account and drive cards, and the three dialogs
   - polling and the result messages, and refetching the other pages
   - the privacy policy update
   - tests: Cancel is focused first; Escape closes; the typed confirmation enables only on an exact match, and Enter submits only then; the right request for each action; results and errors shown

## Decisions

Made 2026-10-02:
- **Re-uploads:** a deleted drive comes back if its box scans or syncs it again; nothing blocks it, and the dialog says so.
- **One service:** Gmail, Google Drive, Cloud Storage or Google Photos, each with its scans and records; the account stays connected. Gmail was first; the other three were added on review.
- **Disconnect:** also revokes access at Google.
- **Typed confirmation:** the account's name as shown, masked.
- **Placement:** a Manage data page, as a nav tab (first planned as Settings in the user menu; moved on review).
- **Undo:** none; deletions are immediate.
- **Confirmation:** a dialog for every deletion, with a typed name for disconnecting.

## As built

- **Jobs:** each job's work runs behind its user's lock, so a user's deletions run one at a time. A drive deletion counts the drive's files inside its transaction, after locking the drive's row. A service job's label is the account's name plus the service, e.g. " · Gmail", and its kind is the service (`gmail`, `drive`, `gcs`, `photos`).
- **Review fixes (before merge):**
  - revoking moved after the deletion commits
  - locking between scans and deletions
  - one job per account
  - no totals for a deleted source
  - per-user job locks
  - dialogs that can't close while their request is out
  - the API renamed `/api/manage-data`, and Google's status included in a failed revoke
- **Results:** the page shows each finished job's result as a dismissible line at the top. A failed revoke is shown as a warning, with what to do. Finishing a job refetches this page, Browse's sources and folders, the linked accounts, and Request History.
- **Dialogs:** all three open with Cancel focused, including the typed one; you click or tab into the name field. While a dialog's request is out, Escape, the backdrop and Cancel don't close it, so a `400` or `409` is seen.
- **Privacy policy:** sections 7 and 8 now point to Manage data for disconnecting accounts and deleting drives, effective 2 October 2026.
- **Tests:** `be/db/deletion_test.go`, `be/web/manage_data_test.go` and `ui/src/test/manageData.test.tsx`.

