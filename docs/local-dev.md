# Local Development Environment

How the developer machine is set up to run Bhandaar locally, and to reach it through the dev endpoint. The general build and run commands are in [../CLAUDE.md](../CLAUDE.md).

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
