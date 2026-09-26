package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/server/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

// Size limits on a change batch: compressed (the request body) and after
// decompression, enforced while reading so a gzip bomb can't use more.
const (
	MaxChangesBody         = 2 << 20
	MaxChangesDecompressed = 16 << 20
)

// postChanges is POST /agent/v1/drives/{drive_id}/changes.
func (s *Server) postChanges(w http.ResponseWriter, r *http.Request) {
	id, ok := driveID(w, r)
	if !ok {
		return
	}
	body, ok := readChanges(w, r)
	if !ok {
		return
	}

	var batch wire.ChangeBatch
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&batch); err != nil || dec.More() {
		if err == nil {
			err = errors.New("unexpected data after the batch")
		}
		writeError(w, http.StatusBadRequest, wire.CodeInvalidBatch, "invalid batch JSON: "+err.Error(), nil)
		return
	}
	if msg := checkEnvelope(batch); msg != "" {
		writeError(w, http.StatusBadRequest, wire.CodeInvalidBatch, msg, nil)
		return
	}
	if len(batch.Changes) > MaxChangesPerBatch {
		// 413, so the agent halves its batches.
		writeError(w, http.StatusRequestEntityTooLarge, wire.CodePayloadTooLarge,
			fmt.Sprintf("at most %d changes per batch", MaxChangesPerBatch), nil)
		return
	}

	sum := sha256.Sum256(body)
	p := principal(r)
	resp, err := s.store.ApplyChanges(r.Context(), store.ChangesRequest{
		UserID: p.UserID, AgentID: p.AgentID, DriveID: id,
		IdempotencyKey: r.Header.Get(wire.HeaderIdempotencyKey), RequestSHA256: sum[:],
		Batch: batch,
	})
	var mismatch *store.StreamMismatchError
	switch {
	case errors.Is(err, store.ErrDriveNotOpen):
		writeError(w, http.StatusNotFound, wire.CodeDriveNotOpen, "open the drive with PUT first", nil)
	case errors.As(err, &mismatch):
		writeError(w, http.StatusConflict, wire.CodeStreamMismatch, "the drive has a different stream; re-open it",
			map[string]any{"stream_id": mismatch.StreamID})
	case errors.Is(err, store.ErrIdempotencyKeyReused):
		writeError(w, http.StatusUnprocessableEntity, wire.CodeIdempotencyKeyReused,
			"this Idempotency-Key was used for a different batch", nil)
	case err != nil:
		writeInternal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, resp)
	}
}

// readChanges reads the body, gunzipping it if needed, within the limits.
// It answers the request itself on failure.
func readChanges(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	var src io.Reader = r.Body
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			if tooLarge(w, err) {
				return nil, false
			}
			writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "invalid gzip body", nil)
			return nil, false
		}
		defer zr.Close()
		src = zr
	default:
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "unsupported Content-Encoding "+enc, nil)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(src, MaxChangesDecompressed+1))
	if err != nil {
		if tooLarge(w, err) {
			return nil, false
		}
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "reading the body: "+err.Error(), nil)
		return nil, false
	}
	if len(body) > MaxChangesDecompressed {
		writeError(w, http.StatusRequestEntityTooLarge, wire.CodePayloadTooLarge,
			fmt.Sprintf("the batch is larger than %d bytes decompressed", MaxChangesDecompressed), nil)
		return nil, false
	}
	return body, true
}

func tooLarge(w http.ResponseWriter, err error) bool {
	var mbe *http.MaxBytesError
	if !errors.As(err, &mbe) {
		return false
	}
	writeError(w, http.StatusRequestEntityTooLarge, wire.CodePayloadTooLarge,
		fmt.Sprintf("the request body is larger than %d bytes", mbe.Limit), nil)
	return true
}

// checkEnvelope validates what a batch claims, as opposed to its entries: a
// broken envelope is a client bug, answered with 400 and not applied.
func checkEnvelope(b wire.ChangeBatch) string {
	if _, err := uuid.Parse(b.StreamID); err != nil {
		return "stream_id must be a UUID"
	}
	if b.FromVersion < 0 || b.FromVersion >= b.ToVersion {
		return fmt.Sprintf("need 0 <= from_version < to_version, got (%d, %d]", b.FromVersion, b.ToVersion)
	}
	prev := b.FromVersion
	for i, c := range b.Changes {
		switch {
		case c.V <= prev && i > 0:
			return fmt.Sprintf("changes must be sorted by v with no repeats (v %d after %d)", c.V, prev)
		case c.V <= b.FromVersion || c.V > b.ToVersion:
			return fmt.Sprintf("v %d is outside (%d, %d]", c.V, b.FromVersion, b.ToVersion)
		}
		prev = c.V
	}
	return ""
}
