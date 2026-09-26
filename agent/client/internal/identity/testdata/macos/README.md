Hand-written fixtures in Apple's documented plist format (diskutil info
-plist, ioreg -a). They are **not** captured from a real Mac, so the macOS
code path is unverified until driveagent runs on one (implementation plan,
M0 and M7). Replace them with real, masked outputs when that happens.
