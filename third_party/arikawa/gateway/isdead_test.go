package gateway

import (
	"testing"
	"time"
)

// A connection that said hello but never acknowledged a heartbeat (it died
// right after a reconnect) is dead once two beats are missed, like one that
// stopped acknowledging.
func TestIsDeadWithoutAck(t *testing.T) {
	now := time.Now()
	g := &gatewayImpl{Gateway: &Gateway{}, heartrate: 40 * time.Second}

	g.helloBeat = now.Add(-30 * time.Second)
	g.lastSentBeat = now
	if g.isDead() {
		t.Fatal("dead 30s after hello with a 40s heartbeat")
	}

	g.helloBeat = now.Add(-90 * time.Second)
	if !g.isDead() {
		t.Fatal("not dead 90s after hello without any acknowledgement")
	}

	// An acknowledgement since keeps it alive.
	g.echoBeat = now.Add(-10 * time.Second)
	if g.isDead() {
		t.Fatal("dead 10s after an acknowledgement")
	}
	g.echoBeat = now.Add(-100 * time.Second)
	if !g.isDead() {
		t.Fatal("not dead 100s after the last acknowledgement")
	}
}
