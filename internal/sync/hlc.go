// Package sync implements the Hybrid Logical Clock (§2.3) that Argos uses
// for sync conflict resolution instead of raw wall-clock timestamps.
package sync

import "sync"

// HLC is a Hybrid Logical Clock value (§2.3, §5.1): a physical time
// component (unix ms), a logical counter, and the id of the node that
// produced it.
type HLC struct {
	Physical int64
	Counter  int64
	NodeID   string
}

var (
	lastMu     sync.Mutex
	lastByNode = map[string]HLC{}
)

// Now produces nodeID's next HLC for the given wall-clock reading. Per
// §2.3, the physical component only ever moves forward: if
// currentPhysicalTimeMs hasn't advanced past this node's last-produced
// value (including when the system clock is frozen or has gone backwards),
// the counter is incremented instead, so consecutive calls from the same
// node still produce strictly increasing values.
func Now(nodeID string, currentPhysicalTimeMs int64) HLC {
	lastMu.Lock()
	defer lastMu.Unlock()

	prev, ok := lastByNode[nodeID]
	next := HLC{NodeID: nodeID}
	if !ok || currentPhysicalTimeMs > prev.Physical {
		next.Physical = currentPhysicalTimeMs
		next.Counter = 0
	} else {
		next.Physical = prev.Physical
		next.Counter = prev.Counter + 1
	}
	lastByNode[nodeID] = next
	return next
}

// Observe merges a received HLC (from another node) into the local one,
// advancing local past both per §2.3: the new physical component is
// whichever of the two is greater; when they're equal, the new counter
// continues from whichever side's counter is greater, incremented by one,
// so the result is always strictly greater than both inputs. The result
// keeps local's own NodeID, since it represents this node's clock after
// observing the message, not the sender's.
func Observe(local, received HLC) HLC {
	next := HLC{NodeID: local.NodeID}
	switch {
	case local.Physical > received.Physical:
		next.Physical = local.Physical
		next.Counter = local.Counter + 1
	case received.Physical > local.Physical:
		next.Physical = received.Physical
		next.Counter = received.Counter + 1
	default:
		next.Physical = local.Physical
		if local.Counter > received.Counter {
			next.Counter = local.Counter + 1
		} else {
			next.Counter = received.Counter + 1
		}
	}
	return next
}

// Compare returns -1 if a orders before b, 1 if a orders after b, or 0 if
// they are identical, comparing physical, then counter, then nodeID as the
// final deterministic tiebreak (§2.3).
func Compare(a, b HLC) int {
	if a.Physical != b.Physical {
		if a.Physical < b.Physical {
			return -1
		}
		return 1
	}
	if a.Counter != b.Counter {
		if a.Counter < b.Counter {
			return -1
		}
		return 1
	}
	if a.NodeID != b.NodeID {
		if a.NodeID < b.NodeID {
			return -1
		}
		return 1
	}
	return 0
}
