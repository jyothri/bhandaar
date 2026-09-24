# UI Review

- **Date:** 2026-09-23
- **Scope:** `ui/` (all 18 source/config files, ~900 lines); section 7 adds backend findings (`be/`) from local testing
- **Reviewed at:** `feature/driveagent-relocated-sidecar` (commit `7e93442`); `ui/` is identical on `main`
- **Baseline (run after dev setup):** `tsc -b` passes · `npm run build` passes · `npm run lint` reports 5 errors, 2 warnings (all covered by items 1.7, 3.1, 3.5 and `prefer-const` in `src/api/index.ts`)

Status legend: `[ ]` open · `[x]` done · `[-]` won't fix · **Deferred** = open, parked for now (reason in the item)

## Status

*Last updated: 2026-09-24*

| | Count |
|---|---|
| Open | 38 |
| Done | 2 |
| Won't fix | 0 |
| *of which Deferred* | 1 (7.6) |

### Change log

| Date | Commit | Change | Items |
|---|---|---|---|
| 2026-09-23 | `b3a81fc` | Review notes added | — |
| 2026-09-23 | `5d4f052` | UI dependencies upgraded within current majors: 19 advisories (10 high) → 0; tailwind moved to devDependencies; renamed router devtools/plugin APIs; router plugin moved before `plugin-react-swc` (dev server refused to start otherwise); Node 22 pinned via `ui/.nvmrc` | 6.1 |
| 2026-09-24 | `3cc9a7e` | Backend URL and Google client ID moved to Vite env vars (`src/config.ts`, `ui/.env.development`, `ui/.env.production`); dev server now uses the local backend; optional `DEV_PUBLIC_HOST` in `vite.config.ts` for serving the dev env via `<dev-endpoint>` | 2.1 |
| 2026-09-24 | `0b69c82` | Backend OAuth findings added as section 7; first end-to-end test via `<dev-endpoint>` (Google linking + scan 1) added 2.4, 7.5, 7.6; 1.9 corrected (`label:unread` works) | 7.1–7.6, 2.4 (new, open) |
| 2026-09-24 | *(uncommitted)* | 7.6 deferred: the total message count isn't known until Gmail listing finishes | 7.6 |

---

## 1. Bugs and security

- [ ] **1.1 OAuth `state` is a fixed placeholder and never checked** — `src/routes/request.tsx:129`
  Sends `state = "YOUR_CUSTOM_STATE"`; `be/web/oauth.go` has no `state` handling. This leaves account linking open to CSRF.
  *Fix:* generate `crypto.randomUUID()`, save it in `sessionStorage`, and compare it in `oauth/glink.tsx` before redirecting to the backend. Ideally the backend creates and checks it instead.

- [ ] **1.2 OAuth URLs are built without encoding** — `src/routes/oauth/glink.tsx:25`, `src/routes/request.tsx:132`
  `code`, `scope` and `redirect_uri` are pasted into the query string as-is. `scope` contains `:` and `/`, and a multi-scope list with spaces would break.
  *Fix:* build the URLs with `URLSearchParams`.

- [ ] **1.3 Redirect runs during render** — `src/routes/oauth/glink.tsx:26`
  `window.location.href = ...` in the render body runs twice under StrictMode, and again on any re-render, so the one-time auth code can be sent twice.
  *Fix:* redirect from the route's `beforeLoad` (`throw redirect({ href })`) or from a `useEffect`.

- [ ] **1.4 Errors become `"[object Object]"`, and `response.ok` is never checked** — `src/api/index.ts:23`, `:60`
  `throw new Error(content)` stringifies an object. A 500 with an HTML body fails later as a confusing JSON parse error.
  *Fix:* add one shared `fetchJson<T>()` helper that checks `ok` and throws `new Error(content.error ?? response.statusText)`.

- [ ] **1.5 Account key isn't URL-encoded** — `src/api/index.ts:56`
  *Fix:* wrap it in `encodeURIComponent(accountKey)`.

