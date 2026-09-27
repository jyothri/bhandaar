# driveagent: Agent Hardening

**Status:** implemented (0.5.0), archived. Written 2026-09-27 as a plan against 0.4.1, then kept as the as-built reference; code comments cite its section names. The current specs summarise it where they touch it: the locks and exit code 5 in [`remote-sync-agent.md`](../specs/remote-sync-agent.md#upload-lock), the scan order in [`drive-comparison-agent.md`](../specs/drive-comparison-agent.md), and machine binding in the [operational rules](../specs/remote-sync.md#operational-rules). It closed what was follow-up 2 in [`remote-sync-followups.md`](../specs/remote-sync-followups.md) (a state dir copied to another machine).

## Goals

1. **One agent per machine.** All `driveagent` processes running at once on a machine (per user) use **one state dir**, and they are all **one binary version**. Another state dir, or another version, is refused while any `driveagent` is running. A state dir copied to another machine is detected.
2. **One scan per physical drive.** At most one `scan` reads a physical disk at a time, whatever `--drive-id` or partition it uses. Scans of *different* disks still run in parallel, as seagate1 and seagate2 do.

Non-goals: a daemon, or queueing work inside one process. Coordinating two OS users on one machine (see [Limits](#limits)). Stopping a binary older than 0.5.0: it doesn't know about the new locks (see [Rollout](#rollout)).

## Before 0.5.0

| What | How | Gap |
|---|---|---|
| Two scans of one drive | The upload lock, `flock` on `<state-dir>/upload-<sha256(drive_id)[:16]>.lock`, taken and waited for by `scan` | Keyed on the **label**. The same disk under two `--drive-id`s, or two partitions of one disk (seagate1 also has the `DBR_BOOT` FAT32 partition), scanned at once and fought over the drive's read head |
| `scan` vs `sync` of one drive | The same upload lock; `sync` and `remote-status` skip a busy drive | None |
| Concurrent processes on `state.db` | WAL, `BEGIN IMMEDIATE`, `busy_timeout(5000)` | None |
| Token refresh | `flock` on `credentials.lock` | None |
| Two state dirs on one machine | Nothing | Two agents scanning one disk, each unaware of the other; different `agent_id`s for one machine |
| Two versions at once | Nothing (the schema check refuses only a **newer** schema) | An upgrade mid-scan left old and new binaries writing one `state.db` |
| State dir copied to another machine | Nothing ([operational rule 1](../specs/remote-sync.md#operational-rules)) | Both machines share `agent_id` and stream ids and wipe each other's uploads |

## Design

### The lock dir

Locks that must span state dirs live in a per-user **lock dir**, not in the state dir: **`<home>/.driveagent-locks/`**, where `<home>` is the user's home directory from the account database (`user.Current().HomeDir`), not `$HOME`; `os.UserHomeDir` is only a fallback when the account can't be looked up. `$DRIVEAGENT_LOCK_DIR` overrides it.

The path has to be the same for every `driveagent` process of the user, however it was started, and it must not disappear while a lock is held. Directories from the environment fail both:

- `$XDG_RUNTIME_DIR` (`/run/user/<uid>`) is set only in login sessions, not under cron, `sudo`, `su` or Docker, so processes would split between it and a fallback. It's also deleted when the user's last session ends, so a `nohup` or tmux scan would lose its locks when the user logs out and back in.
- macOS's `$TMPDIR` (`/var/folders/…/T/`) can be unset under cron, and macOS deletes files there that haven't been used for about 3 days, even while a long scan holds one.
- `$HOME` doesn't always match the user actually running: some `sudo` setups keep the caller's `$HOME` while running as root. The account database's home directory always belongs to the running user. (A `driveagent` run as root is therefore a different user, as in [Limits](#limits).)

The home directory is on a local filesystem, where `flock` works. The files persisting doesn't matter, because a lock is released when its process exits.

The lock dir is created `0700`. The override is for tests (the `cmd/driveagent` tests use a temp one) and for a throwaway dev state dir, such as testing against the dev `agentserver` while a real scan runs. A separate lock dir is a separate world: nothing is coordinated across it, and the usage text says so.

All locks are `flock`s on files that are never deleted, so a crashed process releases them and nothing is ever stale. Each lock may have a JSON **sidecar** describing its holder, written after taking the lock and read only when the lock is busy, to say who holds it. A leftover sidecar is harmless, because it's never trusted while the lock is free.

`internal/runlock` holds the lock dir lookup (`Dir`), the instance lock (`JoinInstance`), the disk locks (`LockDisks`) and the sidecars. Its busy errors match `runlock.ErrBusy`. `syncer.Lock`, the upload lock, is unchanged.

### Goal 1: the instance lock

Every command that uses the state dir (`scan`, `sync`, `compare`, `report`, `remote-status`, `login`, `logout`) joins the instance right after parsing its flags, before anything else, and holds it until it returns (`joinInstance` in `cmd/driveagent/locks.go`). `version` and `help` don't.

Files in the lock dir: `instance.gate` (exclusive, held for milliseconds), `instance.lock` (shared, held for each process's lifetime), and `instance.json`:

```json
{"state_dir": "/home/jyothri/.driveagent", "version": "0.5.0", "pid": 12345, "started": "2026-09-28T10:00:00Z"}
```

`state_dir` is absolute with symlinks resolved, so `~/.driveagent` and a symlink to it are the same dir. To join:

1. Take `instance.gate` exclusively, waiting up to 10 s.
2. Try `instance.lock` **exclusively**, without waiting.
   - **Got it:** nothing else is running. Write `instance.json` (temp file, then rename), release the exclusive lock, take it **shared**.
   - **Busy:** something else is running. Read `instance.json`. If its `state_dir` and `version` match ours, take `instance.lock` shared. Otherwise refuse (below).
3. Release `instance.gate`.

Every joiner passes through the gate, so the release-then-share in step 2 can't interleave with another process's check. `instance.json` is rewritten only by a process that found no one else running, so it always describes the processes still running.

A process that needs the state dir to itself (`login --new-agent`) joins **alone**: it must find no one running, keeps `instance.lock` exclusive, and records `"alone": true`, so nothing else joins until it's done.

The version check compares `version.Version` only, not `Commit`, so dev builds of one version can run together.

Refusals exit with a new code, **5 (busy)**, so scripts can tell "try later" from a real error:

```
error: another driveagent is running on this machine with state dir /home/jyothri/.driveagent (driveagent 0.5.0, pid 12345, since 2026-09-28 10:00).
One machine runs one state dir: wait for it to finish, or pass the same --state-dir
```

```
error: driveagent 0.5.0 is running on this machine (pid 12345, since 2026-09-28 10:00); this is 0.5.1.
Wait for it to finish before running a different version
```

The pid is the first process's. Others may be running too; the message doesn't try to list them.

### Goal 1: machine binding (follow-up 2)

`agent.json` gains `machine_id`: `/etc/machine-id` (or `/var/lib/dbus/machine-id`) on Linux, `IOPlatformUUID` from `ioreg -rd1 -c IOPlatformExpertDevice` on macOS, and empty where neither can be read (`creds.MachineID`). An `agent.json` without it (every one from before 0.5.0) adopts the current machine on first use, keeping its agent id: no change for anyone. A machine whose id can't be read never mismatches.

On a mismatch the state dir was copied or moved from another machine, and `creds.AgentID` fails with a `*creds.MachineMismatchError`. So every command that talks to the server (`scan`, `sync`, `remote-status`, `login`) refuses, with exit 1, and so does `logout`, whose revocation would also end the other machine's login:

```
error: this state dir belongs to another machine (agent 3f2c1d7e…, machine 0123abcd…; this machine is fedcba98…). Using one state dir on two machines makes them undo each other's uploads.
If it was moved here for good, run "driveagent login --new-agent": this machine becomes a new agent and re-uploads its drives
```

`compare` and `report` never contact the server, so they aren't refused.

`login --new-agent` joins the instance alone, then (`startNewAgent`): forgets every drive's stream, marker and `sync_rejected` (`store.ResetStreams`), so each drive gets a new stream when next opened and is uploaded again in full; deletes `credentials.json` without revoking it, since the old agent may still be in use on the other machine; writes `agent.json` with a new agent id and this machine (`creds.NewAgent`); then logs in as usual. It prints the new agent id and points at `driveagent sync`. It works on any state dir, mismatched or not, and each step can simply be run again if one fails.

This refuses rather than minting a new agent silently, as follow-up 2 first suggested: a moved state dir (a new laptop) and a copied one look the same from inside, and a silent new agent would start a full re-upload of every drive with no one having asked for it.

### Goal 2: the physical-drive lock

**The key.** `identity.DiskKeys(root string) []string` names the disk(s) holding the drive root (`--drive-root`, else `--path`), read only and without root. Per disk, it returns the first of:

1. `serial:<HWSerial>`, the disk's serial, normalised (generic serials count as missing);
2. `dev:<whole disk>`, the whole-disk device: on Linux its `major:minor` (`dev:8:16` for `sdb`); on macOS the `diskutil` whole disk (`dev:disk4`), as `detectMacOS` works it out (for APFS, the physical store's disk).

When no disk can be found at all (tmpfs, overlay, a network share, an unsupported OS), it falls back to:

3. `fs:<source>:<FSUUID>`, the filesystem ID;
4. `path:<drive root>`, absolute with symlinks resolved.

It never fails: whatever can't be read falls through to the next kind, and a root that doesn't exist gets a path key (the scan then fails on its own checks).

Keys 1 and 2 are per **disk**, so every partition of a disk gets the same key. The fallbacks keep apart only scans of one filesystem or mount; `scan` then prints `note: couldn't find the disk holding <root>; only other scans of the same <key> are kept out`.

**On Linux**, `stat` gives the filesystem's device number, and sysfs the disk behind it: `/sys/dev/block/<maj>:<min>` resolves to the device's directory; a partition (it has a `partition` file) has its disk as the parent directory, whose `dev` is the disk's number; a device-mapper or md device (LUKS, LVM, RAID) lists its underlying devices in `slaves/`, followed down to the disks, so a filesystem spanning several disks gets a key per disk. Each disk's serial is `ID_SERIAL_SHORT` in its udev record (`/run/udev/data/b<disk>`), or, for a single disk without one, in the partition's record.

**On macOS**, it's what `Detect` reads: the USB serial from `ioreg`, else the whole disk from `diskutil`.

**The lock.** A `flock` on `<lock dir>/disk-<sha256(key)[:16]>.lock` per key, taken in sorted order (so two scans spanning the same disks can't deadlock), with a sidecar `disk-<…>.json`:

```json
{"key": "serial:NA77YET6", "drive_id": "seagate1", "path": "/media/jyothri/Seagate1/Jyo/interview", "pid": 12345, "started": "2026-09-28T10:00:00Z"}
```

It lives in the lock dir, not the state dir, so it doesn't depend on the instance lock: it keeps two scans off one disk whichever state dirs they use, as long as they share the lock dir.

**When a disk is busy**, `scan` fails at once with exit 5:

```
error: another scan is reading this disk (serial NA77YET6): drive seagate1, /media/jyothri/Seagate1/Jyo/interview (pid 12345, since 2026-09-28 10:00)
One scan per disk at a time: wait for it, or re-run with --wait
```

`--wait` makes it wait instead, printing `waiting: another scan is reading this disk …` once, for scripted sequences. Failing is the default because a second scan of a busy disk is almost always a mistake, and a silent wait looks like a hang.

**Order in `scan`** (`runScan`'s steps):

0. Join the instance; read the disk keys of the drive root and take their locks.
1. Take the drive's upload lock, waiting for a `sync` of it (unchanged).
2. Onward, unchanged: preflight, drive checks, and so on.

The instance lock comes first in every command, and disk locks always before the upload lock. `sync` never takes a disk lock (it doesn't touch the drive), so there's no lock-order cycle. A scan refused at step 0 hasn't touched `state.db` or the server. The wrong-drive guard keeps calling `Detect`; `DiskKeys` shares its parsing but runs separately, because it runs first.

The upload lock stays: it still keeps a scan and a `sync` of one drive apart, and two scans of one `--drive-id` on *different* disks (a mistake the wrong-drive guard catches) still serialize on it.

## Limits

- **Per OS user.** Two users on one machine have different lock dirs and aren't coordinated; that includes the same person running `driveagent` under `sudo`. There's one user on each machine, and the agent never needs root.
- **Serial collisions.** Two disks in identical USB enclosures whose bridge reports one fixed, non-generic serial would share a key, and their scans would serialize. It's safe (a wait, not corruption), and the message names the key, so the fix is to add that serial to `genericSerials`.
- **Partial udev.** If a disk's udev record is missing but one partition's has the serial, and another partition's doesn't, the two partitions get different keys (`serial:` and `dev:`). Real udev records every partition with its disk's serial; this only matters in a container with some records copied in.
- **Older binaries** (0.4.x) take none of the new locks and aren't seen by them.

## Code

| Where | What |
|---|---|
| `internal/runlock` | `Dir` (lock dir), `JoinInstance` (the gate protocol, `InstanceBusyError`), `LockDisks` (`DiskBusyError`, `DescribeKey`), sidecars |
| `internal/identity/disk.go` | `DiskKeys` per OS (`diskKeysLinux` over sysfs and udev, `diskKeysMacOS` over `detectMacOSDisk`, the path key elsewhere), `IsDiskKey` |
| `internal/creds` | `machine_id` in `agent.json`, `MachineID` per OS, `MachineMismatchError`, `NewAgent` |
| `internal/store` | `ResetStreams` |
| `cmd/driveagent/locks.go` | `joinInstance`, `lockDisk`, `startNewAgent`; each command calls `joinInstance`; `scan --wait`; `login --new-agent`; `exitBusy = 5` |

## Tests

- **`internal/runlock`:** the lock dir (override, account home rather than `$HOME`, the `$HOME` fallback, `0700`); the instance (same dir and version join; another dir, another version, or joining alone while others run are refused; after everyone leaves another dir takes over and rewrites `instance.json`; a symlinked state dir is the same; a stale `instance.json` is ignored; 20 concurrent joins across two dirs let exactly one dir in; a stuck gate times out); disk locks (another disk is free; the same key is busy and names the holder; keys taken before a busy one are released; `--wait` waits and reports once; a cancelled wait; a busy lock without a sidecar).
- **`internal/identity`:** a fake sysfs with partitions, a whole-disk filesystem, LUKS over a partition, LVM over two disks, LVM on LUKS and a loop device; serials from the disk's or the partition's udev record; generic serials; tmpfs with and without a filesystem ID; no sysfs; the macOS fixtures (the real seagate plists give `serial:`, a generic serial or failing `ioreg` gives `dev:`, a network share or failing `diskutil` gives `path:`).
- **`internal/creds`:** a new `agent.json` records the machine; an old one adopts it, keeping its id; another machine is a `MachineMismatchError` naming `login --new-agent`; an unreadable machine id never mismatches; `NewAgent`; parsing `IOPlatformUUID`.
- **`internal/store`:** `ResetStreams` clears every drive's stream, marker and rejected entries, keeps the drives, and a new stream can start.
- **`cmd/driveagent`** (a temp lock dir for the whole run; each scan's disk is its drive root, unless a test says otherwise): a scan of a busy disk exits 5 naming the holder, sends nothing and creates no `state.db`; `--wait` waits, then scans and uploads; the note for a disk that can't be found; another state dir makes `sync`, `remote-status`, `login` and `report` exit 5, while the running state dir still works; another version exits 5; on another machine `sync`, `scan`, `remote-status` and `logout` refuse without a request and keep the login, then `login --new-agent` logs in as a new agent and `sync` re-uploads the drive on a new stream; `--new-agent` refuses while another process runs; the real CLI exits 5 and `version` still runs. The existing `TestTwoScansAtOnce` covers two disks at once.

**To verify manually**, on the Linux box (scans only read the drives):

1. Start a scan on seagate1, and in another terminal a scan of seagate1's `DBR_BOOT` partition (or the same folder under a second `--drive-id`): the second exits 5 naming the first.
2. Scan seagate1 and seagate2 at once: both run; `progress.py` still finds both.
3. With a scan running, run `driveagent sync`, `report` and `remote-status` with the same state dir: they run. Run `report --state-dir /tmp/x`: exit 5.
4. Copy a state dir to a Mac: `sync` refuses; `login --new-agent` re-uploads under a new agent. Use a test drive and the dev `agentserver`, not the prod account's drives.

## Rollout

1. Let running scans finish, then stop every `driveagent` on the machine; 0.4.x processes are invisible to the new locks. Replace the binary on each machine (the Linux box, both Macs).
2. First run on each machine: `agent.json` adopts the machine id; nothing else changes.
3. Raise `AGENTSERVER_LATEST_AGENT_VERSION` in the prod `.env`, as after every release. No server change is needed.
