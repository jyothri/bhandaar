package syncer

import (
	"fmt"

	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

// The synced marker's rules (docs/specs/remote-sync-agent.md, "Synced
// marker"). The marker is a copy of the server's acked ranges, never merged
// locally; comparing it with the server's next answer is how either side
// going back in time is noticed. The store keeps it (store.Marker); these
// are the decisions.

// checkRanges checks that ranges from the server are what the protocol
// promises: each (from, to] with 0 <= from < to, sorted, and apart.
func checkRanges(rs []wire.Range) error {
	for i, r := range rs {
		if r[0] < 0 || r[0] >= r[1] || (i > 0 && r[0] <= rs[i-1][1]) {
			return fmt.Errorf("the server sent malformed acked ranges %v", rs)
		}
	}
	return nil
}

// covers reports whether the union of have (sorted, apart) includes every
// interval in need.
func covers(have []wire.Range, need ...wire.Range) bool {
	for _, n := range need {
		if n[0] >= n[1] {
			continue
		}
		ok := false
		for _, h := range have {
			if h[0] <= n[0] && n[1] <= h[1] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func sameRanges(a, b []wire.Range) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func highest(rs []wire.Range) int64 {
	if len(rs) == 0 {
		return 0
	}
	return rs[len(rs)-1][1]
}

// action is what reconciling decides.
type action int

const (
	keepMarker  action = iota // same ranges: nothing to do
	adoptServer               // the server has more: copy its ranges
	clearMarker               // the server started the stream over
	mintStream                // one side went back in time: start over
)

// reconcile compares the server's answer to PUT /drives with the local
// marker and the local clock (the reconcile table in the agent spec). why
// explains a mintStream.
func reconcile(local store.Marker, clock int64, resp wire.DriveOpenResponse) (a action, why string) {
	server := resp.AckedRanges
	switch {
	case resp.Reset:
		return clearMarker, ""
	case highest(server) > clock:
		return mintStream, fmt.Sprintf("the server has versions up to %d but state.db's clock is at %d: state.db was restored from an older copy", highest(server), clock)
	case !covers(server, local.AckedRanges()...):
		return mintStream, fmt.Sprintf("the server's acked ranges %v no longer cover what it acknowledged before, %v: the server was restored from an older copy", server, local.AckedRanges())
	case !sameRanges(server, local.AckedRanges()):
		return adoptServer, ""
	}
	return keepMarker, ""
}

// firstGap returns the oldest interval, up to head, that the marker (in
// the server's form) doesn't cover.
func firstGap(acked []wire.Range, head int64) (wire.Range, bool) {
	lo := int64(0)
	for _, r := range acked {
		if r[0] > lo {
			return wire.Range{lo, min(r[0], head)}, lo < head
		}
		lo = max(lo, r[1])
	}
	return wire.Range{lo, head}, lo < head
}

// gaps returns every interval up to head the marker doesn't cover.
func gaps(acked []wire.Range, head int64) []wire.Range {
	var out []wire.Range
	lo := int64(0)
	for _, r := range acked {
		if r[0] > lo && lo < head {
			out = append(out, wire.Range{lo, min(r[0], head)})
		}
		lo = max(lo, r[1])
	}
	if lo < head {
		out = append(out, wire.Range{lo, head})
	}
	return out
}
