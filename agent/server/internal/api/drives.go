package api

import (
	"net/http"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/wire"
)

// Limits on drive fields.
const (
	maxDriveIDLen  = 128
	maxRootLen     = 4096
	maxIdentityLen = 256
)

// driveID reads and checks the {drive_id} path segment (ServeMux has
// already unescaped it). It answers the request itself on failure.
func driveID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("drive_id")
	if id == "" || len(id) > maxDriveIDLen || !utf8.ValidString(id) || hasControl(id) {
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest,
			"drive_id must be 1-128 bytes of UTF-8 without control characters", nil)
		return "", false
	}
	return id, true
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// openDrive is PUT /agent/v1/drives/{drive_id}.
func (s *Server) openDrive(w http.ResponseWriter, r *http.Request) {
	id, ok := driveID(w, r)
	if !ok {
		return
	}
	var req wire.DriveOpenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	bad := func(msg string) {
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, msg, nil)
	}
	stream, err := uuid.Parse(req.StreamID)
	switch {
	case err != nil:
		bad("stream_id must be a UUID")
		return
	case req.DriveRoot == "" || len(req.DriveRoot) > maxRootLen || len(req.BackupRoot) > maxRootLen:
		bad("drive_root is required, and drive_root and backup_root are at most 4096 bytes")
		return
	case !utf8.ValidString(req.DriveRoot) || !utf8.ValidString(req.BackupRoot):
		bad("drive_root and backup_root must be UTF-8")
		return
	}
	if i := req.Identity; i != nil {
		for _, v := range []string{i.FSUUID, i.FSType, i.FSUUIDSource, i.HWSerial} {
			if len(v) > maxIdentityLen || !utf8.ValidString(v) || hasControl(v) {
				bad("identity fields must be short UTF-8 strings")
				return
			}
		}
		if i.FSUUIDSource != "" && i.FSUUIDSource != "linux" && i.FSUUIDSource != "macos" {
			bad(`identity.fs_uuid_source must be "linux" or "macos"`)
			return
		}
	}
	req.StreamID = stream.String()

	p := principal(r)
	resp, err := s.store.OpenDrive(r.Context(), p.UserID, p.AgentID, id, req)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// listDrives is GET /agent/v1/drives.
func (s *Server) listDrives(w http.ResponseWriter, r *http.Request) {
	drives, err := s.store.ListDrives(r.Context(), principal(r).AgentID)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, drives)
}
