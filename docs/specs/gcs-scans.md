# Google Cloud Storage Scans

**Status:** proposed, 2026-09-28. Adds Google Cloud Storage as a source: scan a Google account's buckets, and browse them like Drive and agent drives. Builds on the Google Photos work (#39): its Photos service, retry helper and service tabs.

## Problem

Bhandaar scans Google Drive, Gmail and Google Photos, but not Cloud Storage, where large and forgotten data often sits, and where old object versions and soft-deleted objects keep costing money without showing up in the Console's usual views. What exists today is only leftovers:

| Piece | Where | State |
|---|---|---|
| `ScanType.GStorage` | `ui/src/types/scans.ts` | Nothing sends it, and the backend has no case for it (`POST /api/scans` answers "Unknown scan type") |
| `cloud.google.com/go/storage v1.50.0` | `be/go.mod` | Required, but no package imports it (`go mod tidy` would drop it) |
| `GOOGLE_APPLICATION_CREDENTIALS`, `~/keys/gae_creds.json` | `CLAUDE.md`, `README.md`, `be/README.md`, `docs/architecture.md` | Documented as needed "for Cloud Storage access", but no code or compose file reads it |

## Decisions

Taken with the owner on 2026-09-28:

| Question | Decision |
|---|---|
| Access | **The linked Google account.** Cloud Storage is a service the account grants, like Gmail, Drive and Photos, with read-only scopes. A scan sees what that Google account can read. No service account key |
| What a scan covers | **Pick a project, then its buckets.** The Request page lists the account's projects, then the project's buckets. One scan covers every bucket of the project, or one bucket, optionally under a prefix |
| Browse | **A folder tree from the latest scan.** A Cloud Storage tab on the account: buckets, then prefixes as folders, each with totals, from each bucket's latest complete scan |
| What's recorded | Name, size, MD5, time updated, and also **storage class**, **noncurrent versions** and **soft-deleted objects**, since all three decide what a bucket costs |

## API facts

Checked against Google's documentation on 2026-09-28 ([objects.list](https://docs.cloud.google.com/storage/docs/json_api/v1/objects/list), [the Object resource](https://docs.cloud.google.com/storage/docs/json_api/v1/objects), [buckets.list](https://docs.cloud.google.com/storage/docs/json_api/v1/buckets/list), [soft delete](https://docs.cloud.google.com/storage/docs/soft-delete), [projects.search](https://docs.cloud.google.com/resource-manager/reference/rest/v3/projects/search)). Step 0 checks the ones marked *(to check)* against a real account.

- **Projects:** Resource Manager `projects.search` (v3) returns the projects the caller has `resourcemanager.projects.get` on, paged. It accepts `cloudplatformprojects.readonly`, which is narrower than `cloud-platform.read-only`.
- **Buckets:** `buckets.list` needs a `project` and `storage.buckets.list`. It pages 1000 at a time (`projection=noAcl`), and answers each bucket's location, default storage class, `versioning.enabled`, and `softDeletePolicy.retentionDurationSeconds`. With `returnPartialSuccess=true`, buckets in unreachable regions are named in `unreachable` instead of failing the call.
- **Objects:** `objects.list` needs `storage.objects.list`. It pages up to 1000 at a time, takes a `prefix` and, with `delimiter=/`, lists one "folder" at a time. `versions=true` lists every version, live and noncurrent (noncurrent ones have `timeDeleted`). `softDeleted=true` lists *only* soft-deleted objects (they have `softDeleteTime` and `hardDeleteTime`). The two can't be combined, so a full picture takes two passes.
- **Object fields:** `size` (a string holding a uint64), `md5Hash` (base64; **absent for composite objects and CMEK-encrypted objects**), `crc32c`, `storageClass`, `updated`, `timeCreated`, `generation`, `componentCount`.
- **Soft delete** is on by default, with 7 days' retention (7–90 days, or off). Soft-deleted objects "continue to accrue storage charges until their retention period expires". Deleting before a class's minimum storage duration also costs, except for Standard. *(To check: whether listing soft-deleted objects needs more than `storage.objects.list`.)*
- **Scopes:** `https://www.googleapis.com/auth/devstorage.read_only` for buckets and objects. Access also depends on IAM: a Google account with no role on a project sees none of its buckets.
- **Cost of a scan:** each `objects.list` call is a Class A operation, billed to the bucket's project, not ours: one per 1000 objects, per pass. Requester Pays buckets need a `userProject` to bill, so a scan skips them and says so. *(To check: that the Cloud Storage JSON API and Cloud Resource Manager API have to be enabled in the OAuth client's own project, as the Picker API had to be.)*
- **Go:** `cloud.google.com/go/storage` (already in `go.mod`) takes a token source (`option.WithTokenSource`) and a fake endpoint (`option.WithEndpoint`) for tests. `Query.Versions`, `Query.SoftDeleted` and `Query.Delimiter` cover the passes. Resource Manager is `google.golang.org/api/cloudresourcemanager/v3`, already a dependency. *(To check: `Query.SoftDeleted` in v1.50.)*

