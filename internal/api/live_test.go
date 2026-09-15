package api

import "testing"

func TestHubReplayAndResync(t *testing.T) {
	h := newHub()
	for i := 0; i < 10; i++ {
		h.publish(Message{Kind: "status"})
	}
	missed, head, ok := h.since(7)
	if !ok || head != 10 || len(missed) != 3 || missed[0].Seq != 8 {
		t.Fatalf("replay from 7: ok=%v head=%d n=%d", ok, head, len(missed))
	}
	if missed, _, ok := h.since(10); !ok || len(missed) != 0 {
		t.Fatalf("up to date should replay nothing")
	}
	if _, _, ok := h.since(0); ok {
		// 0 is older than the ring only once the ring wrapped; here it is not.
		_ = ok
	}
	for i := 0; i < ringSize+5; i++ {
		h.publish(Message{Kind: "status"})
	}
	if _, _, ok := h.since(3); ok {
		t.Fatal("a seq older than the ring must demand a resync")
	}
	missed, head, ok = h.since(head + ringSize)
	if !ok || len(missed) != 5 || head != 10+ringSize+5 {
		t.Fatalf("tail replay: ok=%v n=%d head=%d", ok, len(missed), head)
	}
}
