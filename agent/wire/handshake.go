package wire

// Handshake decisions.
const (
	DecisionOK                  = "ok"
	DecisionUpgradeRecommended  = "upgrade_recommended"
	DecisionUpgradeRequired     = "upgrade_required"
	DecisionUnsupportedProtocol = "unsupported_protocol"
)

// HandshakeRequest is the body of POST /agent/v1/handshake.
type HandshakeRequest struct {
	AgentVersion string `json:"agent_version"`
	Protocols    []int  `json:"protocols"`
	OS           string `json:"os,omitempty"`
	Arch         string `json:"arch,omitempty"`
}

// HandshakeResponse tells the agent whether it may proceed, and with which
// protocol and limits.
type HandshakeResponse struct {
	Decision           string `json:"decision"`
	Protocol           int    `json:"protocol"`
	MinAgentVersion    string `json:"min_agent_version"`
	LatestAgentVersion string `json:"latest_agent_version"`
	DownloadURL        string `json:"download_url"`
	Message            string `json:"message"`
	Limits             Limits `json:"limits"`
}

// Limits lets the server tighten batch sizes without an agent release. The
// agent uses the smaller of its own defaults and these.
type Limits struct {
	MaxChangesPerBatch int `json:"max_changes_per_batch"`
	MaxBatchBytes      int `json:"max_batch_bytes"`
}