## Design

### Linking: Cloud Storage as a service

- `be/db/accounts.go`: add `ServiceGcs = "gcs"`, mapped from `devstorage.read_only`, and also from `devstorage.read_write`, `devstorage.full_control`, `cloud-platform` and `cloud-platform.read-only`, which all include reading, in the fixed order Gmail, Drive, Photos, Cloud Storage.
- UI: `Service` gains `gcs`, and `serviceScopes.gcs` asks for **both** `devstorage.read_only` and `cloudplatformprojects.readonly` in one grant ("Grant Cloud Storage access"). If the account grants only the first (Google's consent screen lets the user untick scopes), the project list isn't available, and the Request page asks for a project ID instead.
- `privatetokens.scope` already records what was granted, so the Request page can tell which case it's in (`/api/accounts` gains `canListProjects` for an account with `gcs`).

### Request page

A **Cloud Storage** tab (`?type=gcs`):

1. **Project:** a select filled from `GET /api/gcs/{client_key}/projects` (active projects, by display name, with the ID). Or a text box for the ID when the account can't list projects, or has none listed.
2. **Buckets:** "All buckets" or one bucket, from `GET /api/gcs/{client_key}/projects/{project}/buckets` (name, location, default class, versioning, soft-delete retention).
3. **Prefix** (one bucket only): optional, e.g. `backups/2024/`.
4. **Include:** "Noncurrent versions" and "Soft-deleted objects", both on by default. Each adds a pass.
5. **Submit** starts the scan, with live progress as for the others.

`POST /api/scans` takes `{ScanType: "GStorage", GStorageScan: {ClientKey, ProjectId, Bucket, Prefix, Versions, SoftDeleted}}`. `checkScanRequest` checks the account has granted `gcs`, that the project ID and bucket name are well formed, and that the prefix fits.

### Backend

**`be/collect/gcs.go`:**

- `GcsProjects(userID, clientKey)` and `GcsBuckets(userID, clientKey, project)` answer the Request page's two lists. Errors from Google (403 on a project, API not enabled) become a message the page can show.
- `CloudStorage(scan GStorageScan, userID)`: logs a `gcs` scan (`LogStartScan`) and records `scanmetadata` (`search_path` = `<project>/<bucket>/<prefix>`, or `<project> (all buckets)`). Then, in the background, under the collectors' `lock`, for each bucket (one after another):
  1. **Live and noncurrent:** `objects.list` with `versions` (or not) and the prefix, 1000 a page. Each version is one row: live, or `noncurrent` when it has `timeDeleted`.
  2. **Soft-deleted:** when asked for, and the bucket's soft-delete retention is above 0, a second pass with `softDeleted=true`; each row is `soft_deleted`.
  3. Progress: objects listed so far, published under the account's `client_key`.
  4. Retry 429 and 5xx with backoff, as the Photos collector's `fetch` does (#39). A 403 on one bucket marks that bucket failed in the scan's record and moves on. The scan fails only if every bucket did.
- Buckets are listed one page at a time, never whole, so memory stays flat for large buckets. Rows go to the database in batches of 1000, as they're listed.
- **Folder totals** are computed after the listing, per bucket, from the rows: one row per prefix (split on `/`), with the object count and bytes under it, split by state (live, noncurrent, soft-deleted) and by storage class. That's the same idea as Drive's and local scans' folder rows, done in SQL (`INSERT … SELECT … GROUP BY` over each ancestor prefix) rather than in memory.

**Tables** (`be/db/gcs.go`):

```sql
CREATE TABLE IF NOT EXISTS gcs_buckets (          -- what a scan found of each bucket
  id              serial PRIMARY KEY,
  scan_id         INT NOT NULL REFERENCES scans (id),
  project_id      TEXT NOT NULL,
  bucket          TEXT NOT NULL,
  location        TEXT NOT NULL DEFAULT '',
  storage_class   TEXT NOT NULL DEFAULT '',        -- the bucket's default
  versioning      BOOLEAN NOT NULL DEFAULT false,
  soft_delete_days INT,                             -- NULL when off
  prefix          TEXT NOT NULL DEFAULT '',        -- '' for the whole bucket
  status          TEXT NOT NULL,                    -- completed | failed | skipped (Requester Pays)
  error           TEXT NOT NULL DEFAULT '',
  UNIQUE (scan_id, bucket)
);

CREATE TABLE IF NOT EXISTS gcs_objects (
  id              bigserial PRIMARY KEY,
  scan_id         INT NOT NULL REFERENCES scans (id),
  bucket          TEXT NOT NULL,
  name            TEXT NOT NULL,                    -- the object's full name
  parent          TEXT NOT NULL,                    -- its prefix up to the last '/', '' at the root
  generation      BIGINT NOT NULL,
  state           VARCHAR(12) NOT NULL,             -- live | noncurrent | soft_deleted
  size            BIGINT NOT NULL,
  storage_class   TEXT NOT NULL,
  md5hash         TEXT NOT NULL DEFAULT '',         -- hex; '' for composite and CMEK objects
  crc32c          TEXT NOT NULL DEFAULT '',
  updated         TIMESTAMPTZ,
  time_deleted    TIMESTAMPTZ,                      -- noncurrent: when it stopped being live
  hard_delete_time TIMESTAMPTZ,                     -- soft-deleted: when it goes for good
  UNIQUE (scan_id, bucket, name, generation, state)
);
CREATE INDEX IF NOT EXISTS gcs_objects_children ON gcs_objects (scan_id, bucket, parent);

CREATE TABLE IF NOT EXISTS gcs_prefix_totals (    -- one row per prefix ("folder") per scan
  scan_id         INT NOT NULL REFERENCES scans (id),
  bucket          TEXT NOT NULL,
  prefix          TEXT NOT NULL,                    -- '' is the bucket itself
  parent          TEXT,                             -- NULL for the bucket
  live_objects    BIGINT NOT NULL, live_bytes BIGINT NOT NULL,
  noncurrent_objects BIGINT NOT NULL, noncurrent_bytes BIGINT NOT NULL,
  soft_deleted_objects BIGINT NOT NULL, soft_deleted_bytes BIGINT NOT NULL,
  bytes_by_class  JSONB NOT NULL,                   -- live bytes per storage class
  PRIMARY KEY (scan_id, bucket, prefix)
);
CREATE INDEX IF NOT EXISTS gcs_prefix_totals_children ON gcs_prefix_totals (scan_id, bucket, parent);
```

Objects live in their own table, not `scandata`: they need the state, class, generation and parent columns, and the `parent` index is what makes a folder's page one indexed query over millions of rows. `DeleteScan` deletes all three tables' rows. `GetScanSummary` gains a `gcs` case: live objects and bytes as the item count and total, `folder_count` from `gcs_prefix_totals`, and the noncurrent and soft-deleted totals as extra fields.

**Routes:**

| Route | What it answers |
|---|---|
| `GET /api/gcs/{client_key}/projects` | The account's active projects: `[{projectId, displayName}]`, or 409 with a message if it can't list them |
| `GET /api/gcs/{client_key}/projects/{project}/buckets` | The project's buckets with their settings |
| `GET /api/gcs/{scan_id}?page=&bucket=&prefix=` | A page of a scan's objects under a prefix, for the results view |
| `GET /api/browse/google/{client_key}/gcs/children?bucket=&folder=&page=` | Browse: with no bucket, the buckets (from each one's latest complete scan); else a prefix's subfolders and objects, as the `FolderPage` Drive and agent drives use |

The Browse route checks the account is the user's (`checkGoogleAccount`); the others check the scan's owner, or the account, as their neighbours do.

### Which scan Browse reads

For each bucket, the **latest complete scan of the whole bucket**: a `gcs` scan whose `gcs_buckets` row for it is `completed` with `prefix = ''`. Prefix scans and failed buckets show on their own results pages, but never replace a bucket's tree in Browse. A bucket no longer returned by a newer whole-project scan is still shown from its last scan, marked "not seen since <date>".

### UI

- **Results view** (`scans.$scanId.tsx`, `gcs`): the summary (live objects and bytes, noncurrent and soft-deleted totals, bytes per storage class, and buckets that failed or were skipped), then a table of objects: name, bucket, size, class, state, updated, MD5.
- **Browse:** a **Cloud Storage** tab on each Google account, with totals like the other tabs. Its roots are the buckets (with location and class badges), and it uses `FolderTree` as-is: folders are prefixes, with their live totals and size bars. A folder's summary card adds its noncurrent and soft-deleted bytes, and bytes per class, since those are what a folder tree hides. Objects show their class, and noncurrent or soft-deleted ones are marked.
- `scanTypeLabel`: `gcs` → "Cloud Storage".

## Tests

- `be/collect/gcs_test.go`: a fake Cloud Storage and Resource Manager (`httptest` with `option.WithEndpoint`). It covers paging, both passes, noncurrent and soft-deleted states, composite objects (no MD5), a 403 on one bucket, skipping a Requester Pays bucket, retries, and progress under the `client_key`.
- `be/db/gcs_test.go` (needs `BE_TEST_DB`): saving objects, the prefix totals (nesting, states, classes), the summary, `DeleteScan`, and Browse's choice of scan (latest complete whole-bucket scan, not a prefix scan or failed bucket).
- `be/web/gcs_test.go`: owner and grant checks on every route, and `checkScanRequest`'s checks for project IDs, bucket names and prefixes.
- `be/db/accounts_test.go`: the storage scopes map to `gcs`.
- UI: the Request page's Cloud Storage tab (projects, the fallback text box, buckets, the options), the results view, and Browse's Cloud Storage tab.

## Implementation plan

One PR for the whole feature, with a commit per step. Each step leaves the branch working.

0. **Check before building** (a throwaway script, not merged): link a test account with both scopes, and confirm:
   - which APIs the OAuth client's project needs enabled;
   - that `projects.search` lists the account's projects with `cloudplatformprojects.readonly` alone;
   - the fields `buckets.list` answers;
   - that `objects.list` works with `versions=true`, and with `softDeleted=true`, and what permission the latter needs;
   - how composite objects come back;
   - how long 1000-object pages take.

   Also check `Query.SoftDeleted` in the pinned client library. Record the findings here.
1. **Cloud Storage as a linkable service:** `ServiceGcs`, the scope map, the UI `Service` type and scopes, `canListProjects`, and the tests.
2. **Project and bucket lists:** `collect.GcsProjects`, `collect.GcsBuckets`, and their routes, tested against the fake.
3. **The scan:** the tables, the collector with both passes, prefix totals, `DeleteScan`, `GetScanSummary`, the results route, and the tests.
4. **Request page:** the Cloud Storage tab and its tests.
5. **Results view and Browse:** the results view, the Browse route and its choice of scan, the Cloud Storage tab, and the tests.
6. **Docs and cleanup:** `architecture.md` (diagram, routes, collector, schema, scopes, a scan flow), `README.md`, `CLAUDE.md`. Remove `GOOGLE_APPLICATION_CREDENTIALS` and `~/keys/gae_creds.json` from the docs, since nothing needs them. Then mark this spec implemented with an "As built" section, and move it to `docs/archive/`.

Then an end-to-end run on dev.sm against a copy of production, as for Photos, before the PR.

## Open questions

1. **Keeping old scans' objects:** each scan stores a row per object version, so repeated scans of a large bucket add up (10 million objects is several GB of rows per scan). Should older scans of a bucket be pruned automatically, say keeping the latest two per bucket, or left to deleting scans by hand?
2. **Very large buckets:** listing is one call per 1000 objects per pass, so a 10-million-object bucket takes 10,000+ calls, likely most of an hour. That's fine for occasional scans. If it isn't, [Storage Insights inventory reports](https://docs.cloud.google.com/storage/docs/insights/inventory-reports) (a daily CSV or Parquet listing written to a bucket) could be read instead, but they have to be set up per bucket first. Out of scope unless step 0 shows listing is too slow.
3. **Cost estimates:** with storage class and bytes per class recorded, Browse could estimate the monthly cost per bucket and folder. That needs per-location price tables, so it's left for later.
