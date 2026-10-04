# Remote Sync: CI, Build and Release

**Status:** implemented. This is the compact as-built reference; the workflow files themselves are the source of truth. The original spec, with the workflows written out, is in [`archive/remote-sync/remote-sync-ci.md`](../archive/remote-sync/remote-sync-ci.md). The overview is [`remote-sync.md`](remote-sync.md).

| Workflow | Builds | Publishes |
|---|---|---|
| `.github/workflows/agentserver-docker-image.yml` | The `agentserver` image | Docker Hub `jyothri/bhandaar-agentserver` (`:latest` plus a per-commit tag), on merge to `main` |
| `.github/workflows/driveagent.yml` | `driveagent` for `linux/amd64`, `darwin/amd64`, `darwin/arm64` | A GitHub Release `driveagent/v<Version>`, on merge to `main` when the agent changed |

Both run their tests on pull requests and never publish from one.

## Server: `agentserver-docker-image.yml`

- **Triggers** on `agent/server/**`, `agent/wire/**` and the workflow file.
- **`test`**: a Postgres service container (`AGENTSERVER_TEST_DB`; each store test gets its own schema), then `go test -race` in `agent/wire` (a separate module, so it needs its own step), `go vet` and `go test -race` in `agent/server`.
- **Image**: built only if `test` passes, and pushed only on `main` (production runs `:latest`, so a PR must never push it). It uses the existing `DOCKER_USERNAME`/`DOCKER_PASSWORD` secrets.
- **`agent/server/build/Dockerfile`**: the build context is the repo root, so `agent/wire` is there for the `replace`, and `Dockerfile.dockerignore` (read by BuildKit) keeps everything but `agent/server/` and `agent/wire/` out. The Go stage is pinned to `agent/server/go.mod`'s toolchain. The runtime stage is Alpine, with the binary on `PATH` and a non-root user, so `docker exec -it <container> agentserver user add …` works.
- **Deploying** is by hand on the prod box: pull `:latest` and restart the service ([server spec](remote-sync-server.md#deployment)).

## Agent: `driveagent.yml`

- **Versioning.** `Version` in `agent/client/internal/version/version.go` is a constant, bumped by hand in each PR that changes the agent, so local builds report a real version. The tag is `driveagent/v<Version>`. CI stamps `version.Commit` (short SHA) with `-ldflags`; `driveagent version` prints `driveagent 0.4.1 (1e38c16), protocols [1]`.
- **Release-relevant files:** everything under `agent/client/` and `agent/wire/` except Markdown, `_test.go`, `testdata/` and `agent/client/scripts/`. A `wire` change needs an agent release. Other changes run CI without a bump or a release.
- **Jobs:**
  - `test`: `go vet` and `go test -race` in `agent/client`.
  - [`version-check`](#version-check).
  - `build`: the three targets, `CGO_ENABLED=0` (pure-Go SQLite, cross-compiled on `ubuntu-latest`), each a tarball `driveagent_<os>_<arch>.tar.gz` with the binary and `README.md`, kept as workflow artifacts (so a PR's build can be downloaded before release).
  - `release`: only on `main` when `version-check` says so, `gh release create` with the tarballs and `SHA256SUMS`, marked latest. It creates the tag itself (`GITHUB_TOKEN`, `contents: write`).
- **Branch protection:** `test`, `version-check` and `build` should be required checks on `main`.

### `version-check`

`agent/client/scripts/version-check.sh` (tests in `version-check_test.sh`; runnable locally with `EVENT` set) reads `Version`, checks it's `MAJOR.MINOR.PATCH`, and compares it with the highest `driveagent/v*` tag:

| Event | Release-relevant files changed? | Outcome |
|---|---|---|
| Pull request | Yes, against the base | **Fail** unless `Version` is above the latest tag |
| Pull request | No | Pass, no release |
| Push to `main` or manual run | The tag for `Version` doesn't exist | Release |
| Push to `main` or manual run | The tag exists, and relevant files changed since | **Fail**: a bump was skipped or doubled; fix with a follow-up bump |
| Push to `main` or manual run | The tag exists, nothing relevant changed | Pass, no release |

### Signing

The `release` job runs in the `driveagent-release` GitHub environment (deployments from `main` only; each run waits for the owner's approval), which holds `DRIVEAGENT_SIGNING_KEY`, an Ed25519 private key (PEM, base64). It signs `driveagent/v<V>\n` followed by `SHA256SUMS` into `SHA256SUMS.sig`, checks that signature with the public keys built into driveagent (`internal/update/keys.go`, via `go run ./scripts/verifysig`), and uploads it with the release. driveagent's [self-update](agent-auto-update.md) installs only releases signed this way. The private key isn't kept anywhere else; if it's lost, make a new one and add its public key to `keys.go` (see [Signing in CI](agent-auto-update.md#signing-in-ci)).

### Installing a release

```bash
asset=driveagent_linux_amd64.tar.gz     # or driveagent_darwin_arm64.tar.gz, driveagent_darwin_amd64.tar.gz
cd "$(mktemp -d)"
curl -fsSLO "https://github.com/jyothri/bhandaar/releases/latest/download/$asset"
curl -fsSLO "https://github.com/jyothri/bhandaar/releases/latest/download/SHA256SUMS"
sha256sum --check --ignore-missing SHA256SUMS      # macOS: shasum -a 256 --check --ignore-missing SHA256SUMS
tar -xzf "$asset"
mkdir -p ~/.local/bin && install -m 0755 driveagent ~/.local/bin/
driveagent version
```

From 0.7.0 this is needed once per machine: driveagent then [updates itself](agent-auto-update.md). The repo is public, so no authentication is needed. Asset names carry no version, so `releases/latest/download/<asset>` is stable. The macOS binaries aren't signed or notarized: a `curl` download runs as-is, and a browser download needs `xattr -d com.apple.quarantine driveagent` once.

### Linking releases to the server's version check

After each release (approved in the `driveagent-release` environment), the user raises `AGENTSERVER_LATEST_AGENT_VERSION` in the prod `.env` and restarts `agentserver`. Older agents then get `upgrade_recommended`, and from 0.7.0 they update themselves to exactly that version. `AGENTSERVER_MIN_AGENT_VERSION` is raised only when a release is required. The handshake's `download_url` defaults to `…/releases/latest`.
