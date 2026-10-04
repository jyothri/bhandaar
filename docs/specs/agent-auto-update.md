# driveagent: Updating Itself

**Status:** proposed. Written 2026-10-03, against driveagent 0.6.1; revised the same day after review.

## Problem

Every driveagent release has to be installed by hand on every machine: download the tarball, check it, unpack it, `install` it (see [Installing a release](remote-sync-ci.md#installing-a-release)). The server already knows which version is current: after each release, `AGENTSERVER_LATEST_AGENT_VERSION` is raised in the prod `.env`, and every handshake reports it. But an older agent only prints a note (`note: driveagent 0.6.1 is available …`), which a scan run from cron never shows anyone. So machines lag behind, and a release that matters, like 0.6.0's MD5 backfill for [duplicates](../archive/duplicates.md), reaches each machine only when someone remembers to install it.

## Goals

1. **Update on handshake.** When the server reports a newer version, driveagent downloads that exact release from GitHub, verifies it, replaces its own binary, and re-runs the command on the new version. This applies whether the server says `upgrade_recommended` or `upgrade_required`.
2. **Never mid-work.** The update happens before a command does anything, never during a scan or an upload.
3. **Only what we built and approved.** A binary replaces driveagent only if it's the release the server named, it's signed for that version with the release key, and its checksum matches. The release key is used only by a release job you approve.
4. **The server controls rollout.** Agents move to the version in `AGENTSERVER_LATEST_AGENT_VERSION`, not to GitHub's latest. Publishing a release changes nothing until that's raised.
5. **Can be turned off.** `DRIVEAGENT_NO_AUTO_UPDATE=1` stops automatic updates. `driveagent update` still updates by hand.

Non-goals:
- Downgrades. A bad release is fixed forward (see [Rollout](#rollout)).
- Updating machines older than the first version with the updater; that one is installed by hand.
- A daemon that updates in the background.
- Updating a binary the user can't write to, such as one installed by root (see [Where it can't update](#where-it-cant-update)).

## What exists

| Piece | Today |
|---|---|
| Handshake | `POST /agent/v1/handshake` answers `decision` (`ok`, `upgrade_recommended`, `upgrade_required`, `unsupported_protocol`), `min_agent_version`, `latest_agent_version` and `download_url`. The server needs no change for this spec |
| Releases | Merging a version bump to `main` creates the tag `driveagent/v<V>` and a GitHub Release with `driveagent_<os>_<arch>.tar.gz` for linux/amd64, darwin/amd64 and darwin/arm64 (about 5.5 MB each), and `SHA256SUMS` ([`remote-sync-ci.md`](remote-sync-ci.md)). The repo is public |
| Commands that handshake | `scan`, `sync`, `login`, `remote-status`, through `preflight` (health, then handshake). `scan` runs it after taking its disk and upload locks |
| One version per machine | Every `driveagent` joins the instance (`instance.gate`, `instance.lock`, `instance.json` in the lock dir) before anything else. While any is running, another **version** is refused with exit 5 ([agent hardening](../archive/agent-hardening.md#goal-1-the-instance-lock)) |
| Exit codes | 3 for the server unreachable, 4 for an upgrade, 5 for busy ([exit codes](remote-sync-agent.md#exit-codes)) |

The instance lock decides the design. The new version can't run while an old one is running, so a process updates **only when it's the only driveagent running**. Otherwise it carries on with the old version and leaves the update to a later run. Everything below keeps "one version at a time" true, including while the binary is being swapped.

## When it updates

The update step, `selfUpdate`, runs in `scan`, `sync` and `login`, and in `driveagent update`. It runs right after the command joins the instance and opens its remote settings, and **before** it takes any other lock, opens `state.db`, or reads a drive. For `scan`, the handshake therefore moves ahead of the disk lock and the upload lock, and the later `preflight` reuses its answer. One visible change follows: a `scan` with the server unreachable now fails up front (exit 3), before waiting for a busy disk, where today it waits first.

`remote-status` only reports what an update would do; it doesn't update. A status command shouldn't replace the binary. `compare`, `report`, `logout`, `version` and `help` don't check at all.

Steps:

1. **Handshake.** Health, then handshake, as `preflight` does now. If the server is unreachable, the command fails as today (exit 3); there's nothing to update to.
2. **Decide.** Update only if all of these hold:
   - `latest_agent_version` is newer than `version.Version`.
   - This is a release build: `version.Commit` is set by CI's `-ldflags`. A local `go build` is never replaced.
   - `DRIVEAGENT_NO_AUTO_UPDATE` isn't set, or this is `driveagent update`.
   - `DRIVEAGENT_UPDATED_FROM` isn't set. The re-exec in step 6 sets it, so a new binary never updates again in the same run, whatever the server says.
   - The last attempt at this version didn't fail less than an hour ago (see [Failures](#failures)).
   - The binary's directory is writable (see [Where it can't update](#where-it-cant-update)).
   - This process is the only driveagent running (see [The instance lock during an update](#the-instance-lock-during-an-update)).
3. **Download** the release named by `latest_agent_version` (see [Download and verify](#download-and-verify)).
4. **Verify** the signature, the checksum, and that the new binary runs and reports the expected version.
5. **Replace** the binary (see [Replacing the binary](#replacing-the-binary)).
6. **Re-exec.** Exec the new binary, keeping the exclusive instance lock (see [Handing the lock to the new binary](#handing-the-lock-to-the-new-binary)). What gets exec'd is exact:
   - **Path:** the resolved path of the running binary (`os.Executable()`, symlinks resolved), the one just replaced, not `os.Args[0]`. A cron line that runs `driveagent` through `PATH`, or by a relative path, would otherwise exec something else, or nothing.
   - **Arguments:** that path, then the original `os.Args[1:]`.
   - **Environment:** the same, plus `DRIVEAGENT_UPDATED_FROM=<old version>` and `DRIVEAGENT_INSTANCE_FD=<fd>`.

   The new process starts the command from the top: it takes its other locks and handshakes, so nothing else is carried across. One line goes to stderr: `updated driveagent 0.6.1 → 0.7.0`.

If the update doesn't happen, or fails:
- **`upgrade_recommended`:** the command carries on with the current version, printing why it didn't update (one line), as it prints the note today.
- **`upgrade_required`:** the command stops with exit 4, as today. If the only obstacle is another running driveagent, it first [waits to be alone](#required-and-busy).

## The instance lock during an update

Every process holds `instance.lock` shared, and joins only while holding `instance.gate`. The updater keeps both for the whole update, from the check to the exec, so no process can join, and no version can start, while the binary changes.

### Checking it's alone

`runlock.TryAlone`, under the gate:

1. Take `instance.gate`, waiting up to 10 s as joiners do.
2. **Unlock** our shared `instance.lock`, then **try it exclusively**, without waiting.
   - **Got it:** no other driveagent is running. Rewrite `instance.json` with `"updating": true` and `"updating_since"`. Keep the gate and the exclusive lock, and go on to download.
   - **Busy:** others are running. **Immediately re-take the shared lock** (`TryRLock`), still under the gate, then release the gate and skip the update. A failure to re-take it is fatal: the command exits 1. It can't fail in practice, because an exclusive lock is only ever taken under the gate, which we hold.

The unlock in step 2 is deliberate, not a lock conversion. `flock(2)` conversion isn't atomic on Linux or macOS: the kernel drops the shared lock first, and a conversion that fails leaves the process holding nothing, while `gofrs/flock` still believes it holds the shared lock. Doing the unlock and the re-take explicitly, under the gate, keeps both the lock and the library's state right. This is how `JoinInstance` already switches from exclusive to shared.

Two processes started at once both find the other and neither updates; the next run will.

### Joiners while updating

A process that starts while an update runs finds the gate held. Today it gives up after 10 s (exit 5). From now on, when the wait passes 10 s, it reads `instance.json` (reading the sidecar without the gate is fine for this) and decides:
- **Says `updating`, and `updating_since` is less than 10 minutes ago:** keep waiting for the gate, until 10 minutes after `updating_since`. It prints once: `waiting for driveagent to finish updating itself (pid 12345)`. The download timeouts bound an update at about 6.5 minutes (3 files × 2 minutes, plus the 10 s smoke test).
- **Otherwise:** give up as today (exit 5). That covers no `updating`, or an `updating_since` older than 10 minutes, which means a hung updater or a stale sidecar. A stale sidecar is harmless once the gate is free.

Once it has the gate, the joiner joins as usual. If the new version is now running, the joiner's own version doesn't match, and it exits 5 with the usual message telling the user to re-run. Under cron, the next run is the new version anyway.

### Handing the lock to the new binary

Releasing the instance lock before `exec` would leave a gap. An old-version process, such as the cron job for another drive started in the same minute, could join in it and write `instance.json` with its version. Then the freshly updated binary would exit 5, and that run's scan would be lost, possibly along with the runs after it while the old scan runs for hours. So the lock is handed over, not released:

1. Before exec, clear `FD_CLOEXEC` on the fd holding `instance.lock` (Go opens files close-on-exec). Release `instance.gate`: a joiner that gets it now finds `instance.lock` held exclusively and `instance.json` saying `updating`, and is refused, or waits as above.
2. Exec, with `DRIVEAGENT_INSTANCE_FD=<fd>`. A `flock` lock belongs to the open file description, which survives `exec`, so the new process starts already holding `instance.lock` exclusively.
3. The new process, instead of joining, **adopts** that fd (`runlock.AdoptInstance`):
   - Check that the fd is a lock file in the lock dir.
   - Take the gate.
   - Rewrite `instance.json` with its own version, without `updating`.
   - Unlock and re-take `instance.lock` shared (the same explicit switch as `JoinInstance`).
   - Release the gate, and carry on as a joined process.

   If adopting fails (an unknown fd, say), it closes the fd and joins normally.

So `instance.lock` is held exclusively from the moment the updater confirms it's alone until the new version writes `instance.json`. At no point can a second version run.

### Required and busy

With `upgrade_required`, a busy machine would otherwise fail every run until a long `sync` or `scan` finishes. So, for a required update only, a process that isn't alone waits for the others to finish:
- It holds its shared lock and retries `TryAlone` every 30 s, for up to 10 minutes.
- It prints once: `driveagent 0.7.0 is required; waiting for the other driveagent (pid 12345) to finish`.
- After 10 minutes, it exits 4 with: `driveagent 0.7.0 is required, and will be installed by the next run once the other driveagent (pid 12345) has finished`. No manual steps, since none are needed.

A required version also means the server refuses the old agent's uploads (426), so the other process won't run long.

## Download and verify

URLs, for version `V`, OS `GOOS` and architecture `GOARCH` (checked: GitHub serves the tag's `/` as is):

```
https://github.com/jyothri/bhandaar/releases/download/driveagent/v<V>/driveagent_<GOOS>_<GOARCH>.tar.gz
https://github.com/jyothri/bhandaar/releases/download/driveagent/v<V>/SHA256SUMS
https://github.com/jyothri/bhandaar/releases/download/driveagent/v<V>/SHA256SUMS.sig
```

- **Exact version, not `latest`.** The tag comes from the server's `latest_agent_version`, so what an agent installs is what the operator chose, and a release can be published long before it's rolled out.
- **Base URL:** `https://github.com/jyothri/bhandaar/releases/download` is built in. `DRIVEAGENT_UPDATE_URL` overrides it, for tests and a mirror. The handshake's `download_url` stays informational, the human-facing page in messages.
- **Platforms:** an OS and architecture without an asset (linux/arm64 today) never updates, and says so once per run.
- **Limits:** 2-minute timeout per file, at most 100 MB for the tarball and 64 KB for each of the other two. Plain HTTPS through the system proxy settings (`ProxyFromEnvironment`), following GitHub's redirect to its CDN.

Verification, in order; any failure stops the update:

1. **Signature, bound to the version.** `SHA256SUMS.sig` is an Ed25519 signature over `driveagent/v<V>\n` followed by the exact bytes of `SHA256SUMS`. driveagent rebuilds that message with the `V` it asked for, and checks it with `crypto/ed25519` against the public keys built into driveagent (`internal/update/keys.go`). An older release served under a newer tag, by a mirror or a tampered asset, fails here, before anything runs. Holding a list of keys allows rotation.
2. **Checksum.** The tarball's SHA-256 must match its line in `SHA256SUMS`.
3. **Contents.** The tarball must hold a regular file named `driveagent`, of at most 100 MB, and nothing outside its top level. The `README.md` beside it is ignored.
4. **It runs.** The extracted binary, already written beside the current one (see below), runs `driveagent version` with a 10-second timeout. It must exit 0 and report exactly `V`. This catches a wrong architecture, a truncated file, or a build that doesn't start.

### Signing in CI

The signing key is a secret of a GitHub **Environment**, `driveagent-release`, not of the repository:
- **Deployment branches:** `main` only.
- **Required reviewer:** you. Every release job pauses until you approve it in GitHub.

A workflow on a branch, or an edited workflow that wasn't merged to `main`, can't read the key. A merged one still waits for your approval before the job starts. So signing needs both push access to `main` and your approval, and the signature then guards against more than tampering with GitHub's asset storage.

The `release` job of `driveagent.yml` gets `environment: driveagent-release`, and one step after `Checksums`:

```bash
V=...   # the version being released, as in the existing release step
printf '%s' "$DRIVEAGENT_SIGNING_KEY" | base64 -d > key.pem
{ printf 'driveagent/v%s\n' "$V"; cat dist/SHA256SUMS; } > signed.txt
openssl pkeyutl -sign -rawin -inkey key.pem -in signed.txt -out dist/SHA256SUMS.sig
rm key.pem signed.txt
```

- **Secret:** `DRIVEAGENT_SIGNING_KEY` holds a PEM Ed25519 private key, base64-encoded. It's made once with `openssl genpkey -algorithm ed25519`. The public key's 32 raw bytes go into `keys.go`.
- **Upload:** `gh release create` uploads `SHA256SUMS.sig` with the rest.
- **Self-check:** before releasing, the job verifies the signature with the public key from `keys.go`, using a small Go program in `agent/client/scripts/`. A mismatched secret fails the release instead of shipping updates nobody can install.
- **Key loss:** if the private key is lost or leaks, make a new one, ship a release that trusts both keys (installed by hand on machines that can't verify it), then drop the old key.

## Replacing the binary

- **Which file:** the resolved path of the running binary (`os.Executable()`, symlinks resolved), so a symlink in `~/.local/bin` pointing elsewhere updates its target. It's the file the re-exec runs, and its directory is the one that must be writable.
- **How:**
  1. Create the temp file `.driveagent-update-<random>` in that directory, mode 0755. Creating it is also the writability check (see below). Write the new binary to it and `fsync`. Verification step 4 runs this file.
  2. Copy the current binary to `<name>.prev`, replacing any older one, for a manual rollback. If that fails (disk full, quota), the update **aborts**: an update without a way back isn't worth the risk, and the temp file and `.prev` need twice the binary's size anyway.
  3. `rename(2)` the temp file over the current one.

  On any failure, remove the temp file. The current binary is never touched until the rename, which is atomic on one filesystem.
- **macOS:** replace by rename, never by writing into the existing file. Overwriting a running Mach-O in place can get the process killed for an invalid code signature, whereas a new file under the old name is a new vnode. Files downloaded by Go carry no quarantine attribute, so Gatekeeper doesn't stop them. darwin/arm64 binaries must be at least ad-hoc signed to run. Go's linker does that by default, but the manual test (see [Tests](#tests)) checks `codesign -v` on the downloaded binary, so a future build flag can't quietly break updates on Apple Silicon.
- **Running copies:** on Linux and macOS, a running process keeps its old inode, so replacing the file under it is safe. The alone check means none is running anyway.

### Where it can't update

Writability is checked by creating the temp file before anything is downloaded, not with `access(2)`. That gives the right answer for ACLs, read-only bind mounts and immutable flags. If it can't be created (a root-owned `/usr/local/bin`, say):

```
note: driveagent 0.7.0 is available, but /usr/local/bin isn't writable by this user, so it can't update itself.
Install it by hand (https://github.com/jyothri/bhandaar/releases/tag/driveagent/v0.7.0), or install driveagent somewhere you can write, such as ~/.local/bin
```

For `upgrade_required`, the same text goes with exit 4.

## Failures

A failed download or verification is recorded in `update.json` in the **lock dir**: `{"version": "0.7.0", "failed_at": "…", "error": "…"}`. The lock dir, not the state dir, because the binary is shared by everything this user runs. Automatic updates to that version aren't tried again for an hour, so a broken release or a GitHub outage doesn't slow every cron run. `driveagent update` ignores the record. A successful update, or a newer `latest_agent_version`, clears it.

Errors are one line, naming the step: `couldn't update to driveagent 0.7.0: the SHA256SUMS signature doesn't verify`. For `upgrade_recommended`, the command then carries on.

## Commands

### `driveagent update`

Updates now, to the server's `latest_agent_version`. It needs the server's URL from the state dir, but no login: the handshake is unauthenticated. It ignores `DRIVEAGENT_NO_AUTO_UPDATE` and the failure record. It doesn't re-exec; it prints `updated driveagent 0.6.1 → 0.7.0` and exits 0.

| Situation | Output | Exit |
|---|---|---|
| Already current (or newer) | `driveagent 0.7.0 is current` | 0 |
| Another driveagent running | The usual busy message | 5 |
| Can't update (not writable, no asset, failed verification) | The reason | 1 |

Flags:
- `--check`: report only, as `driveagent 0.7.0 is available (this is 0.6.1)` or `… is current`, without downloading. Exit 0 either way.
- `--version V`: install exactly `V`, even if it's older than this one, for a rollback on one machine. That release must still be signed for `V`, so it can't go back past the first signed release.

### `remote-status`

Gains one line, reporting without updating:
- `update: driveagent 0.7.0 is available; it will be installed by the next scan, sync or login`
- `update: … automatic updates are off (DRIVEAGENT_NO_AUTO_UPDATE)`
- `update: … can't update: <reason>`

## Configuration

| Variable | Effect |
|---|---|
| `DRIVEAGENT_NO_AUTO_UPDATE=1` | No automatic updates; `driveagent update` still works. Commands print the note, as today |
| `DRIVEAGENT_UPDATE_URL` | Base URL for releases instead of GitHub's; for tests and mirrors |
| `DRIVEAGENT_UPDATED_FROM`, `DRIVEAGENT_INSTANCE_FD` | Set by the re-exec; not for users |

## Code

- **`internal/update`** (new):
  - `Check`: decide from a handshake response, the version and the environment.
  - `Download`: fetch the three files into memory, with limits.
  - `Verify`: the version-bound signature, the checksum, extraction.
  - `Install`: temp file, smoke test, `.prev`, rename.
  - `keys.go`: the public keys.

  Every network and exec step goes through package-level funcs that tests replace.
- **`internal/runlock`:**
  - `TryAlone` (unlock, try exclusive, else re-take shared, all under the gate)
  - joiners waiting while `updating` is recent
  - `HandOver` (clear `FD_CLOEXEC`, release the gate) and `AdoptInstance`
  - `update.json` in the lock dir
- **`cmd/driveagent`:**
  - `selfUpdate`, the wait when required and busy, and the re-exec (`syscall.Exec` with the resolved path)
  - `scan` handshakes before its disk and upload locks; `preflight` takes an earlier handshake
  - the `update` command, and the `remote-status` line
- **`.github/workflows/driveagent.yml`:** the `driveagent-release` environment, signing, the self-check, uploading `SHA256SUMS.sig`.
- **Docs:**
  - [`remote-sync-ci.md`](remote-sync-ci.md): signing, the environment, the key; installing by hand still works.
  - [`remote-sync-agent.md`](remote-sync-agent.md): the update step in each command's order; `scan` failing up front when the server is unreachable, in the exit-code notes; the `update` command and the variables.
  - The agent's README.

## Tests

The implementation PR must include these two `runlock` tests; the first design (before review) failed both:
1. **A failed `TryAlone` keeps the process joined.** Beside a second process, `TryAlone` fails, and afterwards a joiner of a *different version* is still refused (exit 5).
2. **No gap at the exec.** Run the hand-over and the adoption across a real `exec` of a helper binary. A joiner of the old version started in between is refused, and the re-exec'd process doesn't exit 5.

Also:
- **`internal/update`**, against an `httptest` server laid out like GitHub's release downloads, with a test key pair:
  - an update end to end into a temp dir
  - each verification failure: bad signature, unknown key, a signature for another version (a replayed older release), checksum mismatch, a missing asset, a tarball without `driveagent`, a path escaping the tarball, oversize files, a binary that reports the wrong version
  - an unwritable directory, found by creating the temp file
  - symlinked executables
  - `.prev`, and aborting when it can't be written
  - never downgrading without `--version`
- **`runlock`:** `TryAlone` succeeds alone and blocks joiners until it's done; a joiner waits while `updating` is recent and gives up on a stale one; adopting an unknown fd falls back to joining.
- **`cmd/driveagent`** (with the exec replaced by a recorder):
  - `scan`, `sync` and `login` re-exec the resolved path with `os.Args[1:]`, `DRIVEAGENT_UPDATED_FROM` and `DRIVEAGENT_INSTANCE_FD`
  - no update when `DRIVEAGENT_UPDATED_FROM` is set, for dev builds, with `DRIVEAGENT_NO_AUTO_UPDATE`, from `remote-status`, or with another driveagent running
  - required and busy: it waits, then exits 4 with the "next run" message
  - the failure record's hour
  - `update`, `update --check`, `update --version`
- **CI:** the release job checks its own signature with the built-in public key before uploading.
- **By hand, once,** on Linux and on macOS (arm64):
  1. Install the first release with the updater.
  2. Raise `AGENTSERVER_LATEST_AGENT_VERSION` on the dev stack to a second test release.
  3. Run `driveagent scan` on a small test drive: it should update, re-exec, and scan.
  4. Check `driveagent version`, the `.prev` file, and on macOS `codesign -v` on the new binary.

## Rollout

1. **0.7.0** ships the updater and the first signed release. It's installed by hand on each machine, the last time that's needed; older agents can't update themselves. Before it merges: create the key, the `driveagent-release` environment and its secret.
2. Raise `AGENTSERVER_LATEST_AGENT_VERSION` to 0.7.0 as usual. Agents on 0.6.x get the same note as before.
3. From the next release (0.7.1), approving the release job and raising `AGENTSERVER_LATEST_AGENT_VERSION` is all a rollout takes. Each machine updates on its next `scan`, `sync` or `login` while no other driveagent runs there. To hold a machine back, set `DRIVEAGENT_NO_AUTO_UPDATE=1` in its environment (in its cron line, for scheduled scans).

**A bad release is fixed forward.** Agents never downgrade, so lowering `AGENTSERVER_LATEST_AGENT_VERSION` after machines have updated doesn't undo it. The fix is the next release, or on one machine `driveagent update --version <good>` or the `.prev` copy. With a few machines, that's acceptable. To limit the damage, roll out to one machine first: hold the others back with `DRIVEAGENT_NO_AUTO_UPDATE=1` until it has run a scan.

Implementation is one PR, a commit per step, bumping driveagent to 0.7.0:

1. **Signing:** the key, the environment, the CI step, the self-check and `keys.go`.
2. **`internal/update`**, with its tests.
3. **`runlock`:** `TryAlone`, joiners waiting, the hand-over and adoption, with the two required tests.
4. **`selfUpdate`**, the re-exec, the earlier handshake in `scan`, `driveagent update` and the `remote-status` line.
5. **Docs**, then the manual check above on the dev stack.

## Decisions

Made 2026-10-03:
- **When:** automatically, on the handshake of `scan`, `sync` and `login`, whenever a newer version is reported, recommended or required; plus `driveagent update` by hand. `remote-status` only reports (after review).
- **Mid-command:** replace, then re-exec the same command, only before it has done any work, handing the instance lock to the new binary.
- **Verification:** an Ed25519 signature over the version and `SHA256SUMS`, made in CI; the public key built in.
- **Signing key:** a secret of the `driveagent-release` GitHub Environment, limited to `main`, each release approved by you (after review).
- **Version and opt-out:** the server's `latest_agent_version`, an exact tag rather than GitHub's latest; `DRIVEAGENT_NO_AUTO_UPDATE=1` turns automatic updates off.
- **Rollback:** none across machines; a bad release is fixed forward (after review).
