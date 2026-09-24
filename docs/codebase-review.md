# Codebase Review: UI, Backend, Ops

- **Date:** 2026-09-23
- **Scope:** started as a review of `ui/` (all 18 source/config files, ~900 lines). It grew to cover the backend (`be/`, section 7) and ops (CI, Docker, production nginx and compose on <prod-host>, section 2) as testing turned up issues there.
- **Reviewed at:** `feature/driveagent-relocated-sidecar` (commit `7e93442`); `ui/` is identical on `main`
- **Baseline (run after dev setup):** `tsc -b` passes · `npm run build` passes · `npm run lint` reports 5 errors, 2 warnings (all covered by items 1.7, 3.1, 3.5 and `prefer-const` in `src/api/index.ts`)

Status legend: `[ ]` open · `[x]` done · `[-]` won't fix · **Deferred** = open, parked for now (reason in the item)

## Status

*Last updated: 2026-09-24*

| | Count |
|---|---|
| Open | 12 |
| Done | 45 |
| Won't fix | 1 |
| *of which Deferred* | 1 (7.6) |

### Change log

This work was first raised as one PR (#6). After review it was split into focused PRs, so the per-commit hashes from #6 are no longer cited.

| Date | Where | Change | Items |
|---|---|---|---|
| 2026-09-23 | this doc | Review of `ui/` started; scope later grew to the backend and ops | — |
| 2026-09-24 | #7 | UI dependencies upgraded within current majors: 19 advisories (10 high) → 0; tailwind moved to devDependencies; renamed router devtools/plugin APIs; router plugin before `plugin-react-swc`; Node 22 pinned via `ui/.nvmrc` | 6.1, 2.2 |
| 2026-09-24 | #8 (stacked on #7) | Backend URL and Google client ID from Vite env vars, checked at build time and at runtime; optional `DEV_PUBLIC_HOST`; UI Dockerfile hardened; CI pushes images only from `main`, runs `go test -race`, path filters fixed | 2.1, 2.3, 2.5, 2.7 |
| 2026-09-24 | #9 | `flag.Parse()` in `main` with lazily built OAuth configs; progress hub rewritten as a non-blocking broadcast (keeps newest on full buffer, separate all-scans set); Gmail scan waits for in-flight fetches, no start-time race, no pending leak; first backend tests | 7.7, 7.8, 7.11, 7.12, 7.13, 7.14 |
| 2026-09-24 | production (outside this repo) | Reverse proxy for the dev endpoint; SSE proxy timeouts; UI cache headers; `be` service passes `DB_*` | 2.3 (cache headers), 2.4, 2.6 |
| 2026-09-24 | this doc | Findings and decisions: 7.6 deferred, 7.9 won't fix (keep global dedupe), 2.6 investigation, PR #6 review follow-ups | 7.6, 7.9, 7.10 (open) |
| 2026-09-24 | production deploy | Backend and UI images built from `main` after #7–#9 deployed; `DB_*` connection and schema migration confirmed; rollback images kept until 2027-09-24 | 2.2, 2.6, 7.7, 7.8, 7.11–7.14 live |
| 2026-09-24 | #12 | UI bugs and security: random OAuth `state` checked on the callback; OAuth URLs built with `URLSearchParams`; callback redirect moved to `beforeLoad`; shared `fetchJson`; SSE hook fixes; date and Gmail filter fixes; label targets | 1.1–1.10 |
| 2026-09-24 | #13 | React / TanStack Query idioms: Gmail filter computed during render; request form in one state object; `enabled` instead of the `"none"` sentinel; query keys in one place, with invalidation of the real ones; mutation errors handled once and Submit disabled while pending; one message at a time; `Header` inside the router. `npm run lint` is clean | 3.1–3.6, 3.8 |
| 2026-09-24 | #14 | Durations, start times and an indeterminate progress bar; backend returns scan times as UTC instants; empty, loading and error states in history; th cells and typo; shared `Table` / `Input`; dark mode for body and form; favicon and package name; no `console.log`; `typecheck` script and a CI `check` job. Review follow-ups: cancelled consent message, `replace` on the callback redirect, stream errors before the first update, history refetch until scans finish, reversed dates rejected, no trailing space in the filter. PR review: `scans` time columns migrated to `timestamptz`; scans that can't start, or are left open by a restart, marked Failed; history shows status and polls only for recent open scans | 4.3–4.6, 5.2, 5.3; 1.11–1.14, 3.9, 5.5 |
| 2026-09-24 | #15 | Major upgrades: Vite 8 and plugin-react-swc 4; ESLint 10 and react-hooks 7 (React Compiler rules, no code changes needed); TypeScript 6.0; globals 17 and react-refresh 0.5 (its Vite preset, off for route files). TypeScript 7 split out as 6.3 | 6.2; 6.3 (open) |
| 2026-09-24 | #16 (stacked on #15) | Vitest + Testing Library: 36 unit tests (filter builder, date and duration formatting, `fetchJson`, OAuth state) and 8 request-form tests through the real router; filter logic moved to `src/gmailFilter.ts`; `npm test` runs in the CI `check` job | 5.4 |

---

## 1. Bugs and security

- [x] **1.1 OAuth `state` is a fixed placeholder and never checked** — `src/routes/request.tsx:129`
  Sends `state = "YOUR_CUSTOM_STATE"`; `be/web/oauth.go` has no `state` handling. This leaves account linking open to CSRF.
  *Done in #12 (frontend only):* `src/oauthState.ts` creates a `crypto.randomUUID()` per link attempt and keeps it in `sessionStorage`. `/oauth/glink` forwards the code to the backend only if the returned `state` matches, and clears the stored value either way, so each one works once. Otherwise it shows "Account linking failed" with a link back. *Not done:* backend-issued state. It belongs with 7.3 (redirect allowlist).

- [x] **1.2 OAuth URLs are built without encoding** — `src/routes/oauth/glink.tsx:25`, `src/routes/request.tsx:132`
  `code`, `scope` and `redirect_uri` are pasted into the query string as-is. `scope` contains `:` and `/`, and a multi-scope list with spaces would break.
  *Fix:* build the URLs with `URLSearchParams`.
  *Done in #12:* both the Google authorization URL and the backend `/api/glink` URL are built with `URLSearchParams`; the redirect URI uses `window.location.origin`.

- [x] **1.3 Redirect runs during render** — `src/routes/oauth/glink.tsx:26`
  `window.location.href = ...` in the render body runs twice under StrictMode, and again on any re-render, so the one-time auth code can be sent twice.
  *Fix:* redirect from the route's `beforeLoad` (`throw redirect({ href })`) or from a `useEffect`.
  *Done in #12:* `beforeLoad` checks the state (1.1) and throws `redirect({ href })` to the backend. The router treats an absolute `href` as a full-page navigation. A callback without a `code` (e.g. access denied) isn't forwarded.

- [x] **1.4 Errors become `"[object Object]"`, and `response.ok` is never checked** — `src/api/index.ts:23`, `:60`
  `throw new Error(content)` stringifies an object. A 500 with an HTML body fails later as a confusing JSON parse error.
  *Fix:* add one shared `fetchJson<T>()` helper that checks `ok` and throws `new Error(content.error ?? response.statusText)`.
  *Done in #12:* all API calls use `fetchJson<T>()`. Most backend errors are plain text from `http.Error`, and a few are JSON `{ error: { message } }`, so the helper uses the text body or `error.message`. Anything else, such as a proxy's HTML error page, falls back to the HTTP status. The request page shows the message when a scan request fails.

- [x] **1.5 Account key isn't URL-encoded** — `src/api/index.ts:56`
  *Fix:* wrap it in `encodeURIComponent(accountKey)`.
  *Done in #12.*

- [x] **1.6 `ScanProgress` can render a stray "0"** — `src/components/ScanProgress.tsx:7`, `:21`
  `sseData.scan_id && (...)` renders `0` when the scan ID is 0, and it relies on `{} as Progress` being falsy.
  *Fix:* use `useState<Progress | null>(null)` and `if (!sseData) return null`.
  *Done in #12.*

- [x] **1.7 Problems in the SSE hook** — `src/components/hooks/useSse.ts`
  - `setData` is missing from the effect's dependencies (line 39). It works only because the effect never re-reads the caller's closure.
  - Errors are cleared with `setError("")` instead of `null`.
  - `onerror` reports an error even while the browser is reconnecting on its own. Check `eventSource.readyState === EventSource.CLOSED` first.
  - The callback is typed `any`. Make the hook generic: `useSse<T>(..., onData: (d: T) => void)`.

  *Done in #12:* the callback is kept in a ref updated after each render, rather than listed as a dependency, so a new function from the caller doesn't reconnect the stream. Errors are cleared with `null` (also on open), `onerror` reports only when `readyState === EventSource.CLOSED`, and the hook is generic. `ScanProgress` passes its state setter directly.

- [x] **1.8 Date round-trip does nothing and can shift the date** — `src/routes/request.tsx:76-83`
  Builds a local-time `Date` and then calls `toISOString()` (UTC). East of UTC this moves the date back one day.
  *Fix:* the `<input type="date">` value is already `YYYY-MM-DD`; store it as is.
  *Done in #12.*

- [x] **1.9 Gmail filter syntax errors** — `src/routes/request.tsx:105-120`
  - ~~`label:unread` should be `is:unread`.~~ *Corrected 2026-09-24:* scan 1 used `label:inbox label:unread` and returned 40 unread inbox messages, so Gmail accepts `label:unread`. `is:unread` is the documented form, but switching is optional.
  - Gmail's `before:` is exclusive, so the chosen end date is left out. Add one day, or label the field to say so.
  - Stray double space after `after:` (line 114).

  *Done in #12:* `before:` uses the day after the chosen end date, so the end date is included. The day is added in UTC, so it doesn't depend on the browser's time zone. Unread now uses `is:unread`, and the double space is gone.

- [x] **1.10 Three labels point at the wrong input** — `src/routes/request.tsx:174`, `:187`, `:200`
  The Inbox, Unread and Date range labels all use `htmlFor="filter"`, so clicking any of them focuses the query box.
  *Fix:* point them at `inbox`, `unread` and `datepicker-range-start`.
  *Done in #12.*

Raised in review after #13; all done in #14:

- [x] **1.11 Cancelled consent shows the wrong message** — `src/routes/oauth/glink.tsx`
  Declining on Google's screen returns `?error=access_denied&state=…` with no code, and the page said the response "doesn't match this browser session".
  *Done in #14:* `validateSearch` reads `error`. `access_denied` shows "Linking was cancelled."; any other error is named.

- [x] **1.12 Back returns to the spent callback** — `src/routes/oauth/glink.tsx`
  The redirect to the backend pushed a history entry, so Back returned to `/oauth/glink?code=…`, showed "Account linking failed", and left the code in history.
  *Done in #14:* `throw redirect({ href, replace: true })`, which makes the router use `window.location.replace`. Verified: linking adds one history entry instead of two, and Back lands on `/request`.

- [x] **1.13 Stream errors are hidden before the first update** — `src/components/ScanProgress.tsx`
  `ScanProgress` returned `null` until data arrived, so a stream that failed immediately showed nothing.
  *Done in #14:* the error line renders with or without data. It still appears only once the `EventSource` is `CLOSED` (1.7), e.g. on an HTTP error; while the browser keeps retrying an unreachable server, nothing is shown.

- [x] **1.14 Reversed dates aren't caught** — `src/routes/request.tsx`
  An end date before the start date gives a valid filter that matches nothing, so the scan "succeeds" with zero messages.
  *Done in #14:* `submitRequest` rejects it, comparing the `YYYY-MM-DD` strings.

## 2. Configuration and deployment

- [x] **2.1 Backend URL and OAuth client ID are hard-coded** — `src/api/index.ts:4`, `src/routes/request.tsx:128`
  Local dev always talks to production.
  *Fix:* use `import.meta.env.VITE_BACKEND_URL` and `VITE_GOOGLE_CLIENT_ID`, and add a `.env.example`.
  *Done in #8:* both values come from `src/config.ts`, which throws a clear error at startup if either is missing; they are typed in `src/vite-env.d.ts`. `vite.config.ts` checks the same variables at build time (`command === "build"`), so a misconfigured build fails instead of shipping a blank page; the runtime check stays as a backstop. Instead of a `.env.example`, the per-mode files are committed: `ui/.env.development` (`http://localhost:8090`) and `ui/.env.production` (production URL, so the Docker build is unchanged). Neither value is secret. Local overrides go in `.env.development.local`, which is gitignored.

- [x] **2.2 Router devtools are mounted in every build** — `src/routes/__root.tsx:20`
  *Fix:* gate them behind `import.meta.env.DEV`, or lazy-load them.
  *Done, no code change needed:* fixed by the dependency upgrade (#7). `@tanstack/react-router-devtools` 1.167 exports a component that returns `null` unless `NODE_ENV === "development"`, and the production build drops the real implementation. Verified on 2026-09-24: none of the devtools code (e.g. the "Open TanStack Router Devtools" string from `router-devtools-core`) is in `dist/`, and the dev server still loads it.

- [x] **2.3 Dockerfile hardening** — `Dockerfile`
  - Pin the base image (e.g. `node:22-alpine` instead of `node:latest`).
  - Use `npm ci` instead of `npm install`.
  - Add an nginx SPA fallback (`try_files $uri /index.html`). Without it, a browser landing directly on `/oauth/glink` or `/requests` gets a 404, and the Google OAuth redirect lands on `/oauth/glink`. ~~**High priority.**~~ *Correction 2026-09-24:* production isn't affected today. Its `ui` container mounts `<prod-repo>/storagemanager/nginx/conf/nginx.conf` on <prod-host>, which already has `try_files $uri /index.html`. The image itself lacked the config, though.

  *Done:*
  - The builder is `node:22-alpine` (matches `.nvmrc`) and uses `npm ci`. The runtime is `nginx:stable-alpine`.
  - `ui/.dockerignore` was never used, because the build context is the repo root. It is replaced by `ui/Dockerfile.dockerignore`, which BuildKit reads for `ui/Dockerfile`. It keeps a local `ui/node_modules`, `dist` and `.env*.local` out of the image.
  - Verified with a local build: the image builds, only `https://sm.jkurapati.com` is baked in, and it's 93 MB.
  - **The serving config deliberately stays out of this repo.** Production's `ui` container mounts its own `nginx.conf` from the `<prod-repo>` repo over `/etc/nginx/conf.d/`, and nothing else runs this image. So a copy here would never be used, and it would drift. The image ships nginx's stock config, so a plain `docker run` returns 404 for client-side routes; mount a config if you run it standalone. A baked-in `ui/nginx.conf` was tried and removed on 2026-09-24.
  - **Cache headers were added to production's config instead** (2026-09-24, prod `<prod-repo>/storagemanager/nginx/conf/nginx.conf`; committed in `<prod-repo>` as `d1caefe`). Hashed `/assets/*` files get `Cache-Control: public, max-age=31536000, immutable`, and a missing asset returns a real 404 instead of `index.html`. Everything else, including `index.html` and the SPA fallback, gets `no-cache`, so a new deploy is picked up. After reloading the container, `/`, `/requests` and `/oauth/glink` still return `index.html` (200).

- [x] **2.4 nginx drops the progress stream every 60 seconds** — prod nginx `<prod-repo>/nginx/nginx/conf/nginx.conf`, `location /sse` in the `sm` and the dev endpoint blocks
  Seen 2026-09-24: progress-stream connections through the dev endpoint ended at exactly 1m0.01s and 1m3.0s. That's nginx's default `proxy_read_timeout` of 60s, which cuts the stream whenever no event arrives for a minute. The browser reconnects, but each drop makes the UI briefly show "Connection lost" (see 1.7). nginx may also buffer the stream and delay events.
  *Fix:* in both `/sse` locations, add `proxy_read_timeout 1h;`, `proxy_buffering off;` and `proxy_cache off;`. The `Upgrade`/`Connection` headers aren't needed for SSE. Optionally have the backend send a keep-alive comment every ~30s, so any proxy timeout stays harmless.
  *Done on the prod box on 2026-09-24; not in this repo:* added `proxy_read_timeout 1h; proxy_buffering off; proxy_cache off;` to the `/sse` locations of both `sm.jkurapati.com` and `<dev-endpoint>`. Ran `nginx -t`, then reloaded (no restart). Committed in `<prod-repo>` as `8fb8cd9`, together with the the dev endpoint block. *Confirmed 2026-09-24:* after the reload, dev backend logs show streams through the dev endpoint staying open for 6m57s, 13m51s and 3m20s, with no more drops at exactly 60s. The keep-alive comment is not done.

- [x] **2.5 CI pushes `:latest` from pull requests** — `.github/workflows/ui-docker-image.yml`, `backend-docker-image.yml`
  Both workflows run on `pull_request` as well as `push` to `main`, and call `docker-build-push` with `addLatest: true` and pushing left on. So building any PR that touches `ui/` or `be/` overwrites `jyothri/bhandaar-ui:latest` / `jyothri/bhandaar:latest`, which production runs (`<prod-repo>/storagemanager/docker-compose.yml`). The next `docker compose pull` there would deploy unmerged code.
  *Fix:* set `pushImage: ${{ github.event_name == 'push' }}` (build-only on PRs), or split the build and push jobs. Consider pinning production to a version tag instead of `:latest`.
  *Done (2026-09-24):* both workflows now pass `pushImage: ${{ github.event_name == 'push' }}`, and the only `push` trigger is `main`. So PRs still build, which catches broken Dockerfiles, but never push. In `docker-build-push@v6`, `skipPush` is `getInput("pushImage") === "false"`. With push skipped, it still logs in to Docker Hub when credentials are available (for private base images), and skips the login otherwise, e.g. for fork PRs without secrets. The UI workflow also sets `enableBuildKit: true`, because `ui/Dockerfile.dockerignore` (2.3) is only honoured by BuildKit. It worked before only because the runner's Docker already defaults to BuildKit. Both files pass `actionlint` 1.7.12.
  *Not done:* pinning production to a version tag instead of `:latest`. *To confirm:* the next PR's run log should show the build step without a push.

- [x] **2.6 Production compose passes DB settings under names the backend doesn't read** — prod `<prod-repo>/storagemanager/docker-compose.yml`, `be/db/database.go:51-54`
  Since issue #9 (`5394a8c`), the backend reads `DB_HOST`/`DB_USER`/`DB_PASSWORD`/`DB_NAME`, but the `be` service passes `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB`. A current backend image would ignore those and use the defaults (`hddb`, empty password, `hdd_db`). It works only if the production values happen to match. This is unverified: I didn't read the production values.
  *Investigated 2026-09-24 (read-only; no secrets printed). It's a code change on top of config that was always dead:*
  - The `POSTGRES_*` entries on `be` date from the prod compose's first commit (`6b554d0`, 2025-03-09, in `<prod-repo>`). **No backend version ever read them.**
  - Before #9, `database.go` hard-coded the connection settings; #9 moved them to `DB_*` environment variables.
  - #9 (`5394a8c`, 2025-12-24) switched to `DB_*` with defaults `hddb` / **empty password** / `hdd_db`. It also changed the repo's own `be/build/docker-compose.yml` to `DB_*`, but the separate prod compose wasn't updated.
  - (Details of production database authentication are omitted from the public repo.)
  - **So the next backend image pull will fail to connect to the database.** That same pull is what delivers 7.7 and 7.8.
  - ~~The obvious mapping isn't enough: prod's `.env` has an empty `<prod-env-var>`…~~ *Corrected 2026-09-24:* that check tested the wrong variable name. Prod's `.env` (and `.env.sops`) define **`<prod-env-var>`** (set, non-empty), and the compose file already passes `${<prod-env-var>}` to both `hdd_db` and `be`. Logging in over TCP with `<prod-env-var>` works. The secret is fine; only the names the `be` service passes are wrong.

  *Fix (no secret changes):* in prod `<prod-repo>/storagemanager/docker-compose.yml`, in the `be` service only, replace the three `POSTGRES_*` entries with `DB_HOST=hdd_db`, `DB_PORT=5432`, `DB_USER=${<prod-env-var>}`, `DB_PASSWORD=${<prod-env-var>}`, `DB_NAME=${<prod-env-var>}` and `DB_SSL_MODE=disable`. Leave `hdd_db`'s own `POSTGRES_*` alone. Do this before pulling the next backend image. The current image ignores these variables, so the edit is safe to apply now. Back up the database first: the new image also runs schema migrations.
  *Done 2026-09-24 (by the owner, on the prod box):* database backed up to `a local SQL dump` (73K). The `be` service now passes `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME`/`DB_SSL_MODE`, and `hdd_db`'s own `POSTGRES_*` are untouched. `docker compose config` is clean. `be` was recreated with the current image: it logged `Successfully connected to DB!`, `/api/health` returned 200, and `/api/scans` still returns all historical data. Committed in `<prod-repo>` as `3475aac` (not pushed).
  *Caveat:* the recreate used the image already running, so it doesn't prove the new `DB_*` names work. They are first exercised by the next backend image: check its log for `Successfully connected to database`. Tag the current image before pulling, so you can roll back.
  *Confirmed on deploy (2026-09-24):* the `main` image with #9 logged `Connecting to database host=hdd_db … user=hddb` and then `Successfully connected to database`, so the `DB_*` names work. It also ran the migration that adds `status`, `error_msg` and `completed_at` to `scans`; existing scans are intact. The previous backend and UI images are kept, tagged `pre-review-2026-09`, for rollback. A one-off cron job on the prod box removes them on or after 2027-09-24.

  Optionally, make the backend fail fast with a clear error when `DB_PASSWORD` is empty outside local dev.

- [x] **2.7 CI never ran the tests, and path filters missed build inputs** — `.github/workflows/backend-docker-image.yml`, `ui-docker-image.yml`
  Raised in PR #6 review: both workflows only built Docker images, so the new tests (7.7, 7.8) could regress unnoticed. The backend workflow's filter was `"**.go"`, so changes to `be/go.mod`, `be/go.sum`, `be/build/Dockerfile` or the workflow itself didn't trigger it. The filter also matched the unrelated `agent/linux/` Go module. The UI workflow didn't trigger on its own changes either.
  *Done (2026-09-24):* the backend workflow has a `test` job (`actions/setup-go@v5` with `go-version-file: be/go.mod`, then `go test -race ./...` in `be/`; `ubuntu-latest` has gcc for cgo). The image job `needs: test`, so a failing test also blocks the push from `main`. Filters are now `be/**` + the workflow file for the backend, and `ui/**` + the workflow file for the UI. Both pass `actionlint`.

## 3. React / TanStack Query idioms

- [x] **3.1 `queryFilter` is derived data kept in state** — `src/routes/request.tsx:101-120`
  A `useEffect` calls `setQueryFilter`, causing an extra render and an exhaustive-deps lint warning.
  *Fix:* compute it during render (`useMemo` optional).
  *Done in #13:* `buildGmailFilter()` and `dateForApi()` are pure functions outside the component, and the filter is computed during render (no `useMemo`; it's cheap).

- [x] **3.2 Form state is spread across many `useState` calls** — `src/routes/request.tsx:19-25`
  Consider one form-state object, or a form library if more scan types are coming.
  *Done in #13:* one typed `RequestForm` object, changed through `updateForm(changes)`. No form library for now.

- [x] **3.3 Use `enabled` instead of the `"none"` sentinel** — `src/routes/requests.tsx:23`, `src/api/index.ts:52`
  *Fix:* pass `enabled: selectedAccount !== "none"` and drop the special case from `getScanRequests`.
  *Done in #13.*

- [x] **3.4 Invalidated query key doesn't exist** — `src/routes/request.tsx:36`
  It invalidates `["scans"]`, which no query uses. Because of `staleTime: Infinity`, the history page stays stale after a new scan.
  *Fix:* define keys in one place (e.g. `queryKeys.scanRequests(account)`) and invalidate `getScanRequests`.
  *Done in #13:* keys live in `src/api/queryKeys.ts`. A successful scan request invalidates every account's scan-request list, and the scanned-accounts list, since a first scan adds its account there.

- [x] **3.5 Mutation errors are handled twice** — `src/routes/request.tsx:40`, `:63-68`
  `onError` duplicates the try/catch around `mutateAsync`.
  *Fix:* keep one, and use `isPending` to disable Submit so it can't be double-clicked.
  *Done in #13:* kept `onError`, which now shows the message; `submitRequest` calls `mutate`. Submit is disabled, and reads "Submitting…", while pending.

- [x] **3.6 Stale messages linger** — `src/routes/request.tsx`
  `infoMessage` is never cleared on error, so success and error text can show at the same time.
  *Done in #13:* one `message` state with a `kind` (error or info). Each new message replaces the last one, and submitting clears it.

- [ ] **3.7 Username is read from the option's text** — `src/routes/request.tsx:73`
  *Fix:* look up the account in `accounts` by `clientKey`.

- [x] **3.8 `Header` is outside `RouterProvider`** — `src/App.tsx:32`
  It can't use `Link` or router hooks.
  *Fix:* move it into `__root.tsx` alongside the nav.
  *Done in #13.*

- [x] **3.9 Request history never picks up a finished scan** — `src/routes/requests.tsx`
  Raised in review after #13. `scanRequests` kept `staleTime: Infinity`, so after the one fetch triggered by submitting, a running scan stayed "not finished" until a full reload.
  *Done in #14:* default `staleTime` (refetch on mount), `refetchOnWindowFocus: true` for this query (it's off globally in `App.tsx`), and a 10-second `refetchInterval` while any listed scan has no end time. *PR #14 review:* scans that never get an end time kept the page polling. Fixed in the same PR:
  - The backend marks a scan Failed when `Gmail()`, `CloudDrive()` or `Photos()` returns early after `LogStartScan`.
  - At startup, it marks scans that are still open Failed ("Interrupted: the server stopped before the scan finished"), leaving their end time null.
  - The history API includes `status`, and the Duration column reads "Failed" or "Failed after m:ss".
  - Polling runs only while a scan has no end time, isn't Failed, and started less than 6 hours ago.

## 4. UI / UX

- [ ] **4.1 The home route (`/`, "Data") is a placeholder** — `src/routes/index.tsx`
  `types/mail.ts` (`MessageMetadata`) is defined but unused, and the backend already serves `/api/gmaildata/{id}` and `/api/photos/{id}`.
  *Next feature:* a results view, linked from the history table by scan ID, with pagination and sizes.

- [ ] **4.2 Only Gmail scans can be requested**, although `ScanType` also lists Local, GDrive, GStorage and GPhotos.

- [x] **4.3 Values are shown raw**
  - The progress table shows raw seconds and ignores `completion_pct`. Add a `<progress>` bar and mm:ss formatting.
  - History shows the raw `scan_start_time`. Format it with `Intl.DateTimeFormat`.

  *Done in #14:*
  - Elapsed time and history durations are m:ss (h:mm:ss from an hour up), via `src/format.ts`. A scan with no end time reads "Not finished" instead of `-1`.
  - The always-0 ETA column became a Progress column: an indeterminate `<progress>` while messages are being fetched, and a percentage once `completion_pct` is non-zero (7.6).
  - **Found along the way:** the history API sent Pacific wall-clock time labelled as UTC. `GetScanRequestsFromDb` and `GetScansFromDb` converted with `AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles'`, so a scan started at 16:52 UTC came back as `09:52Z`, and `GetScansFromDb` mixed that with an unconverted `scan_end_time`. Both now use `AT TIME ZONE 'UTC'` alone and return the real instant, which the UI formats in the viewer's time zone. Verified against the local database: scan 10 (16:52 UTC) shows as 9:52 AM in a Pacific-time browser.
  - *PR #14 review:* reading the columns `AT TIME ZONE 'UTC'` still assumed the database session's zone was UTC, because they were `TIMESTAMP` filled with `current_timestamp`. A startup migration now converts `created_on`, `scan_start_time`, `scan_end_time` and `completed_at` to `timestamptz` (`USING col AT TIME ZONE 'UTC'`), and the queries read them as they are. It only converts columns that are still `timestamp`, so it runs once. Verified on a copy of the local database: with the database's zone set to `America/New_York`, scan 10 comes back as `12:52-04:00`, the same instant. The migration reads existing values as UTC, which is only right if the database's zone was UTC when they were written. *Checked on production 2026-09-24 (read-only):* `SHOW timezone` is `Etc/UTC`, from `postgresql.conf`, with no per-database or per-role override. The database's `localtimestamp` matched real UTC to the second, and the three production scans are still `timestamp without time zone`, so the migration will convert them correctly. Other tables' `TIMESTAMP` columns (e.g. `messagemetadata.date`) are unchanged.

- [x] **4.4 Missing empty and error states** — `src/routes/requests.tsx`
  Show "No scans for this account" when the list is empty, and surface errors from the scan-requests query.
  *Done in #14:* also "Loading scans…" while loading, and the accounts error now includes its message. A failing request shows its error after TanStack Query's default 3 retries (about 7 seconds).

- [x] **4.5 Styling inconsistencies**
  - Dark mode is half done: the header and tables have `dark:` classes, the page body and form don't.
  - The progress table header uses `<td scope="col">`; it should be `<th>` (`ScanProgress.tsx:30-44`).
  - Typo: "Elapsted" (`ScanProgress.tsx:34`).
  - Long input and table class strings are duplicated. Extract small `Input` / `Table` components.

  *Done in #14:* the body gets light and dark colours and `color-scheme: light dark` (so native controls follow), and container borders have dark variants. The progress headers are `<th scope="col">`, and the typo is fixed. `components/Table.tsx` (`Table`, `Tr`, `Td`) and `components/Input.tsx` replace the duplicated class strings.

- [x] **4.6 Leftover scaffolding**
  `index.html` still uses the Vite default icon, and `package.json` is still named `"react"`.
  *Done in #14:* `public/favicon.svg` (a small drive icon) replaces `vite.svg`, and the package is `bhandaar-ui`.

## 5. Code health

- [ ] **5.1** Delete the empty `src/App.css` and the unused types in `src/types/optionals.ts`, or start using them.
  *Correction (2026-09-24):* `App.css` isn't empty. It holds the Tailwind import, and since #14 the base theme styles, so keep it. Only `optionals.ts` is left.
- [x] **5.2** Remove the leftover `console.log` calls in `src/api/index.ts`, `src/components/hooks/useSse.ts` and `src/routes/request.tsx`.
  *Done:* the ones in `api/index.ts` and the request page's error handling went in #12 and #13; the rest in #14. `src/` has no `console` calls left.
- [x] **5.3** Add a `typecheck` script (`tsc -b`) and run lint and typecheck in CI.
  *Done in #14:* the UI workflow's new `check` job runs `npm ci`, `npm run lint` and `npm run typecheck` with Node from `ui/.nvmrc`, and the image job `needs` it.
- [x] **5.4** Add tests (there are none):
  - Vitest for the pure functions: filter builder, date formatting, `fetchJson`.
  - React Testing Library for the request form.
  - Moving the filter logic out of the component makes it testable on its own.

  *Done in #16:*
  - **Setup:** Vitest 5 with jsdom, run by `npm test`. The test config fixes the two required `VITE_*` values, so tests ignore local `.env` files. The filter logic moved to `src/gmailFilter.ts`.
  - **36 unit tests:**
    - Filter builder: all options, next-day `before:`, and month/year/leap/DST boundaries, in three non-UTC zones.
    - Duration and date formatting, including the same instant at different offsets.
    - `fetchJson`, through the API functions: text, JSON and HTML errors, and no `[object Object]`.
    - OAuth state helpers.
  - **8 request-form tests** in `src/test/`, rendered through the real route tree: validation, the filter, labels, one POST with Submit disabled while pending, error replacing success, and the Google link's state.
  - Reintroducing earlier bugs fails the matching tests: Submit not disabled, a label on the wrong input, and a trailing space.
  - CI's `check` job runs `npm test`.

- [x] **5.5 The Gmail filter ends with a space** — `src/routes/request.tsx`
  Raised in review after #13: every term was appended with a trailing space, which was stored as `search_filter` and shown in history.
  *Done in #14:* `buildGmailFilter` joins an array of terms with single spaces.

---

## Suggested order

Merge order for the split PRs: #7, then #8 (stacked on it); #9 independently; this doc last.

Section 1 is done in #12, and section 3 (except 3.7) in #13. 2.5, 2.6, 7.7 and 7.8 are done and deployed.

1. **7.1–7.4** OAuth account linking in the backend: the handler continuing after a failed token request, the secret in the query string, the open redirect (7.3 allowlist) and the lost 400. Optionally move the OAuth `state` from the browser (1.1) to the backend in the same change.
2. **4.1** Results view (next feature).
3. Everything else, as convenient.

---

## 6. Dependencies

- [x] **6.1 Upgrade within current major versions** — done in #7
  React 19.0 → 19.3, TanStack Query 5.66 → 5.103, TanStack Router 1.111 → 1.170, Tailwind 4.0 → 4.3, Vite 6.1 → 6.4, typescript-eslint 8.24 → 8.70. `npm audit`: 19 → 0.

- [x] **6.2 Major-version upgrades (deferred; do as separate changes)**
  - Vite 6 → 8 and `@vitejs/plugin-react-swc` 3 → 4: the bundler underneath changes.
  - ESLint 9 → 10 and `eslint-plugin-react-hooks` 5 → 7: the new plugin adds React Compiler rules that will flag more code.
  - TypeScript 5.7 → 7: the compiler has been rewritten.
  - `globals` 15 → 17, `eslint-plugin-react-refresh` 0.4 → 0.5.

  *Done in #15, one commit per group, except TypeScript 7 (see 6.3):*
  - **Vite 8, plugin-react-swc 4:** no config changes needed. The Tailwind and TanStack Router plugins already accept Vite 8, and Node 22.23 meets its `>=22.12`. The build takes about 0.4 s instead of 1.5 s, and the main chunk is 315 kB instead of 339 kB. The env check still fails a misconfigured build, the devtools stay out of `dist`, route components hot-update with state kept, and the Docker image builds.
  - **ESLint 10, react-hooks 7:** the recommended set grows from 2 rules to 16, adding the React Compiler checks. The existing code passes them all, and a probe file confirmed they fire.
  - **TypeScript 6.0.3:** no config changes needed. 6.0 no longer auto-includes `@types/*` packages (`types` defaults to `[]`), which doesn't affect this app.
  - **globals 17, react-refresh 0.5:** the rule now comes from the plugin's Vite preset, at `error`. 0.5 also reports local components in files with other exports, which in TanStack route files is a false positive (autoCodeSplitting handles fast refresh), so the rule is off for `src/routes/**`.

- [ ] **6.3 TypeScript 7** — blocked
  Split out of 6.2 on 2026-09-24. The `typescript@7` package no longer exposes the compiler API (its root export is only the version; the rest is under `unstable/*`), and every `typescript-eslint` release, including canary, requires `typescript <6.1.0`. Upgrading would break `npm run lint` and the CI `check` job.
  *When unblocked:* upgrade once `typescript-eslint` supports TS 7. Running TS 7 for `tsc` alongside TS 6 for linting was considered and rejected: it means two compilers that may disagree.

---

## 7. Backend (`be/`)

Found on 2026-09-24 while setting up and testing the local environment. These are outside the original `ui/` scope. Items 7.1–7.4 are in OAuth account linking (`be/web/oauth.go`) and sit on the same flow as items 1.1–1.3, so fix them together. Items 7.5–7.6 came from the first end-to-end scan (scan 1), and 7.7–7.10 from debugging missing scan progress on the dev endpoint.

- [ ] **7.1 Handler continues after the token request fails** — `be/web/oauth.go:68-73`
  When `httpClient.Do` returns an error, the handler logs it and writes a 500 but doesn't `return`. The next line calls `defer res.Body.Close()` on a nil `res`, so the handler panics, and the recovered panic surfaces as a broken response.
  *Fix:* `http.Error(w, ..., http.StatusBadGateway); return`. Also check `res.StatusCode` before decoding.

- [ ] **7.2 Token exchange sends the secret in the query string, unencoded** — `be/web/oauth.go:53-54`
  The client secret, code and `redirect_uri` are joined into the URL with `fmt.Sprintf` and sent as a POST with no body. Query strings end up in proxy and server logs, and a value containing `&`, `+` or `/` corrupts the request. Google's token endpoint expects a form body.
  *Fix:* send the fields form-encoded with `http.PostForm(googleTokenUrl, url.Values{...})`, or use `golang.org/x/oauth2`'s `Config.Exchange`.

- [ ] **7.3 The final redirect goes wherever the caller says** — `be/web/oauth.go:28`, `:110-121`
  The redirect target is built from the caller-supplied `redirectUri` (`scheme://host/request`). A crafted link could send a user to any site after a real Google login: an open redirect. It also combines with the unchecked `state` (1.1).
  *Fix:* redirect only to an allowlist, e.g. `constants.FrontendUrl`, and reject any `redirectUri` whose origin isn't in it. The same allowlist can check `state` for 1.1.

- [ ] **7.4 The 400 for a missing `redirectUri` is lost** — `be/web/oauth.go:30-33`
  `w.Write` is called before `w.WriteHeader(http.StatusBadRequest)`, so Go has already sent a 200 and the 400 is ignored. The same pattern appears at `:71` and `:79`, where a bare `WriteHeader` sends no body.
  *Fix:* use `http.Error(w, "redirectUri not found in request", http.StatusBadRequest)`.

- [ ] **7.5 `completed_at` is never set** — `be/db/database.go:549-590`
  `MarkScanCompleted` and `MarkScanFailed` set `scan_end_time` and `status` but not `completed_at`. So scan 1 has `status = Completed` with a null `completed_at`, even though the log says "Scan marked as completed". `GetScan` (`:597`) reads the column back, so anything that relies on it sees no completion time.
  *Fix:* add `completed_at = current_timestamp` to both updates. If `completed_at` duplicates `scan_end_time`, drop one of the two columns.

- [ ] **7.6 Progress events never carry `completion_pct` or `eta_in_sec`** — **Deferred** — `be/collect/gmail.go:255-280`
  `logProgress` fills in the counts and elapsed time but leaves `CompletionPct` and `EtaInSec` at zero. Even the final event after scan 1 finished reported 0%. The UI shows the ETA column (always 0), and 4.3 plans a progress bar that would need these values.
  *Fix:* compute them from processed / (processed + pending), or send an explicit `done` flag with the final event. Otherwise drop both fields and the UI's ETA column.
  **Deferred (2026-09-24): non-trivial.** The total isn't known up front. `startGmailScan` (`:147-186`) lists messages page by page (`Messages.List(...).Q(filter)` + `NextPageToken`) while earlier pages are already being fetched, and `counter_pending` only grows as each page arrives (`:181`). The alternatives each have a catch:
  - Gmail's `resultSizeEstimate` is too rough for filtered queries.
  - Listing every page before fetching anything restructures the scan, costs extra quota and delays the first fetch.
  - `Labels.Get` counts are exact, but only for a single folder with no filter.

  *When revisited:* send a `listing_complete` flag. Before it's set, show "processed N, M queued". After it, processed + pending is the exact total, so a percentage (and an ETA from the processing rate) becomes meaningful. Until then, hide the always-0 ETA column in the UI (ties to 4.3). *Done in #14:* the ETA column is gone, and the progress bar is indeterminate until `completion_pct` is non-zero.

- [x] **7.7 Progress hub delivers each event to only one client, and can hang scans** — `be/notification/hub.go`, `be/web/sse.go`
  Found 2026-09-24: the dev endpoint showed no scan progress while `sm` did. `GetSubscriber("all")` gave every `/sse/scanprogress` connection the *same* unbuffered channel, so the connections competed for events and each event reached exactly one of them. Dev had two long-lived connections (probably two tabs) and lost the race. StrictMode's extra dev-mode connection can do the same. Worse, `pushToSubscriber` was a blocking send into that shared channel, which is created by the first connection ever and never removed. So a scan running while no tab is connected blocks in `logProgress`; `startGmailScan` then blocks at `done <- true` while holding the global `lock`, and every later Gmail scan waits on that lock. Production's image has the same hub (unchanged since `ce4e286`). A read-only check on 2026-09-24 found no stuck scans there (3 Gmail scans, all finished in ≤5.3s), so the hang hadn't happened yet.
  *Done (2026-09-24):*
  - `Subscribe(key)` gives each connection its own buffered channel (16 updates) in a per-key set, plus an `unsubscribe` func. The SSE handler calls it on disconnect.
  - `broadcast` does a non-blocking send to every subscriber and logs "Dropping progress update for slow subscriber" when a buffer is full. Publishing never blocks a scan.
  - Channels are closed exactly once, by whoever removes them from the set, under the hub lock.
  - `GetPublisher` makes a fresh channel per scan, which removes a race where a new scan could get the previous scan's already-closed channel.
  - Removed the unused `ClosePublisher`.
  - `sse.go`: after writing the `close` event on a closed channel, the handler now flushes and returns, instead of also sending an empty progress event and spinning on the closed channel.
  *Deployed to production 2026-09-24* (backend image built from `main` after #9).

  *Tests:* `be/notification/hub_test.go` is the repo's first test file. It covers every subscriber receiving every update, a non-reading subscriber not blocking, no subscribers, publisher close closing only its own key's subscribers, idempotent unsubscribe, and concurrent subscribe/publish. It passes with `go test -race -count=10` (run in `golang:1.25`, since this box has no C compiler for cgo).
  *Live check on `dev`:* two `curl` clients both received both events of scan 4. With zero clients connected, after one had connected and left (the old hang condition), scans 7 and 8 ran back to back and both completed.

- [x] **7.8 A failed message-list call crashes the whole server** — `be/collect/gmail.go:161-176`, `:98`, `:250`
  Found 2026-09-24 while testing 7.7: three scans at once exceeded Gmail's per-user rate limit (`rateLimitExceeded`, "Units per minute per user"). Scan 5's `Messages.List` ran out of retries, and `startGmailScan` returned early without `wg.Wait()`. The caller's `defer close(messageMetaData)` then closed the channel while `getMessageInfo` goroutines from earlier pages were still running. The next `messageMetaData <- md` hit `panic: send on closed channel`, which killed the process and every other scan. This is independent of the hub change, and production has the same code.
  *Fix:* on every early return, `wg.Wait()` before returning, or cancel in-flight fetches with a `context`. Also, don't run scans concurrently against the same Gmail quota: `lock` serialises `startGmailScan`, but the per-message fetches of a finished list call keep running.
  *Done (2026-09-24):* the paging loop moved into `listMessages()`, and `startGmailScan` now always runs `wg.Wait()`, then `done <- true` and `ticker.Stop()`, before returning, success or failure. This mirrors what `startPhotosScan` already did. Drive fetches synchronously and wasn't affected. Retries in `getMessageInfo` `wg.Add(1)` before their deferred `Done`, so the wait covers them. Context cancellation wasn't added: in-flight fetches finish within their retry budget (3 × 1s).
  *Regression test:* `be/collect/gmail_test.go` points a real `gmail.Service` at an `httptest` fake. Page 1 lists 5 messages with slow fetches, and page 2 fails with a non-retryable 400. Against the old code it fails with `startGmailScan returned with 0 of 5 fetches done`, then `panic: send on closed channel`, the production crash. With the fix it passes.
  *Found and fixed along the way:*
  - **Data race on the package-level `start`:** a scan's `start = time.Now()` raced with the previous scan's `logProgress` reading it for its final event. The race detector flagged it in the new test. `start` is now a parameter of `logProgress`.
  - **Photos progress showed the wrong elapsed time:** `startPhotosScan` never set `start`, so it reported time since the last *Gmail* scan. Fixed by the same change.
  - **No test could import `constants`:** `constants`' `init()` called `flag.Parse()` before the test binary registers `-test.*` flags. First worked around with a `testing.Testing()` check; replaced by the proper fix in 7.14.
  *Deployed to production 2026-09-24* (backend image built from `main` after #9).

  *Verified:* `go test -race -count=10 ./...` passes (in `golang:1.25`). Live scan 9 on `dev` completed with correct progress events and no panics. *Not reproduced live:* the rate-limit crash itself, since that needs Gmail's quota exhausted; the fake-server test covers it. The pre-existing `gofmt` drift in `be/db/database.go` was left alone.

- [-] **7.9 Later scans silently skip messages that earlier scans already saved** — `be/db/database.go:151-164` — **Won't fix (by design)**
  `SaveMessageMetadataToDb` skips any message whose `(username, message_id, thread_id)` already exists, *across all scans*. So scan 3 (295 processed) stored 207, which is 295 minus the 88 already saved by scans 1 and 2. Scan 4 re-ran scan 3's filter and stored 0, so `/api/gmaildata/4` returns nothing. This may be intentional deduplication, but per-scan results are incomplete and depend on scan order.
  *Decide:* either store one row per `(scan_id, message)`, so each scan's results are complete (dedupe in queries instead), or keep global dedupe and make the UI say "N new messages" instead of showing a per-scan listing.
  **Decision (2026-09-24): keep global dedupe.** `messagemetadata` holds each message once per user, attributed to the scan that first saw it. A scan's `scan_id` rows are the messages *new* in that scan, not everything it matched. Anything showing per-scan results (e.g. the results view in 4.1) should label them "new messages" and use the scan's processed count for the total matched.

- [ ] **7.10 Every new scan shows `Completed` while it's still running** — `be/db/database.go:103-107`, `:672-674`
  *Partly addressed in #14:* scans that fail to start, and scans left open by a crash or restart (like scan 6), are now marked `Failed`. A running scan still reads `Completed` until it ends.
  The migration adds `status VARCHAR(50) DEFAULT 'Completed'` (so existing rows count as completed), and `LogStartScan` never sets `status`. So a scan is `Completed` from the moment it's created, until it's marked `Failed`. Scan 6 never ran, because the server crashed first (7.8), but its row still says `Completed`, with no end time.
  *Fix:* have `LogStartScan` insert `status = 'Running'` (or `'Pending'`). With 7.5 (`completed_at`), the status, end time and completion time then agree.

- [x] **7.11 A full subscriber buffer dropped the newest progress update** — `be/notification/hub.go` (`broadcast`)
  Raised in PR #6 review, on the 7.7 hub: when a subscriber's buffer was full, `broadcast` discarded the *incoming* update. Progress events are cumulative snapshots, so the newest is the valuable one. A lagging client could miss a scan's final event and show stale counts indefinitely.
  *Done (2026-09-24):* on a full buffer, `broadcast` now discards the oldest queued snapshot and enqueues the new one, and warns only if that still fails. New test `TestFullBufferKeepsNewestUpdate`: it publishes 48 updates to a non-reading subscriber and expects the buffer to end on update 48. It fails on the previous hub (20/20 runs) and passes with the fix.
  *Test hygiene:* hub tests now wait for the hub to finish with their publisher (`closeAndWait`) before returning. Before, a late broadcast to `NOTIFICATION_ALL` could leak into the next test under `-count=N`, which made `TestEverySubscriberReceivesEveryUpdate` flaky at high counts. The suite passes `-count=300`, and `-race -count=20`.

- [x] **7.12 Skipped messages stayed counted as active** — `be/collect/gmail.go` (`getMessageInfo`)
  Raised in PR #6 review (it predates the PR): the permanent-failure path logged "skipping" and returned without `counter_pending.Add(-1)`, so a scan that skipped any message ended with `ActiveCount > 0` in its final progress event.
  *Done (2026-09-24):* that path now decrements `counter_pending`. The retry path doesn't, because the retried fetch still owns the message. New test `TestSkippedMessagesAreNotLeftActive`: a fake Gmail API returns 404 for one of 3 messages. On the old code the final event showed `processed=2 active=1`; with the fix it's `processed=2 active=0`.

- [x] **7.13 A client key of `"all"` collided with the all-scans subscription** — `be/notification/hub.go`
  Raised in PR #6 review: all-scans subscribers were stored under the magic key `NOTIFICATION_ALL = "all"` in the same map as per-scan subscribers. A scan whose client key was `"all"` would be broadcast twice to every SSE client, and its cleanup would close every SSE client's channel.
  *Done in #9:* all-scans subscribers live in their own set, reached through a new `SubscribeAll()`. `Subscribe(key)` is per-scan only, and `NOTIFICATION_ALL` is gone. No key, including `"all"`, can reach or close them. Test `TestClientKeyAllDoesNotCollideWithSubscribeAll` fails on the previous hub ("update delivered twice") and passes now.

- [x] **7.14 `flag.Parse()` ran inside `constants`' `init()`** — `be/constants/constants.go`, `be/collect/{gmail,drive,photos}.go`, `be/main.go`
  Raised in PR #6 review: the collectors' `init()` copied the OAuth flag values into their `oauth2.Config`s, so flags had to be parsed during package initialisation. That broke `go test` for any package importing `constants`, and the first workaround imported `testing` into production code.
  *Done in #9:* `gmailConfig`, `cloudConfig` and `photosConfig` are built on first use (`sync.OnceValue`). `constants` only registers the flags, and `main` calls `flag.Parse()`. Verified live: the CORS header still reflects `-frontend_url`, and a Gmail scan authenticates with the lazily built config.

---

## Local dev environment

Set up on 2026-09-23. It exists only on the developer machine and is not in the repo, except for `ui/.nvmrc`.

- **Node:** 22 via nvm (`cd ui && nvm use`).
- **Postgres:** Docker container `postgres` (image `postgres:17`, port 5432, volume `bhandaar-pgdata`). Restart with `docker start postgres`.
- **Backend:** there's no `.env` loader, and `be/.env` isn't gitignored, so pass the settings as environment variables:
  ```bash
  cd be && DB_HOST=localhost DB_USER=postgres DB_PASSWORD=postgres DB_NAME=postgres \
    go run . -frontend_url=http://localhost:5173
  ```
  OAuth linking also needs real `-oauth_client_id` and `-oauth_client_secret` values; both default to `dummy`.
- **UI:** `cd ui && npm run dev` (port 5173) calls the local backend at `http://localhost:8090` (from `ui/.env.development`). The backend's CORS default (`-frontend_url=http://localhost:5173`) already allows it.
- **Remote access (`<dev-endpoint>`):** set up 2026-09-24. The prod nginx block is committed in `<prod-repo>` as `8fb8cd9`. Verified the same day: Google account linking and a Gmail scan (scan 1: 40 messages) both worked end to end through it. The production nginx on <prod-host> (`<prod-repo>/nginx`) proxies `/api` and `/sse` to `<dev-host>:8090` and everything else to `<dev-host>:5173`, behind the same access control as `sm.jkurapati.com`. The Let's Encrypt certificate was issued via the certbot container (webroot) and renews like the others. To use it, put `VITE_BACKEND_URL=<dev-endpoint>` and `DEV_PUBLIC_HOST=<dev-endpoint>` in `ui/.env.development.local` (gitignored). `DEV_PUBLIC_HOST` makes `vite.config.ts` listen on all interfaces, accept only that `Host`, and run live reload over `wss` on port 443. Start the backend with `-frontend_url=<dev-endpoint>`. Delete the `.local` file to go back to plain localhost.
- **Local OAuth linking:** Google only redirects to registered URIs. As of 2026-09-24 the OAuth client allows `http://localhost:5173/oauth/glink` (Vite dev), `http://localhost:8080/oauth/glink`, `http://localhost:8090/oauth/glink` and `https://sm.jkurapati.com/oauth/glink`, so the dev server works without changes. The UI builds the redirect URI from the page's own origin (`window.location`), so a UI served from any other host or port will be rejected by Google.
