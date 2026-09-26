package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/wire"
)

// loggedIn returns a client with a valid access token for jyothri.
func loggedIn(t *testing.T, srv *Server) *client {
	t.Helper()
	cl := newClient(t, srv)
	tr := decodeTokens(t, cl.login("jyothri", "correct horse battery"))
	cl.headers = map[string]string{"Authorization": "Bearer " + tr.AccessToken}
	return cl
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func TestDrivesNeedAuth(t *testing.T) {
	srv, _ := dbServer(t)
	cl := newClient(t, srv)
	wantStatus(t, cl.do("GET", "/agent/v1/drives", nil), 401, wire.CodeInvalidToken)
	wantStatus(t, cl.do("PUT", "/agent/v1/drives/d1", wire.DriveOpenRequest{StreamID: uuid.NewString(), DriveRoot: "/d"}), 401, wire.CodeInvalidToken)
}

func TestOpenAndListDrives(t *testing.T) {
	srv, _ := dbServer(t)
	cl := loggedIn(t, srv)
	stream := uuid.NewString()
	req := wire.DriveOpenRequest{StreamID: stream, DriveRoot: "/mnt/seagate2", BackupRoot: "Jyo/Backup",
		Identity: &wire.Identity{FSUUID: "e856-bf7e", FSType: "ntfs", FSUUIDSource: "linux", HWSerial: "NA95"}}
	rec := cl.do("PUT", "/agent/v1/drives/seagate2", req)
	wantStatus(t, rec, 200, "")
	open := decode[wire.DriveOpenResponse](t, rec)
	if open.DriveID != "seagate2" || open.StreamID != stream || open.Reset || open.PhysicalDrive == nil {
		t.Errorf("open = %+v", open)
	}
	if !strings.Contains(rec.Body.String(), `"acked_ranges":[]`) {
		t.Errorf("acked_ranges should be an empty array: %s", rec.Body)
	}

	// Escaped drive ids are unescaped by the router.
	wantStatus(t, cl.do("PUT", "/agent/v1/drives/my%20drive", wire.DriveOpenRequest{StreamID: uuid.NewString(), DriveRoot: "/x"}), 200, "")

	rec = cl.do("GET", "/agent/v1/drives", nil)
	wantStatus(t, rec, 200, "")
	list := decode[[]wire.Drive](t, rec)
	if len(list) != 2 || list[0].DriveID != "my drive" || list[1].DriveID != "seagate2" || list[1].BackupRoot != "Jyo/Backup" {
		t.Errorf("list = %+v", list)
	}
}

func TestOpenDriveValidation(t *testing.T) {
	srv, _ := dbServer(t)
	cl := loggedIn(t, srv)
	good := func() wire.DriveOpenRequest {
		return wire.DriveOpenRequest{StreamID: uuid.NewString(), DriveRoot: "/d"}
	}
	cases := map[string]struct {
		path string
		req  wire.DriveOpenRequest
	}{
		"stream not a uuid": {"/agent/v1/drives/d", wire.DriveOpenRequest{StreamID: "s", DriveRoot: "/d"}},
		"no drive root":     {"/agent/v1/drives/d", wire.DriveOpenRequest{StreamID: uuid.NewString()}},
		"long drive id":     {"/agent/v1/drives/" + strings.Repeat("x", 129), good()},
		"control char":      {"/agent/v1/drives/a%01b", good()},
		"bad source": {"/agent/v1/drives/d", wire.DriveOpenRequest{StreamID: uuid.NewString(), DriveRoot: "/d",
			Identity: &wire.Identity{FSUUIDSource: "windows"}}},
	}
	for name, c := range cases {
		if rec := cl.do("PUT", c.path, c.req); rec.Code != 400 {
			t.Errorf("%s: status %d %s", name, rec.Code, rec.Body)
		}
	}
}
