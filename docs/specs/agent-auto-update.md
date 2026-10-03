# driveagent: Updating Itself

**Status:** proposed. Written 2026-10-03, against driveagent 0.6.1.

## Problem

Every driveagent release has to be installed by hand on every machine: download the tarball, check it, unpack it, `install` it (see [Installing a release](remote-sync-ci.md#installing-a-release)). The server already knows which version is current: after each release, `AGENTSERVER_LATEST_AGENT_VERSION` is raised in the prod `.env`, and every handshake reports it. But an older agent only prints a note (`note: driveagent 0.6.1 is available …`), which a scan run from cron never shows anyone. So machines lag behind, and a release that matters, like 0.6.0's MD5 backfill for [duplicates](../archive/duplicates.md), reaches each machine only when someone remembers to install it.

## Goals

1. **Update on handshake.** When the server reports a newer version, driveagent downloads that exact release from GitHub, verifies it, replaces its own binary, and re-runs the command on the new version. This applies whether the server says `upgrade_recommended` or `upgrade_required`.
2. **Never mid-work.** The update happens before a command does anything, never during a scan or an upload.
3. **Only what we built.** A binary replaces driveagent only if it's the release the server named, its checksum matches, and the checksums are signed with the release key.
4. **The server controls rollout.** Agents move to the version in `AGENTSERVER_LATEST_AGENT_VERSION`, not to GitHub's latest. Publishing a release changes nothing until that's raised.
5. **Can be turned off.** `DRIVEAGENT_NO_AUTO_UPDATE=1` stops automatic updates. `driveagent update` still updates by hand.

Non-goals:
- Downgrades.
- Updating machines older than the first version with the updater; that one is installed by hand (see [Rollout](#rollout)).
- A daemon that updates in the background.
- Updating a binary the user can't write to, such as one installed by root (see [Where it can't update](#where-it-cant-update)).

## What exists

| Piece | Today |
|---|---|
| Handshake | `POST /agent/v1/handshake` answers `decision` (`ok`, `upgrade_recommended`, `upgrade_required`, `unsupported_protocol`), `min_agent_version`, `latest_agent_version` and `download_url`. The server needs no change for this spec |
| Releases | Merging a version bump to `main` creates the tag `driveagent/v<V>` and a GitHub Release with `driveagent_<os>_<arch>.tar.gz` for linux/amd64, darwin/amd64 and darwin/arm64, and `SHA256SUMS` ([`remote-sync-ci.md`](remote-sync-ci.md)). The repo is public |
| Commands that handshake | `scan`, `sync`, `login`, `remote-status`, through `preflight` (health, then handshake). `scan` runs it after taking its disk and upload locks |
| One version per machine | Every `driveagent` joins the instance lock first. While any is running, another **version** is refused with exit 5 ([agent hardening](../archive/agent-hardening.md#goal-1-the-instance-lock)) |
| Exit codes | 3 for the server unreachable, 4 for an upgrade, 5 for busy ([exit codes](remote-sync-agent.md#exit-codes)) |

The instance lock decides the design. The new version can't run while an old one is running, so a process updates **only when it's the only driveagent running**. Otherwise it carries on with the old version and leaves the update to a later run.

## When it updates

The update step, `selfUpdate`, runs in the commands that handshake: `scan`, `sync`, `login` and `remote-status`. It runs right after the command joins the instance and opens its remote settings, and **before** it takes any other lock, opens `state.db`, or reads a drive. For `scan`, the handshake therefore moves ahead of the disk lock and the upload lock. The later `preflight` reuses its answer rather than handshaking again.

`compare`, `report`, `logout`, `version` and `help` never update. `logout` handshakes only to revoke, and shouldn't be held up by a download.

Steps:

1. **Handshake.** Health, then handshake, as `preflight` does now. If the server is unreachable, the command fails as today (exit 3); there's nothing to update to.
2. **Decide.** Update only if all of these hold:
   - `latest_agent_version` is newer than `version.Version`.
   - This is a release build: `version.Commit` is set by CI's `-ldflags`. A local `go build` is never replaced.
   - `DRIVEAGENT_NO_AUTO_UPDATE` isn't set, or the user ran `driveagent update`.
   - `DRIVEAGENT_UPDATED_FROM` isn't set. The re-exec in step 6 sets it, so a new binary never updates again in the same run, whatever the server says.
   - This process is the only driveagent running (see [Alone](#alone)).
   - The last attempt at this version didn't fail less than an hour ago (see [Failures](#failures)).
3. **Download** the release named by `latest_agent_version` (see [Download and verify](#download-and-verify)).
4. **Verify** the signature, the checksum, and that the new binary runs and reports the expected version.
5. **Replace** the binary (see [Replacing the binary](#replacing-the-binary)).
6. **Re-exec.** Release the instance lock, then `execve` the new binary with the same arguments and environment, plus `DRIVEAGENT_UPDATED_FROM=<old version>`. The new process starts the command from the top: it joins the instance, takes its locks and handshakes, so nothing is carried across. One line goes to stderr: `updated driveagent 0.6.1 → 0.7.0`.

If the update doesn't happen, or fails:
- **`upgrade_recommended`:** the command carries on with the current version, printing why it didn't update (one line), as it prints the note today.
- **`upgrade_required`:** the command stops with exit 4, as today. The message says why it couldn't update, and gives the manual install commands.

### Alone

Each process holds `instance.lock` shared. To check that it's alone, `selfUpdate` takes `instance.gate`, then tries to **convert** its shared `instance.lock` to exclusive without waiting.
- **Got it:** no other driveagent is running. It keeps the lock exclusive through the download, replace and exec, so nothing can join meanwhile: a process that starts now waits at the gate (up to 10 s) and then, finding the lock busy, sees our `instance.json`. While updating, `instance.json` gains `"updating": true`, and a joiner that finds it waits for the gate instead of refusing.
- **Busy:** others are running. Skip the update for now.

This is the same gate-and-lock pattern as joining the instance, in `internal/runlock` (`TryAlone`). Nothing new is shared across processes. Two processes started at once both see each other and neither updates; the next run will.

Because the update finishes before `execve`, there's never a moment with two versions holding the instance lock.

### Long downloads and joiners

A joiner waits at most 10 s at the gate today. A download can take longer: the tarball is about 5 MB. So while `updating` is set, a joiner waits for the gate without that limit, printing once: `waiting for driveagent to finish updating itself`. Once the gate is free, it joins whatever version is now running. If that's the new version, the joiner's own version no longer matches, and it exits 5 with the usual message, which tells the user to re-run. Under cron, the next run is the new version anyway.

## Download and verify

URLs, for version `V`, OS `GOOS` and architecture `GOARCH`:

```
https://github.com/jyothri/bhandaar/releases/download/driveagent/v<V>/driveagent_<GOOS>_<GOARCH>.tar.gz
https://github.com/jyothri/bhandaar/releases/download/driveagent/v<V>/SHA256SUMS
https://github.com/jyothri/bhandaar/releases/download/driveagent/v<V>/SHA256SUMS.sig
```

- **Exact version, not `latest`.** The tag comes from the server's `latest_agent_version`, so what an agent installs is what the operator chose, and a release can be published long before it's rolled out.
- **Base URL:** `https://github.com/jyothri/bhandaar/releases/download` is built in. `DRIVEAGENT_UPDATE_URL` overrides it, for tests and a mirror. The handshake's `download_url` stays informational, as the human-facing page in messages: it points at the release page, not at assets.
- **Platforms:** an OS and architecture without an asset (linux/arm64 today) never updates, and says so once per run.
- **Limits:** 2-minute timeout per file, at most 100 MB for the tarball and 64 KB for the other two. Plain HTTPS through the system proxy settings (`ProxyFromEnvironment`), following GitHub's redirect to its CDN.

Verification, in order; any failure stops the update:

1. **Signature.** `SHA256SUMS.sig` is an Ed25519 signature over the exact bytes of `SHA256SUMS`, checked with `crypto/ed25519` against the public keys built into driveagent (`internal/update/keys.go`). Holding a list of keys allows rotation: the next release can trust a new key before signing with it.
2. **Checksum.** The tarball's SHA-256 must match its line in `SHA256SUMS`.
3. **Contents.** The tarball must hold a regular file named `driveagent`, of at most 100 MB, and nothing outside its top level. The `README.md` beside it is ignored.
4. **It runs.** The extracted binary, written beside the current one (see below), runs `driveagent version` with a 10-second timeout. It must exit 0 and report exactly `V`. This catches a wrong architecture, a truncated file, or a build that doesn't start.

The signature is what makes an update trustworthy. The checksum alone only catches a corrupt download: anyone able to publish a release could publish a matching `SHA256SUMS`. The signing key never leaves GitHub's secrets.

### Signing in CI

The `release` job of `driveagent.yml` gains one step after `Checksums`:

```bash
printf '%s' "$DRIVEAGENT_SIGNING_KEY" | base64 -d > key.pem
openssl pkeyutl -sign -rawin -inkey key.pem -in dist/SHA256SUMS -out dist/SHA256SUMS.sig
rm key.pem
```

- **Secret:** `DRIVEAGENT_SIGNING_KEY` holds a PEM Ed25519 private key, base64-encoded. It's made once with `openssl genpkey -algorithm ed25519`. The public key's 32 raw bytes go into `keys.go`.
- **Upload:** `gh release create` uploads `SHA256SUMS.sig` with the rest.
- **Self-check:** the job verifies the signature with the public key from `keys.go` before releasing, so a mismatched secret fails the release instead of shipping updates nobody can install.
- **Key loss:** if the private key is lost or leaks, make a new one, ship a release that trusts both keys (installed by hand on machines that can't verify it), then drop the old key.

## Replacing the binary

- **Which file:** `os.Executable()`, with symlinks resolved, so a symlink in `~/.local/bin` pointing elsewhere updates its target. That's the file the next run starts.
- **How:**
  - Write the new binary to a temp file in the **same directory**, `.driveagent-update-<random>`, mode 0755, and `fsync` it. Verification step 4 runs this file.
  - Copy the current binary to `<name>.prev` (replacing any older one), for a manual rollback.
  - `rename(2)` the temp file over the current one.
  - On any failure, remove the temp file. The current binary is never touched until the rename, which is atomic on one filesystem.
- **macOS:** replacing by rename, not by writing into the existing file, matters. Overwriting a running Mach-O in place can get the process killed for an invalid code signature. A new file under the old name is a new vnode, and Go's linker signs arm64 binaries ad hoc. Files downloaded by Go carry no quarantine attribute, so Gatekeeper doesn't stop them.
- **Running copies:** on Linux and macOS, a running process keeps its old inode, so replacing the file under it is safe. The [Alone](#alone) check means none is running anyway.

### Where it can't update

If the directory isn't writable (a root-owned `/usr/local/bin`, say), or holds the binary through a read-only mount, the update is skipped before anything is downloaded:

```
note: driveagent 0.7.0 is available, but /usr/local/bin isn't writable by this user, so it can't update itself.
Install it by hand (https://github.com/jyothri/bhandaar/releases/tag/driveagent/v0.7.0), or install driveagent somewhere you can write, such as ~/.local/bin
```

For `upgrade_required`, the same text goes with exit 4.

## Failures

A failed download or verification is recorded in the state dir's `update.json` (`{"version": "0.7.0", "failed_at": "…", "error": "…"}`). Automatic updates to that version aren't tried again for an hour, so a broken release or a GitHub outage doesn't slow every cron run. `driveagent update` ignores the record. A successful update, or a newer `latest_agent_version`, clears it.

Errors are one line, naming the step: `couldn't update to driveagent 0.7.0: SHA256SUMS signature doesn't verify`. For `upgrade_recommended`, the command then carries on.

## Commands

### `driveagent update`

Updates now, to the server's `latest_agent_version`. It needs to be logged in only to know the server's URL, not a token: the handshake is unauthenticated. It ignores `DRIVEAGENT_NO_AUTO_UPDATE` and the failure record. It doesn't re-exec; it prints `updated driveagent 0.6.1 → 0.7.0` and exits 0.

| Situation | Output | Exit |
|---|---|---|
| Already current (or newer) | `driveagent 0.7.0 is current` | 0 |
| Another driveagent running | The usual busy message | 5 |
| Can't update (not writable, no asset, failed verification) | The reason | 1 |

Flags:
- `--check`: report only, as `driveagent 0.7.0 is available (this is 0.6.1)` or `… is current`, without downloading. Exit 0 either way.
- `--version V`: install exactly `V`, even if it's older than this one, for a rollback. That release must still be signed, so it can't go back past the first signed release.

### `remote-status`

Gains one line: `update: driveagent 0.7.0 is available; automatic updates on` (or `off (DRIVEAGENT_NO_AUTO_UPDATE)`, or `can't update: <reason>`). `remote-status` itself still updates, like the other commands.

## Configuration

| Variable | Effect |
|---|---|
| `DRIVEAGENT_NO_AUTO_UPDATE=1` | No automatic updates; `driveagent update` still works. Commands print the note, as today |
| `DRIVEAGENT_UPDATE_URL` | Base URL for releases instead of GitHub's; for tests and mirrors |
| `DRIVEAGENT_UPDATED_FROM` | Set by the re-exec; not for users |

## Code

- **`internal/update`** (new):
  - `Check`: decide from a handshake response, the version and the environment.
  - `Download`: fetch the three files into memory, with limits.
  - `Verify`: signature, checksum, extract.
  - `Install`: temp file, smoke test, `.prev`, rename.
  - `keys.go`: the public keys.

  Every network and exec step goes through package-level funcs that tests replace.
- **`internal/runlock`:** `TryAlone` (convert the instance lock to exclusive under the gate), and joiners waiting while `updating` is set.
- **`cmd/driveagent`:**
  - `selfUpdate` and the re-exec (`syscall.Exec`).
  - `scan` handshakes before its disk and upload locks.
  - `preflight` takes an optional earlier handshake.
  - The `update` command, and the `remote-status` line.
- **`.github/workflows/driveagent.yml`:** sign `SHA256SUMS`, check the signature, upload `SHA256SUMS.sig`.
- **Docs:**
  - [`remote-sync-ci.md`](remote-sync-ci.md): signing, the key, and installing by hand still working.
  - [`remote-sync-agent.md`](remote-sync-agent.md): the update step in each command's order, the `update` command and the variables.
  - The agent's README.

## Tests

- **`internal/update`**, against an `httptest` server laid out like GitHub's release downloads, with a test key pair:
  - an update end to end into a temp dir
  - each verification failure: bad signature, unknown key, checksum mismatch, a missing asset, a tarball without `driveagent`, a path escaping the tarball, oversize files, a binary that reports the wrong version
  - an unwritable directory
  - symlinked executables
  - `.prev`
  - never downgrading without `--version`
- **`runlock`:** `TryAlone` succeeds alone, fails beside a second process, and blocks a joiner until it's done; a joiner waits while `updating` is set, then refuses a version mismatch.
- **`cmd/driveagent`** (with the exec replaced by a recorder):
  - `scan` and `sync` re-exec with the same arguments and `DRIVEAGENT_UPDATED_FROM`
  - no update when `DRIVEAGENT_UPDATED_FROM` is set, for dev builds, with `DRIVEAGENT_NO_AUTO_UPDATE`, or with another driveagent running
  - `upgrade_required` that can't update exits 4 with the manual instructions
  - the failure record's hour
  - `update`, `update --check`, `update --version`
- **CI:** the release job checks its own signature with the built-in public key before uploading.
- **By hand, once,** on Linux and macOS (arm64): install the first release with the updater, raise `AGENTSERVER_LATEST_AGENT_VERSION` on the dev stack to a second test release, and run `driveagent scan` on a small test drive. It should update, re-exec, and scan. Then check `driveagent version` and the `.prev` file.

## Rollout

1. **0.7.0** ships the updater and the first signed release. It's installed by hand on each machine, the last time that's needed; older agents can't update themselves.
2. Raise `AGENTSERVER_LATEST_AGENT_VERSION` to 0.7.0 as usual. Agents on 0.6.x get the same note as before.
3. From the next release (0.7.1), raising `AGENTSERVER_LATEST_AGENT_VERSION` is all a rollout takes: each machine updates on its next `scan`, `sync`, `login` or `remote-status` while no other driveagent runs there. To hold a machine back, set `DRIVEAGENT_NO_AUTO_UPDATE=1` in its environment (in its cron line, for scheduled scans).

Implementation is one PR, a commit per step, bumping driveagent to 0.7.0:

1. **Signing:** generate the key, add the secret, add the CI step and `keys.go`, and check that a release from a branch build verifies.
2. **`internal/update`**, with its tests.
3. **`runlock.TryAlone`** and joiners waiting.
4. **`selfUpdate`**, the re-exec, the earlier handshake in `scan`, and `driveagent update`.
5. **Docs**, then the manual check above on the dev stack.

## Decisions

Made 2026-10-03:
- **When:** automatically, on any handshake that reports a newer version, recommended or required; plus `driveagent update` by hand.
- **Mid-command:** replace, then re-exec the same command, only before it has done any work.
- **Verification:** `SHA256SUMS` plus an Ed25519 signature made in CI, the public key built in.
- **Version and opt-out:** the server's `latest_agent_version`, an exact tag rather than GitHub's latest; `DRIVEAGENT_NO_AUTO_UPDATE=1` turns automatic updates off.
