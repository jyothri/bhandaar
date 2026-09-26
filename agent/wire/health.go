package wire

import "time"

// Health statuses.
const (
	HealthOK          = "ok"
	HealthUnavailable = "unavailable"
)

// HealthResponse is the body of GET /agent/health, for both 200 and 503.
type HealthResponse struct {
	Status        string    `json:"status"`
	Service       string    `json:"service"`
	ServerVersion string    `json:"server_version,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	APIVersions   []string  `json:"api_versions"`
	Time          time.Time `json:"time"`
}