- [ ] **1.6 `ScanProgress` can render a stray "0"** — `src/components/ScanProgress.tsx:7`, `:21`
  `sseData.scan_id && (...)` renders `0` when the scan ID is 0, and it relies on `{} as Progress` being falsy.
  *Fix:* use `useState<Progress | null>(null)` and `if (!sseData) return null`.

- [ ] **1.7 Problems in the SSE hook** — `src/components/hooks/useSse.ts`
  - `setData` is missing from the effect's dependencies (line 39). It works only because the effect never re-reads the caller's closure.
  - Errors are cleared with `setError("")` instead of `null`.
  - `onerror` reports an error even while the browser is reconnecting on its own. Check `eventSource.readyState === EventSource.CLOSED` first.
  - The callback is typed `any`. Make the hook generic: `useSse<T>(..., onData: (d: T) => void)`.

- [ ] **1.8 Date round-trip does nothing and can shift the date** — `src/routes/request.tsx:76-83`
  Builds a local-time `Date` and then calls `toISOString()` (UTC). East of UTC this moves the date back one day.
  *Fix:* the `<input type="date">` value is already `YYYY-MM-DD`; store it as is.

- [ ] **1.9 Gmail filter syntax errors** — `src/routes/request.tsx:105-120`
  - ~~`label:unread` should be `is:unread`.~~ *Corrected 2026-09-24:* scan 1 used `label:inbox label:unread` and returned 40 unread inbox messages, so Gmail accepts `label:unread`. `is:unread` is the documented form, but switching is optional.
  - Gmail's `before:` is exclusive, so the chosen end date is left out. Add one day, or label the field to say so.
  - Stray double space after `after:` (line 114).

- [ ] **1.10 Three labels point at the wrong input** — `src/routes/request.tsx:174`, `:187`, `:200`
  The Inbox, Unread and Date range labels all use `htmlFor="filter"`, so clicking any of them focuses the query box.
  *Fix:* point them at `inbox`, `unread` and `datepicker-range-start`.

## 2. Configuration and deployment

- [x] **2.1 Backend URL and OAuth client ID are hard-coded** — `src/api/index.ts:4`, `src/routes/request.tsx:128`
  Local dev always talks to production.
  *Fix:* use `import.meta.env.VITE_BACKEND_URL` and `VITE_GOOGLE_CLIENT_ID`, and add a `.env.example`.
  *Done in `3cc9a7e`:* both values come from `src/config.ts`, which throws a clear error at startup if either is missing; they are typed in `src/vite-env.d.ts`. Instead of a `.env.example`, the per-mode files are committed: `ui/.env.development` (`http://localhost:8090`) and `ui/.env.production` (production URL, so the Docker build is unchanged). Neither value is secret. Local overrides go in `.env.development.local`, which is gitignored.

- [ ] **2.2 Router devtools are mounted in every build** — `src/routes/__root.tsx:20`
  *Fix:* gate them behind `import.meta.env.DEV`, or lazy-load them.

- [ ] **2.3 Dockerfile hardening** — `Dockerfile`
  - Pin the base image (e.g. `node:22-alpine` instead of `node:latest`).
  - Use `npm ci` instead of `npm install`.
  - Add an nginx SPA fallback (`try_files $uri /index.html`). Without it, a browser landing directly on `/oauth/glink` or `/requests` gets a 404, and the Google OAuth redirect lands on `/oauth/glink`. **High priority.**

- [ ] **2.4 nginx drops the progress stream every 60 seconds** — prod nginx `<prod-repo>/nginx/nginx/conf/nginx.conf`, `location /sse` in the `sm` and the dev endpoint blocks
  Seen 2026-09-24: progress-stream connections through the dev endpoint ended at exactly 1m0.01s and 1m3.0s. That's nginx's default `proxy_read_timeout` of 60s, which cuts the stream whenever no event arrives for a minute. The browser reconnects, but each drop makes the UI briefly show "Connection lost" (see 1.7). nginx may also buffer the stream and delay events.
  *Fix:* in both `/sse` locations, add `proxy_read_timeout 1h;`, `proxy_buffering off;` and `proxy_cache off;`. The `Upgrade`/`Connection` headers aren't needed for SSE. Optionally have the backend send a keep-alive comment every ~30s, so any proxy timeout stays harmless.

