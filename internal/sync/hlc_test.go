package sync

import "testing"

func TestNow_FrozenClockStrictlyIncreases(t *testing.T) {
	const nodeID = "node-frozen-clock"
	const frozenMs = int64(1_700_000_000_000)

	first := Now(nodeID, frozenMs)
	second := Now(nodeID, frozenMs)

	if Compare(second, first) <= 0 {
		t.Fatalf("expected second call to be strictly greater than first, got first=%+v second=%+v", first, second)
	}
	if second.Physical != frozenMs || second.Counter != first.Counter+1 {
		t.Fatalf("expected physical to stay at %d and counter to increment, got %+v", frozenMs, second)
	}
}

func TestObserve_ReceivedAheadOfLocal(t *testing.T) {
	local := HLC{Physical: 1000, Counter: 5, NodeID: "local-node"}
	received := HLC{Physical: 2000, Counter: 1, NodeID: "remote-node"}

	got := Observe(local, received)

	want := HLC{Physical: 2000, Counter: 2, NodeID: "local-node"}
	if got != want {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestCompare_TiebreaksOnNodeID(t *testing.T) {
	a := HLC{Physical: 1000, Counter: 5, NodeID: "aaa"}
	b := HLC{Physical: 1000, Counter: 5, NodeID: "bbb"}

	if Compare(a, b) != -1 {
		t.Fatalf("expected a < b when only nodeID differs, got %d", Compare(a, b))
	}
	if Compare(b, a) != 1 {
		t.Fatalf("expected b > a when only nodeID differs, got %d", Compare(b, a))
	}
	if Compare(a, a) != 0 {
		t.Fatalf("expected equal HLCs to compare as 0, got %d", Compare(a, a))
	}
}
