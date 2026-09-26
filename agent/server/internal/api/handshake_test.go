package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jyothri/bhandaar/agent/wire"
)

func TestHandshakeDecisions(t *testing.T) {
	cfg := testConfig(t, map[string]string{
		"AGENTSERVER_MIN_AGENT_VERSION":    "0.2.0",
		"AGENTSERVER_LATEST_AGENT_VERSION": "0.3.1",
	})
	cases := []struct {
		version   string
		protocols []int
		decision  string
		protocol  int
		download  bool
	}{
		{"0.3.1", []int{1}, wire.DecisionOK, 1, false},
		{"0.4.0", []int{1}, wire.DecisionOK, 1, false}, // newer than the server knows
		{"0.3.0", []int{1}, wire.DecisionUpgradeRecommended, 1, true},
		{"0.2.0", []int{1}, wire.DecisionUpgradeRecommended, 1, true},
		{"0.1.9", []int{1}, wire.DecisionUpgradeRequired, 1, true},
		{"0.9.0", []int{2, 3}, wire.DecisionUnsupportedProtocol, 0, false},
		{"0.9.0", []int{1, 2}, wire.DecisionOK, 1, false},
		{"0.3.1", []int{0}, wire.DecisionUpgradeRequired, 0, true},
	}
	srv := New(cfg, nil, nil, fakeDB{})
	for _, c := range cases {
		cl := newClient(t, srv)
		cl.version = "0.0.1" // below the minimum: the handshake must still answer 200
		rec := cl.do("POST", "/agent/v1/handshake", wire.HandshakeRequest{AgentVersion: c.version, Protocols: c.protocols, OS: "linux", Arch: "amd64"},
			wire.HeaderAgentProtocol, "99")
		wantStatus(t, rec, 200, "")
		var h wire.HandshakeResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
			t.Fatal(err)
		}
		if h.Decision != c.decision || h.Protocol != c.protocol || (h.DownloadURL != "") != c.download {
			t.Errorf("%s %v: got %+v, want decision %s protocol %d", c.version, c.protocols, h, c.decision, c.protocol)
		}
		if h.MinAgentVersion != "0.2.0" || h.LatestAgentVersion != "0.3.1" || h.Limits.MaxChangesPerBatch != 1000 || h.Limits.MaxBatchBytes != 1<<20 {
			t.Errorf("versions/limits = %+v", h)
		}
		if h.Decision != wire.DecisionOK && h.Message == "" {
			t.Errorf("%s: no message for %s", c.version, h.Decision)
		}
	}
}

func TestHandshakeBadRequests(t *testing.T) {
	srv := New(testConfig(t, nil), nil, nil, fakeDB{})
	cl := newClient(t, srv)
	for _, body := range []any{
		wire.HandshakeRequest{AgentVersion: "v0.1", Protocols: []int{1}},
		wire.HandshakeRequest{AgentVersion: "0.1.0"},
		"{not json",
		`{"agent_version":"0.1.0","protocols":[1]} trailing`,
	} {
		wantStatus(t, cl.do("POST", "/agent/v1/handshake", body), 400, wire.CodeInvalidRequest)
	}
	wantStatus(t, cl.do("POST", "/agent/v1/handshake", strings.Repeat(" ", DefaultMaxBody+1)+"{}"), 413, wire.CodePayloadTooLarge)
}

func TestHandshakeReachableToAnyVersion(t *testing.T) {
	srv := New(testConfig(t, map[string]string{"AGENTSERVER_MIN_AGENT_VERSION": "0.2.0", "AGENTSERVER_LATEST_AGENT_VERSION": "0.2.0"}), nil, nil, fakeDB{})
	cl := newClient(t, srv)
	cl.version = "0.0.1"
	wantStatus(t, cl.do("POST", "/agent/v1/handshake", wire.HandshakeRequest{AgentVersion: "0.0.1", Protocols: []int{1}},
		wire.HeaderAgentProtocol, "9"), 200, "")
}
