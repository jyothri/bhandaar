# Codebase Review: Open Items

Open and pending work from the review of the UI, backend and ops that started on 2026-09-23. Completed and won't-fix items (53 done, 1 won't fix), the change log and the investigation notes are in [archive/codebase-review-history.md](archive/codebase-review-history.md), under the same item numbers. Local setup is in [local-dev.md](local-dev.md).

Items 2.8–2.11, 4.8, 4.9 and 6.4 are new: leftover work split out of the notes of completed items, or found in production on 2026-09-24. Each says where it came from.

Status legend: `[ ]` open · **Deferred** = open, parked for now · **Blocked** = open, waiting on something outside this repo (reason in the item)

## Status

*Last updated: 2026-09-24*

| | Count |
|---|---|
| Open | 13 |
| *of which Deferred* | 1 (7.6) |
| *of which Blocked* | 1 (6.3) |

## Suggested order

1. **2.11** Check the Google OAuth app's publishing status. If it's still in "Testing", every linked account stops working a week after linking.
2. **4.9** A way to remove (or replace) a linked account, then clear the stale production entry.
3. **4.1** Results view (next feature).
4. The small ones, as convenient: 3.7, 4.8, 5.1, 6.4, 2.8, 2.9, 2.10.
5. When unblocked or revisited: 6.3, 7.6.

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

- [ ] **2.11 Linked Google accounts may expire after 7 days** — Google Cloud console (ops)
  Found in production on 2026-09-24: scan 4 failed with `invalid_grant`, because the account's refresh token, linked in March 2025, was no longer valid. That one was simply old. But if the OAuth consent screen is still in "Testing" status, Google also expires refresh tokens 7 days after they're issued, so every newly linked account would break a week later.
  *To check:* the OAuth app's publishing status. Publishing it lifts the 7-day limit.

## 3. React / TanStack Query idioms

- [ ] **3.7 Username is read from the option's text** — `src/routes/request.tsx:73`
  *Fix:* look up the account in `accounts` by `clientKey`.

## 4. UI / UX

- [ ] **4.1 The home route (`/`, "Data") is a placeholder** — `src/routes/index.tsx`
  `types/mail.ts` (`MessageMetadata`) is defined but unused, and the backend already serves `/api/gmaildata/{id}` and `/api/photos/{id}`.
  *Next feature:* a results view, linked from the history table by scan ID, with pagination and sizes.
  *Note:* by 7.9's decision ([archived](archive/codebase-review-history.md), won't fix), a scan's `messagemetadata` rows are only the messages *new* in that scan. Label per-scan results "new messages", and use the scan's processed count for the total matched.

- [ ] **4.2 Only Gmail scans can be requested**, although `ScanType` also lists Local, GDrive, GStorage and GPhotos.

- [ ] **4.8 The progress table overflows on phones** — `src/components/ScanProgress.tsx`
  Split out of 4.7 ([archived](archive/codebase-review-history.md)). At a 390 px viewport, the five-column progress table makes the page about 470 px wide, with or without an error message in it.
  *Fix:* stack the cells on narrow screens, or let the table scroll inside its container.

- [ ] **4.9 Linked accounts can't be removed** — `be/web/api.go`, `src/routes/request.tsx`
  Found in production on 2026-09-24. Re-linking an account adds a new `privatetokens` row, and the stale one stays in the Accounts list under the same name; picking it fails with `invalid_grant`. Production has one such stale row, from the account re-linked that day.
  *Fix:* let users remove an account, or have a re-link of the same email replace the existing row. Until then, the stale row can only be deleted by hand in the database.

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

- [ ] **7.6 Progress events never carry `completion_pct` or `eta_in_sec`** — **Deferred** — `be/collect/gmail.go:255-280`
  `logProgress` fills in the counts and elapsed time but leaves `CompletionPct` and `EtaInSec` at zero. Even the final event after scan 1 finished reported 0%. The UI shows the ETA column (always 0), and 4.3 plans a progress bar that would need these values.
  *Fix:* compute them from processed / (processed + pending), or send an explicit `done` flag with the final event. Otherwise drop both fields and the UI's ETA column.
  **Deferred (2026-09-24): non-trivial.** The total isn't known up front. `startGmailScan` (`:147-186`) lists messages page by page (`Messages.List(...).Q(filter)` + `NextPageToken`) while earlier pages are already being fetched, and `counter_pending` only grows as each page arrives (`:181`). The alternatives each have a catch:
  - Gmail's `resultSizeEstimate` is too rough for filtered queries.
  - Listing every page before fetching anything restructures the scan, costs extra quota and delays the first fetch.
  - `Labels.Get` counts are exact, but only for a single folder with no filter.

  *When revisited:* send a `listing_complete` flag. Before it's set, show "processed N, M queued". After it, processed + pending is the exact total, so a percentage (and an ETA from the processing rate) becomes meaningful. Until then, hide the always-0 ETA column in the UI (ties to 4.3). *Done in #14:* the ETA column is gone, and the progress bar is indeterminate until `completion_pct` is non-zero.
