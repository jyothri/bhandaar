# Remote Sync: Follow-ups

**Status:** open items, collected on 2026-09-27 when remote sync shipped (0.4.1). Each links to where it came from. When one is done, remove it here and update the reference spec it touches ([`remote-sync.md`](remote-sync.md), [agent](remote-sync-agent.md), [server](remote-sync-server.md), [CI](remote-sync-ci.md)).

## Proposed, with a spec

1. **Link FAT, exFAT and NTFS drives across Linux and macOS.** Today they link only within one OS, and an NTFS drive with no volume UUID on macOS isn't matched at all. The spec adds a partition key (disk serial, partition offset and size): [`remote-sync-cross-os-linking.md`](remote-sync-cross-os-linking.md). It's also the one M7 check still open (a Linux/macOS link).

## Deferred fixes for the operational rules

These are rules today ([`remote-sync.md`](remote-sync.md#operational-rules)); the agent doesn't detect breaking them. (Detecting a state dir copied to another machine shipped in 0.5.0: [`agent-hardening.md`](../archive/agent-hardening.md).)

2. **Stop an old `driveagent` from writing to a migrated `state.db`.** For example, have a future migration move the database to a new file name (`state.v2.db`), so an older binary finds nothing and starts a fresh, obviously empty one, instead of writing rows that are never uploaded.

## Features

3. **Agent drives in the web UI**: the drives, their sync state and linked copies, read from the `agent_*` tables (in `be`/`ui`).
4. **Server-side compare**: compare drives from the uploaded hashes, instead of only locally with `driveagent compare`.
5. **A thinner agent** (later, and only after 4): with the server as the system of record, drop the embedded SQLite, `compare` and `report`. Two things would need replacing: resumability (ask the server for a drive's known `(path, size, mtime)`, or re-hash) and the change feed (without a local store, a failed upload means re-scanning).

## Smaller items

6. **An admin command to remove a drive's (or an agent's) data from the server.** There's none; a test drive's rows can only be deleted by hand in Postgres.
7. **Record the agent's version after login too.** `agent_agents.last_version` is set only at login, so an agent upgraded without logging in again shows its old version (seen with the Intel Mac in M7). Updating it on refresh or on each `PUT /drives` would fix that.
8. **Automate `AGENTSERVER_LATEST_AGENT_VERSION`**, e.g. from the GitHub API's latest `driveagent/v*` release, instead of setting it by hand after each release.
9. **Real macOS fixtures for APFS and exFAT.** The macOS identity code is verified on NTFS (Intel and Apple Silicon); its APFS and exFAT cases are still tested against hand-written fixtures.

## Pending actions (not code)

10. **Scan the two Seagates once on the Linux box** with 0.4.0 or later: they were last scanned before 0.2.0, so the server has no identity for this box's copies. Needed before item 1 can link them.