## 3. React / TanStack Query idioms

- [ ] **3.1 `queryFilter` is derived data kept in state** — `src/routes/request.tsx:101-120`
  A `useEffect` calls `setQueryFilter`, causing an extra render and an exhaustive-deps lint warning.
  *Fix:* compute it during render (`useMemo` optional).

- [ ] **3.2 Form state is spread across many `useState` calls** — `src/routes/request.tsx:19-25`
  Consider one form-state object, or a form library if more scan types are coming.

- [ ] **3.3 Use `enabled` instead of the `"none"` sentinel** — `src/routes/requests.tsx:23`, `src/api/index.ts:52`
  *Fix:* pass `enabled: selectedAccount !== "none"` and drop the special case from `getScanRequests`.

- [ ] **3.4 Invalidated query key doesn't exist** — `src/routes/request.tsx:36`
  It invalidates `["scans"]`, which no query uses. Because of `staleTime: Infinity`, the history page stays stale after a new scan.
  *Fix:* define keys in one place (e.g. `queryKeys.scanRequests(account)`) and invalidate `getScanRequests`.

- [ ] **3.5 Mutation errors are handled twice** — `src/routes/request.tsx:40`, `:63-68`
  `onError` duplicates the try/catch around `mutateAsync`.
  *Fix:* keep one, and use `isPending` to disable Submit so it can't be double-clicked.

- [ ] **3.6 Stale messages linger** — `src/routes/request.tsx`
  `infoMessage` is never cleared on error, so success and error text can show at the same time.

- [ ] **3.7 Username is read from the option's text** — `src/routes/request.tsx:73`
  *Fix:* look up the account in `accounts` by `clientKey`.

- [ ] **3.8 `Header` is outside `RouterProvider`** — `src/App.tsx:32`
  It can't use `Link` or router hooks.
  *Fix:* move it into `__root.tsx` alongside the nav.

## 4. UI / UX

- [ ] **4.1 The home route (`/`, "Data") is a placeholder** — `src/routes/index.tsx`
  `types/mail.ts` (`MessageMetadata`) is defined but unused, and the backend already serves `/api/gmaildata/{id}` and `/api/photos/{id}`.
  *Next feature:* a results view, linked from the history table by scan ID, with pagination and sizes.

- [ ] **4.2 Only Gmail scans can be requested**, although `ScanType` also lists Local, GDrive, GStorage and GPhotos.

- [ ] **4.3 Values are shown raw**
  - The progress table shows raw seconds and ignores `completion_pct`. Add a `<progress>` bar and mm:ss formatting.
  - History shows the raw `scan_start_time`. Format it with `Intl.DateTimeFormat`.

- [ ] **4.4 Missing empty and error states** — `src/routes/requests.tsx`
  Show "No scans for this account" when the list is empty, and surface errors from the scan-requests query.

- [ ] **4.5 Styling inconsistencies**
  - Dark mode is half done: the header and tables have `dark:` classes, the page body and form don't.
  - The progress table header uses `<td scope="col">`; it should be `<th>` (`ScanProgress.tsx:30-44`).
  - Typo: "Elapsted" (`ScanProgress.tsx:34`).
  - Long input and table class strings are duplicated. Extract small `Input` / `Table` components.

- [ ] **4.6 Leftover scaffolding**
  `index.html` still uses the Vite default icon, and `package.json` is still named `"react"`.

## 5. Code health

