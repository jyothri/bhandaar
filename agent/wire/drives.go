package wire

import "time"

// Identity is a drive's identity as the agent found it; every field is
// optional.
type Identity struct {
	FSUUID       string `json:"fs_uuid,omitempty"`
	FSType       string `json:"fs_type,omitempty"`
	FSUUIDSource string `json:"fs_uuid_source,omitempty"` // "linux" or "macos"
	HWSerial     string `json:"hw_serial,omitempty"`
}

// Range is an interval of feed versions, (Range[0], Range[1]]: exclusive,
// inclusive.
type Range [2]int64

// DriveOpenRequest is the body of PUT /agent/v1/drives/{drive_id}.
type DriveOpenRequest struct {
	StreamID   string    `json:"stream_id"`
	DriveRoot  string    `json:"drive_root"`
	BackupRoot string    `json:"backup_root"`
	Identity   *Identity `json:"identity,omitempty"`
}

// DriveOpenResponse answers PUT /agent/v1/drives/{drive_id}.
type DriveOpenResponse struct {
	DriveID       string         `json:"drive_id"`
	StreamID      string         `json:"stream_id"`
	AckedRanges   []Range        `json:"acked_ranges"`
	Reset         bool           `json:"reset"`
	PhysicalDrive *PhysicalDrive `json:"physical_drive"` // null when the drive has no filesystem ID
}

// PhysicalDrive is the real drive a drive row was matched to.
type PhysicalDrive struct {
	ID      int64  `json:"id"`
	CloneOf *int64 `json:"clone_of"` // the drive this one was cloned from, if known
	// Linked are this user's drive rows on other agents matched to the
	// same physical drive.
	Linked []LinkedDrive `json:"linked"`
}

// LinkedDrive is another agent's copy of the same physical drive.
type LinkedDrive struct {
	Hostname     string     `json:"hostname"`
	DriveID      string     `json:"drive_id"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
}

// Drive is one entry of GET /agent/v1/drives (a JSON array of them).
type Drive struct {
	DriveID       string         `json:"drive_id"`
	StreamID      string         `json:"stream_id"`
	AckedRanges   []Range        `json:"acked_ranges"`
	DriveRoot     string         `json:"drive_root"`
	BackupRoot    string         `json:"backup_root"`
	LastSyncedAt  *time.Time     `json:"last_synced_at"`
	PhysicalDrive *PhysicalDrive `json:"physical_drive"`
}
