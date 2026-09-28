# Codebase Review: Open Items

Open and pending work from the review of the UI, backend and ops that started on 2026-09-23. Completed and won't-fix items (56 done, 1 won't fix), the change log and the investigation notes are in [archive/codebase-review-history.md](archive/codebase-review-history.md), under the same item numbers. Local setup is in [local-dev.md](local-dev.md).

Items 2.8–2.11, 4.8, 4.9 and 6.4 are new: leftover work split out of the notes of completed items, or found in production on 2026-09-24. 7.15 came out of the Drive scans work on 2026-09-27. Each says where it came from.

Status legend: `[ ]` open · **Deferred** = open, parked for now · **Blocked** = open, waiting on something outside this repo (reason in the item)

## Status

*Last updated: 2026-09-27*

| | Count |
|---|---|
| Open | 11 |
| *of which Deferred* | 2 (2.11, 7.6) |
| *of which Blocked* | 1 (6.3) |

## Suggested order

1. **4.9** A way to remove a linked account, then clear the stale production entry.
2. **7.15** Google Photos through the Picker API: [spec](specs/photos-picker.md), steps 2–6.
3. The small ones, as convenient: 4.8, 5.1, 6.4, 2.8, 2.9, 2.10.
4. When unblocked or revisited: 2.11, 6.3, 7.6.

Browsing files and folders by Google account and by agent drive was built in #35, from [archive/browse.md](archive/browse.md).

---

## 2. Configuration and deployment

- [ ] **2.8 Production runs `:latest` images** — prod `<prod-repo>/storagemanager/docker-compose.yml`
  Split out of 2.5 ([archived](archive/codebase-review-history.md)). CI now pushes `:latest` only from `main`, but production still pulls `:latest`, so a deploy can't be pinned or rolled back by name. Rollback images are kept by hand today (tagged `pre-review-2026-09`, see 2.6).
  *Fix:* have CI also push a tag per commit (e.g. the short SHA), and point production's compose file at it.

- [ ] **2.9 No keep-alive on the progress stream** — `be/web/sse.go` (optional)
  Split out of 2.4 ([archived](archive/codebase-review-history.md)). Production's nginx `/sse` timeout is now 1 hour, and the browser reconnects after a drop, so this is hardening only.
  *Fix:* send an SSE comment line (`: ping`) every ~30 seconds, so no proxy's idle timeout can cut a quiet stream.

- [ ] **2.10 The backend starts with an empty `DB_PASSWORD`** — `be/db/database.go` (optional)
  Split out of 2.6 ([archived](archive/codebase-review-history.md)). With `DB_PASSWORD` unset, the backend uses an empty password, and a misconfigured deploy only shows up as a generic "failed to ping database" error.
  *Fix:* fail fast with a clear message when `DB_PASSWORD` is empty and `DB_HOST` isn't `localhost`.

- [ ] **2.11 Linked Google accounts expire after 7 days** — **Deferred** — Google Cloud console (ops)
  Found in production on 2026-09-24: scan 4 failed with `invalid_grant`, because the account's refresh token, linked in March 2025, was no longer valid. That one was simply old. But if the OAuth consent screen is still in "Testing" status, Google also expires refresh tokens 7 days after they're issued, so every newly linked account would break a week later.
  *Checked 2026-09-27:* the OAuth client is in "Testing". So every linked account's refresh token expires 7 days after it's issued, and its scans then fail with `invalid_grant` (the progress panel already says to link the account again).
  *Deferred* by decision on 2026-09-27: moving the app to "In production", and any verification, wait. Until then, re-link an account before scanning it if its last link is over a week old. Once [archive/request-drive-scans.md](archive/request-drive-scans.md) step 1 is in, re-linking updates the account in place, so it keeps its history.
  *Fix, when revisited:* publish the app ("In production"). The restricted scopes (`gmail.readonly`, `drive.metadata.readonly`) then show Google's unverified-app warning, with a 100-user cap, unless the app goes through verification.

## 4. UI / UX

- [ ] **4.8 The progress table overflows on phones** — `src/components/ScanProgress.tsx`
  Split out of 4.7 ([archived](archive/codebase-review-history.md)). At a 390 px viewport, the five-column progress table makes the page about 470 px wide, with or without an error message in it.
  *Fix:* stack the cells on narrow screens, or let the table scroll inside its container.