- [ ] **5.1** Delete the empty `src/App.css` and the unused types in `src/types/optionals.ts`, or start using them.
- [ ] **5.2** Remove the leftover `console.log` calls in `src/api/index.ts`, `src/components/hooks/useSse.ts` and `src/routes/request.tsx`.
- [ ] **5.3** Add a `typecheck` script (`tsc -b`) and run lint and typecheck in CI.
- [ ] **5.4** Add tests (there are none):
  - Vitest for the pure functions: filter builder, date formatting, `fetchJson`.
  - React Testing Library for the request form.
  - Moving the filter logic out of the component makes it testable on its own.

---

## Suggested order

1. **1.1** OAuth `state` (with **7.3** redirect allowlist, same backend change) and **2.3** SPA fallback in nginx: these decide whether account linking is safe and works at all.
2. **1.2–1.4** OAuth URL encoding, the render-time redirect, and error handling (`fetchJson`). Fix **7.1**, **7.2** and **7.4** in the same pass, since they are on the same flow.
3. **3.4** Query-key invalidation, plus the remaining items in section 1.
4. **4.1** Results view (next feature).
5. Everything else, as convenient.

---

## 6. Dependencies

- [x] **6.1 Upgrade within current major versions** — done in `5d4f052`
  React 19.0 → 19.3, TanStack Query 5.66 → 5.103, TanStack Router 1.111 → 1.170, Tailwind 4.0 → 4.3, Vite 6.1 → 6.4, typescript-eslint 8.24 → 8.70. `npm audit`: 19 → 0.

- [ ] **6.2 Major-version upgrades (deferred; do as separate changes)**
  - Vite 6 → 8 and `@vitejs/plugin-react-swc` 3 → 4: the bundler underneath changes.
  - ESLint 9 → 10 and `eslint-plugin-react-hooks` 5 → 7: the new plugin adds React Compiler rules that will flag more code.
  - TypeScript 5.7 → 7: the compiler has been rewritten.
  - `globals` 15 → 17, `eslint-plugin-react-refresh` 0.4 → 0.5.

---

## 7. Backend (`be/`)

Found on 2026-09-24 while setting up and testing the local environment. These are outside the original `ui/` scope. Items 7.1–7.4 are in OAuth account linking (`be/web/oauth.go`) and sit on the same flow as items 1.1–1.3, so fix them together. Items 7.5–7.6 came from the first end-to-end scan (scan 1).

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

  *When revisited:* send a `listing_complete` flag. Before it's set, show "processed N, M queued". After it, processed + pending is the exact total, so a percentage (and an ETA from the processing rate) becomes meaningful. Until then, hide the always-0 ETA column in the UI (ties to 4.3).

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
- **Remote access (`<dev-endpoint>`):** set up 2026-09-24. Verified the same day: Google account linking and a Gmail scan (scan 1: 40 messages) both worked end to end through it. The production nginx on <prod-host> (`<prod-repo>/nginx`) proxies `/api` and `/sse` to `<dev-host>:8090` and everything else to `<dev-host>:5173`, behind the same access control as `sm.jkurapati.com`. The Let's Encrypt certificate was issued via the certbot container (webroot) and renews like the others. To use it, put `VITE_BACKEND_URL=<dev-endpoint>` and `DEV_PUBLIC_HOST=<dev-endpoint>` in `ui/.env.development.local` (gitignored). `DEV_PUBLIC_HOST` makes `vite.config.ts` listen on all interfaces, accept only that `Host`, and run live reload over `wss` on port 443. Start the backend with `-frontend_url=<dev-endpoint>`. Delete the `.local` file to go back to plain localhost.
- **Local OAuth linking:** Google only redirects to registered URIs. As of 2026-09-24 the OAuth client allows `http://localhost:5173/oauth/glink` (Vite dev), `http://localhost:8080/oauth/glink`, `http://localhost:8090/oauth/glink` and `https://sm.jkurapati.com/oauth/glink`, so the dev server works without changes. The UI builds the redirect URI from the page's own origin (`window.location`), so a UI served from any other host or port will be rejected by Google.
