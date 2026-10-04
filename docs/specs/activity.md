# Activity: Deletions and Agent Uploads

**Status:** proposed. Written 2026-10-04.

## Problem

Two kinds of work run for a user where they can't see it:

- **Deletions** from Manage data run as background jobs. The page shows a result banner only if it was open from start to finish. A deletion that finishes, or fails, while the user is elsewhere is never reported, although `be` keeps its result.
- **Agent uploads** happen on the user's machines, often from cron. Browse shows each agent drive's last sync, but nothing shows an upload running, how much it sent, what the server rejected, or that it failed. A failure that never reached the server (unreachable, interrupted) is visible only in a terminal nobody reads.

Scans, Photos picks, the duplicates index and folder totals already show on their own pages, and stay out of this.

## Goals

1. **An Activity page** listing the user's deletions and agent upload runs of the last 7 days, newest first, with how each went.
2. **A header icon** that shows when something is running, and when something failed that the user hasn't seen. It links to the page.
3. **Agent upload runs as the agent saw them:** grouped by driveagent run (one `scan`, or one drive of a `sync`), with what the server accepted and rejected, and how the run ended, including failures the server never saw.

Non-goals: notifications outside the app (email, push); acting from the Activity page (retrying a sync, cancelling a deletion); scans, picks and the other per-page work.

## What exists

| Piece | Today |
|---|---|
| Deletion jobs | `be`'s `deletions` table: `kind`, `target`, `label`, `status` (`running`, `done`, `failed`), `counts`, `revoke`, `error`, `started_at`, `finished_at`. Kept 30 days (`PurgeDeletionJobs`, daily) |
| Uploads | `PUT /agent/v1/drives/{drive_id}` opens a drive's stream; `POST …/changes` applies a batch and answers `{applied, skipped, duplicate, rejected}`; `agent_drives.last_synced_at` is the last batch's time. Nothing groups batches into runs, or records a run's end |
| driveagent | `scan` uploads one drive while it scans and drains after; `sync` uploads each drive's pending history. Each ends with exit 0, 3 (remote), 4 (upgrade), 1 (local) or 130/143 (interrupted) |

## Upload runs

An **upload run** is one drive's upload by one driveagent command: a `scan` (one drive), or one drive of a `sync`.

### On the agent (driveagent 0.8.0)

- **Run id:** each run gets a UUID when its drive is opened. Every `PUT /drives/{drive_id}` and `POST …/changes` of the run carries two headers: `Driveagent-Run-Id` and `Driveagent-Run-Kind` (`scan` or `sync`).
- **Report:** when the run ends, the agent sends `POST /agent/v1/runs` with `{runs: [report…]}`:

  ```json
  {"run_id": "…", "drive_id": "seagate1", "kind": "scan", "started_at": "…", "finished_at": "…",
   "outcome": "failed", "error": "agentserver unreachable for 2m0s", "uploaded": 1234, "pending": 5678}
  ```

  - `outcome`: `ok`, `failed` or `interrupted`.
  - `error`: the message the command printed.
  - `uploaded`: changes the server acknowledged in this run.
  - `pending`: what's left to upload for the drive.
- **Reports that can't be sent** go into `state.db` (`run_reports`, a local migration), and are sent after the next successful handshake and token, by `scan`, `sync` or `login`. That covers every failure the server never saw: the server unreachable from the start, a timeout mid-run, an interruption. The queue keeps at most 50 reports and drops the oldest, since only 7 days are shown anyway.
- **What isn't reported:** a run that uploaded nothing and ended `ok` (such as a `sync` from cron with nothing pending) isn't reported, and isn't listed.
- **Sending is best effort:** a failed send never changes the command's exit code.

### On the server (agentserver migration 5)

`agent_upload_runs`, one row per run:

| Column | |
|---|---|
| `id` | `BIGSERIAL` |
| `agent_id`, `drive_id` | The agent and its label for the drive; a report may name a drive never opened |
| `drive_pk` | The drive row, when there is one (`ON DELETE CASCADE`, so deleting a drive in Manage data removes its runs) |
| `run_id` | The agent's UUID; `NULL` for an older agent's batches (below). Unique per agent |
| `kind` | `scan`, `sync`, or `unknown` |
| `started_at`, `last_batch_at`, `finished_at` | |
| `batches`, `applied`, `rejected` | Counts, from the batches' answers |
| `outcome`, `error`, `pending` | From the report; `NULL` until it comes |

- **Batches:** `PUT` and each change batch upsert the run (by `agent_id` and `run_id`) in the same transaction as the batch, adding to its counts. `applied` counts entries applied, and `rejected` counts entries rejected.
- **Reports:** a report upserts its run: it fills in the outcome, or creates the row if no batch ever arrived (a run that failed before uploading anything).
- **Older agents** send no run id. Their batches for a drive join the drive's latest run without a run id if its last batch was under 10 minutes ago; otherwise they start a new one. Such runs have `kind = unknown` and no outcome.
- **No report:** a run is shown as finished (`done`, without an outcome) once 10 minutes pass with no batch. One with a run id but no report after 30 minutes is shown as `no word since <last batch>`.
- **Housekeeping:** a new task, `upload_runs`, deletes runs that finished, or went quiet, more than 7 days ago.

