# Remote Sync: Linking Drives Across Linux and macOS

**Status:** proposed. Written 2026-09-27, after M7 ([implementation plan](../archive/remote-sync/remote-sync-implementation-plan.md#m7--rollout-verification-and-follow-ups-rollout-step-7)). It extends physical-drive matching in [`remote-sync-server.md`](remote-sync-server.md#matching-physical-drives) and drive identity in [`remote-sync-agent.md`](remote-sync-agent.md#drive-identity).

## Problem

The server links two agents' drive rows to one physical drive when their **filesystem IDs** match. For ext4, APFS and HFS+ that works across operating systems, because Linux and macOS both report the filesystem's own UUID. For **FAT, exFAT and NTFS** they don't:

- Linux reports the volume serial: 16 hex digits for NTFS, e.g. `8650…E771`.
- macOS reports something else, or nothing. With Paragon NTFS for Mac on macOS 13.7.8, seagate1 got a GUID (`21782368-…`, apparently its NTFS volume object ID), and seagate2 no `VolumeUUID` at all.
- Reading the raw serial on macOS needs root, which the agent doesn't use.

So v1 links these filesystems only within one OS. Portable drives, the ones that move between machines, are exactly the ones usually formatted exFAT or NTFS. Both backup drives are NTFS, so the Linux and Mac copies of seagate1 and seagate2 stay unlinked. A seagate2 scanned on the Mac isn't matched to any physical drive at all (`physical_drive_id` null), because it has no filesystem ID.

## Idea

Identify the **partition** in a way both OSes agree on, without root: the disk's **serial** plus the partition's **byte offset** and **byte size** on the disk. Call it the **partition key**. M7 checked it on both drives:

| | Linux (udev record of the partition) | macOS (`diskutil info -plist`) |
|---|---|---|
| Serial | `ID_SERIAL_SHORT`: `NA77…T6`, `NA95…KP` | `ioreg` USB serial: `NA77YET6`, `NA95K2KP` |
| Offset | `ID_PART_ENTRY_OFFSET` = 2048 sectors (both drives) | `PartitionMapPartitionOffset` = 1,048,576 bytes (both drives) |
| Size, seagate1 | `ID_PART_ENTRY_SIZE` = 3,905,993,857 sectors = **1,999,868,854,784** bytes | `Size` = **1,999,868,854,784** bytes |
| Size, seagate2 | 3,907,024,896 sectors = **2,000,396,746,752** bytes | **2,000,396,746,752** bytes |

The macOS column holds on two Macs with different NTFS drivers: an Intel Mac on macOS 13.7.8 with Paragon NTFS for Mac, and an Apple Silicon Mac on macOS 26.6.2 with macOS's own FSKit driver. Both gave seagate1 the same offset, size, serial and volume GUID. Linking by that GUID already works between the two Macs (both of them macOS).

udev counts in 512-byte sectors (libblkid's unit, whatever the disk's block size); macOS reports bytes. The partition's size is used, not the filesystem's: `ID_FS_SIZE` and `diskutil`'s `VolumeSize` differ for seagate2, because the two NTFS drivers compute it differently.

The offset tells apart two partitions of one disk: seagate1 also has a small FAT32 partition, `DBR_BOOT`, which shares the disk's serial. The size and serial tell apart different disks.

## Agent

**Identity** (`internal/identity`) gains two optional fields, `PartOffset` and `PartSize`, in bytes. Both are set only together, and only for a partition of a partitioned disk:
- **Linux:** from the partition's udev record, the one already read: `ID_PART_ENTRY_OFFSET` and `ID_PART_ENTRY_SIZE`, times 512. Without a udev record (in a container), from `/sys/dev/block/<major>:<minor>/start` and `size`, also in 512-byte units. A filesystem on a whole disk, with no partition table, has neither: no partition key.
- **macOS:** from the same `diskutil info -plist` output: `PartitionMapPartitionOffset` and `Size`, when `PartitionMapPartition` is true. (`ioreg`'s `IOMedia` `Base` and `Size` carry the same numbers, but `diskutil` is enough.)

**`state.db`**: migration 2 adds `drives.part_offset` and `drives.part_size` (`INTEGER`, NULL when unknown), recorded by `SetDriveIdentity` like the other identity columns.

**Wrong-drive guard:** unchanged. The partition key is recorded silently, and a changed one is recorded without a warning. The filesystem ID stays what the guard refuses on. A reformatted drive keeps its partition key, and a repartitioned one keeps neither.

**Wire** (`agent/wire`): `Identity` gains `part_offset` and `part_size` (`int64`, `omitempty`). `sync` sends the stored values, like the rest of the identity.

## Server

**Migration 4:** `agent_drives` gains `part_offset` and `part_size` (`BIGINT`), stored from each `PUT` like the other identity fields; an index on `agent_drives (hw_serial, part_offset, part_size)`. Physical drives need no new columns: the drive rows linked to a physical drive are its per-OS identities.

**Validation:** both fields present or both absent, `part_offset >= 0`, `part_size > 0`; otherwise `400 INVALID_REQUEST`.

**Matching** keeps today's rules and adds a second step, used where today's rules give up:

1. **By filesystem ID**, exactly as today. If there are candidates, the result is final: the same drive, a clone, and so on.
2. **By partition key**, when step 1 had no filesystem ID to match on, or found no candidates. This applies only when the request has a partition key and a serial, and the serial isn't on the denylist. A candidate is another drive row of the same user that:
   - has the same serial, `part_offset` and `part_size`;
   - has the same filesystem type, when both are known;
   - is **not** a different filesystem on the same OS: a row with the same `fs_uuid_source` and a different non-empty `fs_uuid` is excluded (the disk was reformatted, or it's a clone), and so is a physical drive that has such a row.

   Then:
   - a candidate linked to a physical drive → link to that physical drive (the oldest, if there are several);
   - candidates, none linked → create a physical drive from the request's identity and link the request **and those rows** to it;
   - no candidates → as today: a new physical drive if the request has a filesystem ID, otherwise not linked.
3. The `sameSourceTypes` comment in `internal/store/drives.go` is corrected: macOS doesn't always synthesize a `VolumeUUID` for these filesystems; it may report none.

Matching runs when a drive's identity is new or changed, as today. A partition key arriving for the first time counts as a change, so drives already on the server are linked the next time they're scanned with the new agent.

**What happens to the Seagates:**
- **seagate1:** the Mac's row is physical drive 1, found by its GUID. When the Linux box scans seagate1 with the new agent, step 1 finds nothing (the Linux serial-style ID differs, and NTFS requires the same source), and step 2 finds the Mac row: same serial, offset 1,048,576, size 1,999,868,854,784. Linked.
- **seagate2:** the Mac's row has no filesystem ID and no physical drive. The Linux scan's step 1 finds nothing, and step 2 finds the Mac row. The server creates a physical drive from the Linux identity and links both.
- **Order doesn't matter:** had Linux been first, the Mac's `PUT` would find the Linux row in step 2.

## Limitations

- **A dock that reports its own serial.** Some USB-SATA bridges report the bridge's serial, not the disk's. Two disks of the same model, partitioned the same way and used in the same dock, then have equal partition keys, and step 2 would link them. Within one OS the exclusion rule catches it, as long as both have filesystem IDs; across OSes it doesn't. The effect is a wrong grouping, never mixed data: each drive row keeps its own files, as with the [known limitation](remote-sync-server.md#matching-physical-drives) of today's rules. Enclosures with the disk built in (like the Seagates) report the disk's serial.
- **A disk moved to another enclosure** changes its serial on the bridges that report their own, so the partition key no longer matches. Same-OS linking by filesystem ID still works.
- **Unpartitioned disks** (a filesystem on the whole device, as some cameras format cards) have no partition key.
- **Drives scanned before this change** have no partition key until they're scanned again with the new agent. `sync` sends the stored identity, so it doesn't help until a scan has recorded the key.

## Rollout

`PUT /drives` decodes JSON leniently, so an older server ignores the new fields and an older agent just doesn't send them: either side can go first. Linking starts once both are updated.
- **agentserver:** migration 4 and the matching change. The image is released on merge, then the user redeploys.
- **driveagent:** the identity fields, the `state.db` migration and the wire fields. Version **0.5.0** (a minor release: new behaviour).
- **Afterwards:** scan each drive once with 0.5.0 on each machine. For the Seagates, that's a scan of any folder on each drive on the Linux box (which also records their first identity there) and on the Mac.

## Testing

- **Contract, from real captures:** the M0 udev records (`identity/testdata/linux/udev/b8:33`, `b8:17`) and the Mac's `diskutil` captures (`identity/testdata/macos/diskutil-ntfs-real.plist`, `diskutil-ntfs-nouuid-real.plist`) give the same `PartOffset` and `PartSize` for seagate1 and seagate2. The serials are masked differently in the two sets, so the test compares offsets and sizes.
- **Agent:**
  - no udev record → sysfs;
  - a whole-disk filesystem → no key;
  - `diskutil` with `PartitionMapPartition` false → no key;
  - the `state.db` migration, and the key recorded and sent by `scan` and `sync`.
- **Server matching:**
  - Linux first then Mac, and the reverse;
  - a Mac row with no filesystem ID adopted into a new physical drive;
  - two partitions of one disk not linked;
  - a denylisted or missing serial → no step 2;
  - a different filesystem type → no match;
  - the same-OS exclusion (a reformatted disk, same partition key, new filesystem ID → a new physical drive);
  - a step-1 match left untouched by step 2;
  - an older agent's `PUT` without the fields;
  - validation of the two fields.
- **End to end** (M7 item 6 again): scan seagate1 and seagate2 on the Linux box and on the Mac with 0.5.0; `remote-status` on each shows the other machine's copy as `linked`.

## Alternatives considered

- **Manual linking** (an `agentserver drive link` command, or a flag on the agent): never wrong, but a chore per drive, and easy to forget. It could still be added later, to override step 2 when it's wrong.
- **Reading the NTFS or FAT serial from the raw device on macOS:** it needs root (`/dev/rdiskN`), which the agent deliberately doesn't use.
- **The filesystem size** instead of the partition size: the NTFS drivers disagree (seagate2: 2,000,396,746,240 bytes on Linux against 2,000,396,742,656 on macOS).
- **The GPT partition GUID** (`ID_PART_ENTRY_UUID` on Linux): globally unique, and it would need no serial. It doesn't exist for MBR disks like the Seagates, and it's unverified whether `diskutil` reports it on macOS. It's worth adding as a stronger key once someone checks a GPT drive on a Mac.
