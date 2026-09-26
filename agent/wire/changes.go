package wire

import "time"

// Change kinds and ops.
const (
	KindFile     = "file"
	KindDirChild = "dir_child"
	KindScanRun  = "scan_run"

	OpUpsert = "upsert"
	OpDelete = "delete"
)

// Change is one entry of a drive's change feed. Which fields are set
// depends on Kind and Op (docs/specs/remote-sync-agent.md, "Wire mapping"):
//
//	file upsert:      Path, Size, MTimeUnix, Mode, ContentHash, HashAlgo, Status, ErrorMessage, ScannedAt
//	file delete:      Path
//	dir_child upsert: Path (the parent; "" is the drive root), Child, IsDir, FirstSeenAt
//	dir_child delete: Path, Child
//	scan_run upsert:  RunID, StartedAt, FinishedAt, FilesSeen, BytesHashed, Interrupted
//
// A path or child name that isn't valid UTF-8 is sent as standard base64 of
// its bytes in PathB64 / ChildB64 instead (never both).
type Change struct {
	V    int64  `json:"v"`
	Kind string `json:"kind"`
	Op   string `json:"op"`

	Path     *string `json:"path,omitempty"`
	PathB64  string  `json:"path_b64,omitempty"`
	Child    *string `json:"child,omitempty"`
	ChildB64 string  `json:"child_b64,omitempty"`

	Size         *int64     `json:"size,omitempty"`
	MTimeUnix    *int64     `json:"mtime_unix,omitempty"`
	Mode         *int64     `json:"mode,omitempty"`
	ContentHash  string     `json:"content_hash,omitempty"`
	HashAlgo     string     `json:"hash_algo,omitempty"`
	Status       string     `json:"status,omitempty"` // hashed | error
	ErrorMessage string     `json:"error_message,omitempty"`
	ScannedAt    *time.Time `json:"scanned_at,omitempty"`

	IsDir       *bool      `json:"is_dir,omitempty"`
	FirstSeenAt *time.Time `json:"first_seen_at,omitempty"`

	RunID       *int64     `json:"run_id,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	FilesSeen   *int64     `json:"files_seen,omitempty"`
	BytesHashed *int64     `json:"bytes_hashed,omitempty"`
	Interrupted *bool      `json:"interrupted,omitempty"`
}

// ChangeBatch is the body of POST /agent/v1/drives/{drive_id}/changes
// (gzip-compressed). It covers (FromVersion, ToVersion]: Changes holds every
// entry of the drive's feed in that interval, sorted by V.
type ChangeBatch struct {
	StreamID    string   `json:"stream_id"`
	FromVersion int64    `json:"from_version"`
	ToVersion   int64    `json:"to_version"`
	Changes     []Change `json:"changes"`
}

// ChangesResponse answers a change batch. The batch's range is acknowledged
// even when some entries are rejected.
type ChangesResponse struct {
	AckedRanges []Range    `json:"acked_ranges"`
	Applied     int        `json:"applied"`
	Skipped     int        `json:"skipped"`
	Duplicate   bool       `json:"duplicate"`
	Rejected    []Rejected `json:"rejected"`
}

// Rejected is an entry the server couldn't store.
type Rejected struct {
	V      int64  `json:"v"`
	Reason string `json:"reason"`
}

// Ptr returns a pointer to v, for the optional fields.
func Ptr[T any](v T) *T { return &v }
