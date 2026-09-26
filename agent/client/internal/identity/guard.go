package identity

import "fmt"

// Decision is what the wrong-drive guard concluded.
type Decision struct {
	// Refusal, when set, means stop before scanning: a different filesystem
	// under a known --drive-id. It says why and how to override.
	Refusal string
	// Record is the identity to store (when Save is true).
	Record Identity
	Save   bool
	// Warnings are printed and the scan goes ahead.
	Warnings []string
}

// Check compares the identity stored for driveID with the one found at
// root (docs/specs/remote-sync-agent.md, "Wrong-drive guard"):
//
//   - both have a filesystem ID from the same source, and they differ:
//     refuse, unless acceptChange, which records the new identity;
//   - same filesystem ID, different serial: warn (USB bridges often report
//     their own serial, so a new dock changes it) and record it;
//   - nothing stored: record what was found;
//   - something stored but not found this time: warn and keep it.
//
// A value found replaces the stored one field by field; a field not found
// keeps its stored value.
func Check(driveID, root string, stored, found Identity, acceptChange bool) Decision {
	var d Decision
	if stored.FSUUID != "" && found.FSUUID != "" && stored.Source == found.Source && stored.FSUUID != found.FSUUID {
		if !acceptChange {
			return Decision{Refusal: fmt.Sprintf(
				"drive %q was last scanned on filesystem %s; the drive at %s has %s. Wrong drive? "+
					"If it was reformatted, re-run with --accept-identity-change (or use --replace-root to start the drive over)",
				driveID, stored.FSUUID, root, found.FSUUID)}
		}
		d.Warnings = append(d.Warnings, fmt.Sprintf("drive %q: accepting a new identity, %s (was %s)", driveID, found, stored))
		return Decision{Record: found, Save: true, Warnings: d.Warnings}
	}

	merged := stored
	if found.FSUUID != "" {
		merged.FSUUID, merged.FSType, merged.Source = found.FSUUID, found.FSType, found.Source
	} else if found.FSType != "" && merged.FSType == "" {
		merged.FSType = found.FSType
	}
	if found.HWSerial != "" {
		if stored.HWSerial != "" && stored.HWSerial != found.HWSerial {
			d.Warnings = append(d.Warnings, fmt.Sprintf(
				"drive %q: the disk now reports serial %s (it was %s). That's expected after moving it to another USB enclosure or dock; recording the new serial",
				driveID, found.HWSerial, stored.HWSerial))
		}
		merged.HWSerial = found.HWSerial
	}

	switch {
	case found.Empty() && stored.Empty():
		d.Warnings = append(d.Warnings, fmt.Sprintf(
			"drive %q: found no filesystem ID or disk serial at %s; the scan goes ahead, but this drive can't be linked to its copies on other machines",
			driveID, root))
	case stored.FSUUID != "" && found.FSUUID == "", stored.HWSerial != "" && found.HWSerial == "":
		d.Warnings = append(d.Warnings, fmt.Sprintf(
			"drive %q: couldn't read all of its identity this time (found %s); keeping the stored %s", driveID, found, stored))
	}
	d.Record, d.Save = merged, merged != stored
	return d
}
