package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jyothri/bhandaar/agent/wire"
)

// Limits on entry values. Paths have no fixed length limit in the schema
// (rows are keyed on a hash), but a sanity cap keeps one entry small.
const (
	maxNameBytes = 32 << 10
	minUnix      = -62135596800 // 0001-01-01T00:00:00Z
	maxUnix      = 253402300799 // 9999-12-31T23:59:59Z
)

// entry is a validated change, ready to apply.
type entry struct {
	v          int64
	kind, op   string
	key        []byte // path_key (file) or entry_key (dir_child); nil for scan_run
	parentKey  []byte // dir_child
	path       string // readable form ('\xHH' for invalid bytes)
	child      string
	rawPath    []byte // only when the path isn't valid UTF-8
	rawChild   []byte
	runID      int64
	change     wire.Change
	reason     string // why it was rejected ("" if valid)
	fromReject bool   // a rejected upsert applied as a delete
}

// prepare validates one change and computes its key. A rejected change
// still gets its key when the path could be decoded (so the server can
// replace the key's older row with a tombstone); otherwise key is nil.
func prepare(c wire.Change) *entry {
	e := &entry{v: c.V, kind: c.Kind, op: c.Op, change: c}
	reject := func(format string, a ...any) *entry {
		e.reason = fmt.Sprintf(format, a...)
		return e
	}

	switch c.Kind {
	case wire.KindFile, wire.KindDirChild:
	case wire.KindScanRun:
		if c.Op != wire.OpUpsert {
			return reject("scan_run entries can't be %q", c.Op)
		}
		return prepareRun(e, c)
	default:
		return reject("unknown kind %q", c.Kind)
	}
	if c.Op != wire.OpUpsert && c.Op != wire.OpDelete {
		return reject("unknown op %q", c.Op)
	}

	rawPath, display, raw, why := decodeName(c.Path, c.PathB64, "path")
	e.path, e.rawPath = display, raw
	if c.Kind == wire.KindFile && len(rawPath) > 0 {
		e.key = hash(rawPath)
	}
	if why != "" {
		return reject("%s", why)
	}
	if bytes.HasPrefix(rawPath, []byte("/")) {
		return reject("path must be relative to the drive root")
	}

	if c.Kind == wire.KindFile {
		if len(rawPath) == 0 {
			return reject("a file needs a path")
		}
		if c.Op == wire.OpDelete {
			return e
		}
		return prepareFile(e, c)
	}

	rawChild, childDisplay, rawC, why := decodeName(c.Child, c.ChildB64, "child")
	e.child, e.rawChild = childDisplay, rawC
	if rawChild != nil {
		e.key = hash(rawPath, []byte{0}, rawChild)
		e.parentKey = hash(rawPath)
	}
	switch {
	case why != "":
		return reject("%s", why)
	case len(rawChild) == 0:
		return reject("a dir_child needs a child name")
	case bytes.ContainsRune(rawChild, '/'), string(rawChild) == ".", string(rawChild) == "..":
		return reject("child %q isn't a single name", childDisplay)
	}
	if c.Op == wire.OpDelete {
		return e
	}
	switch {
	case c.IsDir == nil:
		return reject("is_dir is required")
	case c.FirstSeenAt == nil || !inRange(*c.FirstSeenAt):
		return reject("first_seen_at is missing or out of range")
	}
	return e
}

func prepareFile(e *entry, c wire.Change) *entry {
	reject := func(msg string) *entry { e.reason = msg; return e }
	switch {
	case c.Size == nil || *c.Size < 0:
		return reject("size is missing or negative")
	case c.MTimeUnix == nil || *c.MTimeUnix < minUnix || *c.MTimeUnix > maxUnix:
		return reject("mtime out of range")
	case c.Mode == nil || *c.Mode < 0 || *c.Mode > 1<<31-1:
		return reject("mode is missing or out of range")
	case c.ScannedAt == nil || !inRange(*c.ScannedAt):
		return reject("scanned_at is missing or out of range")
	}
	switch c.Status {
	case "hashed":
		if c.ContentHash == "" || c.HashAlgo == "" {
			return reject("a hashed file needs content_hash and hash_algo")
		}
	case "error":
	default:
		return reject(fmt.Sprintf("unknown status %q", c.Status))
	}
	return e
}

func prepareRun(e *entry, c wire.Change) *entry {
	reject := func(msg string) *entry { e.reason = msg; return e }
	switch {
	case c.RunID == nil || *c.RunID < 1:
		return reject("run_id is missing or not positive")
	case c.StartedAt == nil || !inRange(*c.StartedAt):
		return reject("started_at is missing or out of range")
	case c.FinishedAt != nil && !inRange(*c.FinishedAt):
		return reject("finished_at out of range")
	case c.FilesSeen != nil && *c.FilesSeen < 0, c.BytesHashed != nil && *c.BytesHashed < 0:
		return reject("counters can't be negative")
	}
	e.runID = *c.RunID
	return e
}

func inRange(t time.Time) bool {
	u := t.Unix()
	return u >= minUnix && u <= maxUnix
}

// decodeName reads a path or child name, given as plain UTF-8 or as base64
// of its raw bytes. It returns the raw bytes, the readable form, the raw
// bytes to store (nil for valid UTF-8), and why it's invalid (""). When the
// bytes could be decoded, raw is set even if the name is invalid, so the
// caller can still compute the key.
func decodeName(plain *string, b64, field string) (rawBytes []byte, display string, stored []byte, why string) {
	switch {
	case plain != nil && b64 != "":
		return nil, "", nil, fmt.Sprintf("%s and %s_b64 are both set", field, field)
	case b64 != "":
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, "", nil, fmt.Sprintf("%s_b64 isn't valid base64", field)
		}
		display = escapeInvalid(raw)
		if utf8.Valid(raw) {
			return raw, display, nil, fmt.Sprintf("%s_b64 decodes to valid UTF-8; send it as %s", field, field)
		}
		if why := checkName(raw, field); why != "" {
			return raw, display, raw, why
		}
		return raw, display, raw, ""
	case plain != nil:
		raw := []byte(*plain)
		if !utf8.ValidString(*plain) {
			return raw, escapeInvalid(raw), nil, fmt.Sprintf("%s isn't valid UTF-8; send it as %s_b64", field, field)
		}
		return raw, *plain, nil, checkName(raw, field)
	}
	return nil, "", nil, fmt.Sprintf("%s is required", field)
}

func checkName(raw []byte, field string) string {
	switch {
	case bytes.IndexByte(raw, 0) >= 0:
		return field + " contains a NUL byte"
	case len(raw) > maxNameBytes:
		return fmt.Sprintf("%s is longer than %d bytes", field, maxNameBytes)
	}
	return ""
}

// escapeInvalid returns b with each byte that isn't part of valid UTF-8
// written as \xHH. It's for display only; keys are hashes of the raw bytes.
func escapeInvalid(b []byte) string {
	var s strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size <= 1 {
			fmt.Fprintf(&s, `\x%02X`, b[0])
			b = b[1:]
			continue
		}
		s.Write(b[:size])
		b = b[size:]
	}
	return s.String()
}

func hash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}
