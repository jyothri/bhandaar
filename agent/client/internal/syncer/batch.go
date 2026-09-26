package syncer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jyothri/bhandaar/agent/wire"
)

// Defaults for a batch; the handshake's limits can only lower them.
const (
	DefaultMaxChanges = 1000
	DefaultMaxBytes   = 1 << 20
)

// envelopeBytes is room for a batch's JSON around its changes.
const envelopeBytes = 256

// batch is one change batch, encoded once: every retry sends body as is.
type batch struct {
	from, to int64
	entries  []Entry
	body     []byte // gzipped JSON
	key      string // Idempotency-Key
	json     int    // bytes of JSON before gzip
}

// session is where a batch comes from: the drive's stream, and the
// interval being uploaded.
type session struct {
	agentID, driveID, streamID string
	// upper bounds the session: a sync session uploads the gap (from,
	// upper]. closeGap stretches the last batch's to_version to upper, so
	// the range meets the next one exactly.
	upper    int64
	closeGap bool
}

// build makes the next batch from a page read after cursor. more reports
// whether the page stopped before the end of the session's interval.
// Entries are added until their JSON would pass maxBytes (always at least
// one). It returns nil when there's nothing to send: no entries, and no gap
// left to close (a batch can't have from == to).
func (s session) build(cursor int64, page []Entry, more bool, maxBytes int) (*batch, error) {
	size := envelopeBytes
	n := 0
	for _, e := range page {
		b, err := json.Marshal(e.Change)
		if err != nil {
			return nil, err
		}
		if n > 0 && size+len(b)+1 > maxBytes {
			more = true
			break
		}
		size += len(b) + 1
		n++
	}
	page = page[:n]

	to := cursor
	if n > 0 {
		to = page[n-1].Change.V
	}
	if s.closeGap && !more {
		to = s.upper
	}
	if to <= cursor {
		return nil, nil
	}

	changes := make([]wire.Change, n)
	for i, e := range page {
		changes[i] = e.Change
	}
	raw, err := json.Marshal(wire.ChangeBatch{StreamID: s.streamID, FromVersion: cursor, ToVersion: to, Changes: changes})
	if err != nil {
		return nil, err
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return &batch{from: cursor, to: to, entries: page, body: gz.Bytes(), key: s.key(cursor, to), json: len(raw)}, nil
}

// key is the batch's Idempotency-Key: the same for every retry of the same
// (from, to], including after a restart.
func (s session) key(from, to int64) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%d|%d", s.agentID, s.driveID, s.streamID, from, to))
	return hex.EncodeToString(sum[:])
}
