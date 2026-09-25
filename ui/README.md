# Bhandaar UI

The web front end: a React + TypeScript single-page app built with Vite, using TanStack Router (file-based routes in `src/routes/`) and TanStack Query, styled with Tailwind CSS. It talks to the Go backend in `../be/`.

## Setup

Use the Node version in `.nvmrc` (`nvm use`), then:

```bash
npm ci
```

## Commands

| Command | What it does |
|---|---|
| `npm run dev` | Dev server on http://localhost:5173 |
| `npm test` | Vitest, once (`npm run test:watch` to watch) |
| `npm run typecheck` | `tsc -b` |
| `npm run lint` | ESLint |
| `npm run build` | Typecheck, then the production build into `dist/` |
| `npm run preview` | Serve `dist/` locally |

CI runs `npm ci`, lint, typecheck and tests before building the Docker image (`Dockerfile`).

## Configuration

Two build-time variables, checked at startup and when building (`src/config.ts`, `vite.config.ts`):

- `VITE_BACKEND_URL`: the backend's base URL.
- `VITE_GOOGLE_CLIENT_ID`: the Google OAuth client ID used for account linking.

`.env.development` (`npm run dev`) points at a local backend on `http://localhost:8090`, and `.env.production` at the production URL. Put local overrides in `.env.development.local` (gitignored). Setting `DEV_PUBLIC_HOST` there serves the dev server through an HTTPS reverse proxy; see [../docs/local-dev.md](../docs/local-dev.md).

## Tests

Unit tests sit next to their modules (`src/*.test.ts`). Route-level tests live in `src/test/`, because the router generator treats every file under `src/routes/` as a route. `src/test/renderRoute.tsx` renders a path through the real route tree. Tests that don't need a DOM opt into the node environment with a `// @vitest-environment node` comment.
