macOS fixtures for `detectMacOS`.

- `*-real*.plist`: captured on 2026-09-27 from a MacBook Pro (Intel, macOS
  13.7.8), from two NTFS drives mounted read-only by Paragon NTFS for Mac.
  Serials (`NA77MASK`, `NA95MASK`) and the volume UUID are masked.
  - seagate1 (Seagate "BUP Slim BK" enclosure): `diskutil-ntfs-real.plist`
    is `diskutil info -plist /Volumes/Seagate1`; `ioreg-l-real.plist` is
    `ioreg -a -l -r -c IOUSBHostDevice` and `ioreg-no-l-real.plist` the same
    without `-l`, trimmed to that enclosure's device.
  - seagate2 (Seagate "Ultra Slim MT" enclosure), whose NTFS volume has no
    `VolumeUUID` on macOS: `diskutil-ntfs-nouuid-real.plist` and
    `ioreg-l-real-2.plist`.
- `*-real-arm64.plist`: seagate1 again, captured the same day on an Apple
  Silicon MacBook (macOS 26.6.2), mounted read-only by macOS's own FSKit
  NTFS driver: `diskutil info -plist /Volumes/Seagate1` and
  `ioreg -a -l -r -c IOUSBHostDevice`, trimmed to the enclosure, masked the
  same way.
- The others are hand-written in Apple's documented plist format (APFS,
  exFAT, a network share) and not verified on a real Mac.
