# UI Review

- **Date:** 2026-09-23
- **Scope:** `ui/` (all 18 source/config files, ~900 lines)
- **Reviewed at:** `feature/driveagent-relocated-sidecar` (commit `7e93442`); `ui/` is identical on `main`
- **Baseline (run after dev setup):** `tsc -b` passes · `npm run build` passes · `npm run lint` reports 5 errors, 2 warnings (all covered by items 1.7, 3.1, 3.5 and `prefer-const` in `src/api/index.ts`)

Status legend: `[ ]` open · `[x]` done · `[-]` won't fix

## Status

*Last updated: 2026-09-23*

| | Count |
|---|---|
| Open | 32 |
| Done | 1 |
| Won't fix | 0 |

### Change log

| Date | Commit | Change | Items |
|---|---|---|---|
| 2026-09-23 | `b3a81fc` | Review notes added | — |
| 2026-09-23 | `5d4f052` | UI dependencies upgraded within current majors: 19 advisories (10 high) → 0; tailwind moved to devDependencies; renamed router devtools/plugin APIs; router plugin moved before `plugin-react-swc` (dev server refused to start otherwise); Node 22 pinned via `ui/.nvmrc` | 6.1 |

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
  - `label:unread` should be `is:unread`.
  - Gmail's `before:` is exclusive, so the chosen end date is left out. Add one day, or label the field to say so.
  - Stray double space after `after:` (line 114).

- [ ] **1.10 Three labels point at the wrong input** — `src/routes/request.tsx:174`, `:187`, `:200`
  The Inbox, Unread and Date range labels all use `htmlFor="filter"`, so clicking any of them focuses the query box.
  *Fix:* point them at `inbox`, `unread` and `datepicker-range-start`.

## 2. Configuration and deployment

- [ ] **2.1 Backend URL and OAuth client ID are hard-coded** — `src/api/index.ts:4`, `src/routes/request.tsx:128`
  Local dev always talks to production.
  *Fix:* use `import.meta.env.VITE_BACKEND_URL` and `VITE_GOOGLE_CLIENT_ID`, and add a `.env.example`.

- [ ] **2.2 Router devtools are mounted in every build** — `src/routes/__root.tsx:20`
  *Fix:* gate them behind `import.meta.env.DEV`, or lazy-load them.

- [ ] **2.3 Dockerfile hardening** — `Dockerfile`
  - Pin the base image (e.g. `node:22-alpine` instead of `node:latest`).
  - Use `npm ci` instead of `npm install`.
  - Add an nginx SPA fallback (`try_files $uri /index.html`). Without it, a browser landing directly on `/oauth/glink` or `/requests` gets a 404, and the Google OAuth redirect lands on `/oauth/glink`. **High priority.**

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

1. **1.1** OAuth `state` and **2.3** SPA fallback in nginx: these decide whether account linking is safe and works at all.
2. **1.2–1.4** OAuth URL encoding, the render-time redirect, and error handling (`fetchJson`).
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
- **UI:** `cd ui && npm run dev`. Until **2.1** is fixed, the UI calls the production backend, not the local one.
