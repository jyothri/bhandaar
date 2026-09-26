// Package wire holds the request and response types shared by agentserver and
// driveagent. It uses the standard library only (see wire_test.go), so
// importing it adds nothing to the agent's module graph.
package wire

// Headers every agent request carries.
const (
	HeaderAgentVersion   = "X-Agent-Version"
	HeaderAgentID        = "X-Agent-Id"
	HeaderAgentProtocol  = "X-Agent-Protocol"
	HeaderIdempotencyKey = "Idempotency-Key"
)

// MaxIdempotencyKeyLen is the longest Idempotency-Key the server accepts.
const MaxIdempotencyKeyLen = 128