### Old and new together

- **Old agent, new server:** works as today, with heuristic runs.
- **New agent, old server:** the server ignores the unknown headers; `POST /agent/v1/runs` answers 404, and the agent drops the reports quietly, on that server.
- **The handshake's limits are unchanged.**

## In `be`

### Deletion jobs

Deletion jobs are still kept for **30 days** (`PurgeDeletionJobs`, unchanged); the Activity page shows those of the last 7 days. Manage data's banners are unchanged.

### API

- **`GET /api/activity`** returns the user's last 7 days, newest first:

  ```json
  {"entries": [...], "running": 1, "unseen_failures": 2, "seen_at": "…"}
  ```

  Each entry:
  - `kind`: `deletion` or `upload`
  - `id`: `deletion:<id>` or `upload:<id>`
  - `title`
  - `status`: `running`, `done`, `failed`, `interrupted`, or `no_word`
  - `started_at`, `finished_at`
  - `detail`: counts, rejected entries, the error
  - `link`: Manage data for a deletion; Browse at the agent drive for an upload

  Uploads come from `agent_upload_runs` joined to the user's agents (`agent_agents.user_id`), read only, like Browse.
- **`POST /api/activity/seen`** records `seen_at = now()` for the user (`activity_seen`, one row per user). A failure finished after `seen_at` is unseen.

Titles and details:

| Entry | Title | Detail |
|---|---|---|
| Deletion of a drive | `Delete seagate1 (optiplex)` | `888,902 files deleted`, or the error |
| Deletion of a service | `Delete Google Drive data of al***ce` | Items and scans deleted |
| Disconnecting an account | `Disconnect al***ce` | What was deleted, and the revoke result |
| Upload run | `seagate1 (optiplex) · scan` (`· sync`, or `· upload` for an older agent's) | `uploaded 12,345 changes · 3 rejected`; on failure the error, and `5,678 still to upload` |

## In the UI

- **Header icon** (`activity`), left of the user menu:
  - A spinner ring while `running > 0`.
  - A red dot while `unseen_failures > 0`.
  - It links to `/activity`.
  - The header polls `GET /api/activity` every 60 s, and every 10 s while something runs.
  - On phones it sits in the top bar, outside the nav menu.
- **Activity page** (`/activity`, not a nav tab):
  - Entries grouped by day, each with its status badge, title, times and detail, and a link (Manage data, or Browse at the drive).
  - Failures stand out (`danger` tokens).
  - A filter: All, Deletions, Uploads.
  - Opening the page marks everything seen.
  - Empty: `Nothing in the last 7 days.`

## Code

- **`agent/wire`:** the run headers, `RunReport` and `RunsRequest`.
- **agentserver:**
  - migration 5: `agent_upload_runs`
  - batch accounting in `PUT` and the apply transaction
  - `POST /agent/v1/runs`
  - the housekeeping task
- **driveagent 0.8.0:**
  - run ids, and the headers in `remote`
  - reports from `scan` and `sync`
  - the `run_reports` queue in `state.db`, sent after preflight
- **be:**
  - `db/activity.go`: the merged list and `activity_seen`
  - `web/activity.go`: the two routes
- **ui:**
  - `routes/activity.tsx`
  - the header icon
  - the types and API

## Tests

- **agentserver:**
  - a run's counts across batches, including rejected entries
  - a report before any batch, and after
  - older agents' runs, by the 10-minute rule
  - a drive's deletion removing its runs
  - the housekeeping task
  - another user's agent can't report into a run
- **driveagent:**
  - the headers on a scan and a sync
  - each outcome's report: ok, a timeout, interrupted, unreachable from the start
  - queued reports sent on the next run
  - no report for an empty `ok` sync
  - the queue's cap
  - a server without `/runs`
- **be:**
  - the merged list and its order
  - statuses, including `no_word`
  - another user's entries not shown
  - `seen_at` and unseen failures
  - the 7-day window
- **UI:** the icon's states, the page, the filter, marking seen.

## Implementation order

One PR, a commit per step:

1. **Wire and agentserver:** migration 5, runs from batches, `POST /agent/v1/runs`, housekeeping.
2. **driveagent 0.8.0:** run ids, headers, reports and their queue.
3. **be:** `/api/activity` and `activity_seen`.
4. **UI:** the header icon and the Activity page.
5. **Docs**, then a check on the dev stack: a deletion, a scan, a `sync` with the server stopped (reported once it's back), and an older agent's upload.

## Decisions

Made 2026-10-04:
- **Scope:** deletions and agent uploads only. Scans, picks, the duplicates index and folder totals stay on their own pages.
- **Uploads:** what the server saw, plus how each run ended as the agent reports it, including failures the server never saw (sent later).
- **Surface:** a header icon (running, unseen failures) linking to an Activity page.
- **Retention:** the Activity page shows 7 days. Deletion jobs are still kept 30 days; upload runs, which nothing else uses, 7.
- **Housekeeping:** agentserver's and `be`'s maintenance tasks are the server's, not a user's, and aren't listed.