- [ ] **4.9 Linked accounts can't be removed** — `be/web/api.go`, `src/routes/request.tsx`
  Found in production on 2026-09-24. Re-linking an account used to add a new `privatetokens` row, and the stale one stayed in the Accounts list under the same name; picking it fails with `invalid_grant`. Production has one such stale row (account 1), from the account re-linked that day.
  *Half done in #33* ([spec](archive/request-drive-scans.md#identity-and-re-linking-beweboauthgo)): accounts are identified by their Google account ID, and re-linking updates the account's row, keeping its `client_key`. The first re-link of an account from before that updates its newest row, so the stale row is left behind.
  *Left:* let users remove an account; then remove production's stale row that way. Its scans were recorded under the newer account, so removing it loses no history. Until then, it can only be deleted by hand in the database.

## 5. Code health

- [ ] **5.1** Delete the empty `src/App.css` and the unused types in `src/types/optionals.ts`, or start using them.
  *Correction (2026-09-24):* `App.css` isn't empty. It holds the Tailwind import, and since #14 the base theme styles, so keep it. Only `optionals.ts` is left.

## 6. Dependencies

- [ ] **6.3 TypeScript 7** — blocked
  Split out of 6.2 on 2026-09-24. The `typescript@7` package no longer exposes the compiler API (its root export is only the version; the rest is under `unstable/*`), and every `typescript-eslint` release, including canary, requires `typescript <6.1.0`. Upgrading would break `npm run lint` and the CI `check` job.
  *When unblocked:* upgrade once `typescript-eslint` supports TS 7. Running TS 7 for `tsc` alongside TS 6 for linting was considered and rejected: it means two compilers that may disagree.

- [ ] **6.4 Switch to `@vitejs/plugin-react`** — `ui/vite.config.ts`
  Found with the Vite 8 upgrade ([6.2, archived](archive/codebase-review-history.md)): the dev server and the tests print "We recommend switching to `@vitejs/plugin-react` for improved performance as no swc plugins are used". The app uses no SWC plugins.
  *Fix:* swap the plugin, then check the build, fast refresh on route components, and `npm test`. Low priority.

## 7. Backend (`be/`)

- [ ] **7.6 Progress events never carry `completion_pct` or `eta_in_sec`** — **Deferred** — `be/collect/common.go` (`logProgress`, shared by Gmail and Drive since #33)
  `logProgress` fills in the counts and elapsed time but leaves `CompletionPct` and `EtaInSec` at zero. Even the final event after scan 1 finished reported 0%. The UI shows the ETA column (always 0), and 4.3 plans a progress bar that would need these values.
  *Fix:* compute them from processed / (processed + pending), or send an explicit `done` flag with the final event. Otherwise drop both fields and the UI's ETA column.
  **Deferred (2026-09-24): non-trivial.** The total isn't known up front. `startGmailScan` (`:147-186`) lists messages page by page (`Messages.List(...).Q(filter)` + `NextPageToken`) while earlier pages are already being fetched, and `counter_pending` only grows as each page arrives (`:181`). The alternatives each have a catch:
  - Gmail's `resultSizeEstimate` is too rough for filtered queries.
  - Listing every page before fetching anything restructures the scan, costs extra quota and delays the first fetch.
  - `Labels.Get` counts are exact, but only for a single folder with no filter.

  *When revisited:* send a `listing_complete` flag. Before it's set, show "processed N, M queued". After it, processed + pending is the exact total, so a percentage (and an ETA from the processing rate) becomes meaningful. Until then, hide the always-0 ETA column in the UI (ties to 4.3). *Done in #14:* the ETA column is gone, and the progress bar is indeterminate until `completion_pct` is non-zero.

- [ ] **7.15 Google Photos scans can't work** — `be/collect/photos.go`, `be/web/api.go`
  Found while writing [archive/request-drive-scans.md](archive/request-drive-scans.md#google-photos-deferred) (2026-09-27). Since 2025-03-31 the Photos Library API no longer grants `photoslibrary.readonly` or `photoslibrary.sharing` to new authorizations. So the collector, which lists the whole library or an album, can't work for any account linked now. The Request page offers no Photos option. `GET /api/photos/albums` also still takes a raw refresh token in its URL.
  *Decided (2026-09-28):* move to the Photos Picker API. See [specs/photos-picker.md](specs/photos-picker.md): its step 0 found `HEAD` gives sizes. Step 1 removed the Library API collector, the albums route (and its raw refresh token), `GET /api/photos/{scan_id}` and the three tables, which were empty in production. Steps 2–6 are open.
