macOS fixtures for `detectMacOS`.

- `*-real.plist`: captured on 2026-09-27 from a MacBook Pro (Intel, macOS
  13.7.8) with seagate1 attached: an NTFS drive in a Seagate "BUP Slim BK"
  USB enclosure, mounted read-only by Paragon NTFS for Mac. The disk serial
  (`NA77MASK`) and the volume UUID are masked. `ioreg-l-real.plist` is
  `ioreg -a -l -r -c IOUSBHostDevice` and `ioreg-no-l-real.plist` the same
  without `-l`, trimmed to that enclosure's device; `diskutil-ntfs-real.plist`
  is `diskutil info -plist /Volumes/Seagate1`.
- The others are hand-written in Apple's documented plist format (APFS,
  exFAT, a network share) and not verified on a real Mac.
