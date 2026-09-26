# Remote Sync: CI, Build and Release

**Status:** implemented. `agentserver-docker-image.yml` shipped in PR 1 (#22); `driveagent.yml`, `version-check.sh` and the install instructions in PR 2. The overview is in [`remote-sync.md`](remote-sync.md).

Two new GitHub Actions workflows, alongside the existing `backend-docker-image.yml` and `ui-docker-image.yml`:

| Workflow | Builds | Publishes |
|---|---|---|
| `agentserver-docker-image.yml` | The `agentserver` server image | Docker Hub `jyothri/bhandaar-agentserver`, on merge to `main`, exactly like `be` and `ui` |
| `driveagent.yml` | `driveagent` binaries for Linux amd64, macOS Intel and macOS Apple Silicon | A GitHub Release tagged `driveagent/v<version>`, created automatically on merge to `main` when the agent changed |

Both run their tests on pull requests, and never publish from a PR.

## Server: `agentserver-docker-image.yml`

This mirrors `backend-docker-image.yml`.

```yaml
name: Docker agentserver Image CI

on:
  push:
    branches: ["main"]
    paths:
      - "agent/server/**"
      - "agent/wire/**"             # the types shared with driveagent
      - ".github/workflows/agentserver-docker-image.yml"
  pull_request:
    branches: ["main"]
    paths:
      - "agent/server/**"
      - "agent/wire/**"
      - ".github/workflows/agentserver-docker-image.yml"

jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: postgres
          POSTGRES_PASSWORD: postgres
        ports: ["5432:5432"]
        options: >-
          --health-cmd pg_isready --health-interval 5s --health-timeout 5s --health-retries 10
    env:
      AGENTSERVER_TEST_DB: postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: agent/server/go.mod
          cache-dependency-path: agent/server/go.sum
      - name: Test wire module (standard library only)
        run: go test -race ./...
        working-directory: agent/wire
      - name: Vet
        run: go vet ./...
        working-directory: agent/server
      - name: Test server (with race detector, against Postgres)
        run: go test -race ./...
        working-directory: agent/server

  agentserver:
    # Don't build or push an image whose tests fail.
    needs: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Lowercase the repo name and username
        run: echo "REPO=${GITHUB_REPOSITORY,,}-agentserver" >>${GITHUB_ENV}
      - uses: mr-smithers-excellent/docker-build-push@v6
        name: Build Docker image (push to Docker Hub only on main)
        with:
          image: ${{ env.REPO }}
          addLatest: true
          # PRs build only; pushing :latest from a PR would ship unmerged code
          # to production, which runs :latest.
          pushImage: ${{ github.event_name == 'push' }}
          registry: docker.io
          dockerfile: ./agent/server/build/Dockerfile
          # Needed for agent/server/build/Dockerfile.dockerignore (the build context is the repo root).
          enableBuildKit: true
          username: ${{ secrets.DOCKER_USERNAME }}
          password: ${{ secrets.DOCKER_PASSWORD }}
```

- `wire` is a separate module (see [Shared wire module](remote-sync-server.md#shared-wire-module)), so `go test ./...` in `agent/server/` doesn't reach it; it gets its own step.
- The image name follows the existing pattern: `${repo}` for `be`, `${repo}-ui` for `ui`, and `${repo}-agentserver` here, giving `jyothri/bhandaar-agentserver`. Tags are the same as for `be`/`ui`: `:latest` plus the action's default per-commit tag. Prod runs `:latest`.
- It uses the existing `DOCKER_USERNAME` / `DOCKER_PASSWORD` secrets. There are no new secrets.

### `agent/server/build/Dockerfile`

The build context is the repo root, as for `be`, so `agent/wire` is available to the `replace` directive (the image keeps the same layout: `/src/agent/server` and `/src/agent/wire`). To keep the rest of the repo (`ui/node_modules`, `be/`, `agent/client`, `.git`) out of the context sent to Docker, add `agent/server/build/Dockerfile.dockerignore`, the same pattern `ui/` already uses. It ignores everything except `agent/server/` and `agent/wire/`. The workflow sets `enableBuildKit: true`, as the UI workflow does, since BuildKit is what reads a per-Dockerfile ignore file.

```dockerfile
FROM golang:<version from agent/server/go.mod>-alpine AS go-build
LABEL org.opencontainers.image.source=https://github.com/jyothri/bhandaar
WORKDIR /src/agent/server
COPY agent/server/go.mod agent/server/go.sum ./
COPY agent/wire/go.mod ../wire/
RUN go mod download
COPY agent/wire/ ../wire/
COPY agent/server/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/agentserver ./cmd/agentserver

FROM alpine
RUN apk --no-cache add ca-certificates && adduser -D -H agentserver
COPY --from=go-build /out/agentserver /usr/local/bin/agentserver
USER agentserver
EXPOSE 8091
ENTRYPOINT ["agentserver"]
CMD ["serve"]
```

The base image is Alpine, like `be`'s, so a shell is available for debugging. The binary is on `PATH` and runs as a non-root user. That makes the admin commands work as `docker exec -it <container> agentserver user add …`. The Go base-image tag is pinned to the `agent/server/go.mod` toolchain, not the floating `golang:alpine` that `be` uses, so the image is built with the Go version CI tested.

### Deploying

Unchanged from how `be`/`ui` are deployed today. After the image is pushed, the user pulls `jyothri/bhandaar-agentserver:latest` on the prod box and restarts the `agentserver` compose service. The compose and nginx changes are in the [server spec](remote-sync-server.md#deployment).

## Agent: `driveagent.yml`

### Versioning

- **The version is a constant**, `Version` in `agent/client/internal/version/version.go` (`const Version = "0.2.0"`), bumped by hand in the PR that changes the agent. Local `go build`s therefore report a real version, so the server's minimum-version check treats a dev build like the matching release.
- **Tags are derived from it:** `driveagent/v<Version>`, for example `driveagent/v0.2.0`. The `driveagent/` prefix keeps agent tags apart from the repo-wide `v0.1.0` tag (March 2025) and from any future component tags.
- **CI enforces the bump.** A PR that changes release-relevant files must set a `Version` higher than the latest `driveagent/v*` tag (see `version-check` below). The release job then creates exactly that tag.
- `-ldflags` stamps only `version.Commit` (the short SHA, `unknown` by default). `driveagent version` prints both values: `driveagent 0.2.0 (a1b2c3d), protocols [1]`.

**Release-relevant files** are everything under `agent/client/` except Markdown (`**.md`), tests (`**_test.go`), test fixtures (`**/testdata/**`) and scripts (`agent/client/scripts/**`), plus `agent/wire/**` except its tests and fixtures. A change to `wire` changes what the agent sends, so it needs an agent release too. Changes to tests, fixtures, scripts or the workflow file run CI but need no bump and produce no release. The workflow still triggers on them, so tests always run; only `version-check` treats them as not release-relevant.

### Targets

| GOOS/GOARCH | For | Asset |
|---|---|---|
| `linux/amd64` | The optiplex boxes | `driveagent_linux_amd64.tar.gz` |
| `darwin/amd64` | Intel Macs | `driveagent_darwin_amd64.tar.gz` |
| `darwin/arm64` | Apple Silicon Macs (M-series) | `driveagent_darwin_arm64.tar.gz` |

All are built with `CGO_ENABLED=0`: the SQLite driver (`modernc.org/sqlite`) is pure Go, so no C toolchain or macOS runner is needed; cross-compiling on `ubuntu-latest` works. (Verified 2026-09-25 against the current code with `golang:1.27.1`: all three targets build.) Each tarball holds the `driveagent` binary and `README.md`. (There's no repo-root `LICENSE`, only `be/LICENSE`, so none is bundled.) Asset names carry no version, so `releases/latest/download/<asset>` is a stable URL. The version is in the tag, the release title and `driveagent version`.

The module lives in `agent/client/` (it was `agent/linux/` until it started shipping for macOS too). The macOS specifics are under [Platforms](remote-sync-agent.md#platforms) in the agent spec.

### Workflow

```yaml
name: driveagent CI and release

on:
  push:
    branches: ["main"]
    paths:
      - "agent/client/**"
      - "!agent/client/**.md"
      - "agent/wire/**"
      - ".github/workflows/driveagent.yml"
  pull_request:
    branches: ["main"]
    paths:
      - "agent/client/**"
      - "!agent/client/**.md"
      - "agent/wire/**"
      - ".github/workflows/driveagent.yml"
  workflow_dispatch: {}          # re-run a failed release for the current main

concurrency:
  group: driveagent-${{ github.ref }}
  cancel-in-progress: false      # never cancel a release half way

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: agent/client/go.mod
          cache-dependency-path: agent/client/go.sum
      - run: go vet ./...
        working-directory: agent/client
      - name: Test (with race detector)
        run: go test -race ./...
        working-directory: agent/client

  version-check:
    runs-on: ubuntu-latest
    outputs:
      version: ${{ steps.v.outputs.version }}
      release: ${{ steps.v.outputs.release }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }             # tags and history
      - id: v
        run: ./agent/client/scripts/version-check.sh   # see "version-check" below
        env:
          EVENT: ${{ github.event_name }}
          BASE_SHA: ${{ github.event.pull_request.base.sha }}

  build:
    needs: [test, version-check]
    runs-on: ubuntu-latest
    strategy:
      matrix:
        target: [linux/amd64, darwin/amd64, darwin/arm64]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: agent/client/go.mod
          cache-dependency-path: agent/client/go.sum
      - name: Build
        working-directory: agent/client
        run: |
          os=${{ matrix.target }}; os=${os%/*}; arch=${{ matrix.target }}; arch=${arch#*/}
          mkdir -p dist/pkg
          GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
            -ldflags "-s -w -X github.com/jyothri/bhandaar/agent/client/internal/version.Commit=${GITHUB_SHA::7}" \
            -o dist/pkg/driveagent ./cmd/driveagent
          cp README.md dist/pkg/
          tar -C dist/pkg -czf dist/driveagent_${os}_${arch}.tar.gz .
      - uses: actions/upload-artifact@v4
        with:
          name: driveagent-${{ strategy.job-index }}
          path: agent/client/dist/*.tar.gz

  release:
    # Only on main, only when version-check says this commit should be released.
    if: github.event_name != 'pull_request' && needs.version-check.outputs.release == 'true'
    needs: [build, version-check]
    runs-on: ubuntu-latest
    permissions:
      contents: write                         # create the tag and the release
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/download-artifact@v4
        with: { path: dist, merge-multiple: true }
      - name: Checksums
        run: cd dist && sha256sum *.tar.gz > SHA256SUMS
      - name: Create tag and GitHub Release
        env:
          GH_TOKEN: ${{ github.token }}
          V: ${{ needs.version-check.outputs.version }}
        run: |
          prev=$(git tag --list 'driveagent/v*' --sort=-v:refname | head -1)
          notes=$(git log --no-merges --format='- %s (%h)' ${prev:+$prev..}HEAD -- agent/client agent/wire)
          gh release create "driveagent/v$V" dist/*.tar.gz dist/SHA256SUMS \
            --target "$GITHUB_SHA" --title "driveagent v$V" --notes "$notes" --latest
```

`gh release create` creates the tag at `--target` itself, so no separate `git push`, and no new secret, is needed: `GITHUB_TOKEN` with `contents: write` is enough. A tag created with `GITHUB_TOKEN` doesn't trigger further workflows, which is fine because nothing listens for tags.

### `version-check`

This is a small script, `agent/client/scripts/version-check.sh`, so it can also be run locally. It reads `Version` from `internal/version/version.go`, checks it's valid semver (`MAJOR.MINOR.PATCH`), and finds `latest` = the highest existing `driveagent/v*` tag (`git tag --sort=-v:refname`). Then:

| Event | Release-relevant files changed? | Outcome |
|---|---|---|
| Pull request | vs. the PR base, yes | **Fail** unless `Version > latest`: `agent changed but internal/version.Version (0.2.0) is not above the latest release driveagent/v0.2.0 — bump it`. Success sets `release=false` |
| Pull request | no (workflow file only) | Pass, `release=false` |
| Push to `main` / manual run | `driveagent/v$Version` doesn't exist | `release=true` |
| Push to `main` / manual run | tag exists, and release-relevant files changed between it and `HEAD` | **Fail**: two PRs bumped to the same version, or a bump was skipped. The fix is a follow-up PR that bumps `Version` |
| Push to `main` / manual run | tag exists, nothing relevant changed since | Pass, `release=false` (for example a workflow-only change, or a re-run after a successful release) |

The first release of a new agent needs no special case: no `driveagent/v*` tag exists yet, so any valid version is accepted.

**Branch protection (recommended):** make `test`, `version-check` and `build` required checks on `main`, as the `be`/`ui` checks would be. Then an unbumped agent change can't be merged.

### Installing a release

For the agent README, written as part of this work:

```bash
# Linux amd64 (use darwin_arm64 for Apple Silicon, darwin_amd64 for Intel Macs)
asset=driveagent_linux_amd64.tar.gz
curl -fsSLO "https://github.com/jyothri/bhandaar/releases/latest/download/$asset"
curl -fsSLO "https://github.com/jyothri/bhandaar/releases/latest/download/SHA256SUMS"
sha256sum --check --ignore-missing SHA256SUMS      # macOS: shasum -a 256 --check --ignore-missing SHA256SUMS
tar -xzf "$asset" driveagent && install -m 0755 driveagent ~/.local/bin/
driveagent version
```

The repo is public, so the downloads need no authentication. The agent releases are the repo's only GitHub Releases, and each is marked `--latest`, so `releases/latest` always means the newest agent.

**macOS:** the binaries are not code-signed or notarized. Downloaded with `curl` as above, they run as-is, because `curl` doesn't set the quarantine attribute. Downloaded through a browser, Gatekeeper blocks them until you run `xattr -d com.apple.quarantine driveagent`. Signing and notarizing would need a paid Apple Developer ID and a macOS CI step. It was decided on 2026-09-25 to leave that out of v1; the `curl`/`xattr` route is acceptable (see the non-goals in [`remote-sync.md`](remote-sync.md#non-goals-v1)).

### Linking releases to the server's version check

- The server's `AGENTSERVER_AGENT_DOWNLOAD_URL` defaults to `https://github.com/jyothri/bhandaar/releases/latest`, which the handshake returns with `upgrade_recommended` and `upgrade_required`.
- `AGENTSERVER_LATEST_AGENT_VERSION` and `AGENTSERVER_MIN_AGENT_VERSION` are set by hand in the prod `.env` after an agent release. Raise `LATEST` for every release, so older agents get a nudge. Raise `MIN` only when a release is required, for example for a protocol change. Automating this (the server reading the latest release from the GitHub API) is possible later, but not part of v1.

## Rollout placement

- `agentserver-docker-image.yml` and the Dockerfile ship with rollout step 1 (server skeleton).
- `driveagent.yml`, `internal/version`, `version-check.sh` and the install section of the README ship with rollout step 2 (agent identity). The first release is `driveagent/v0.1.0`, cut when step 2 merges.
- Make the new checks required in branch protection once each workflow has run green on `main`.
