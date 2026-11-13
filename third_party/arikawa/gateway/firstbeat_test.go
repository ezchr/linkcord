package gateway

import (
	"testing"
	"time"
)

func TestFirstBeatDelay(t *testing.T) {
	interval := 41250 * time.Millisecond
	for range 100 {
		if d := firstBeatDelay(interval); d < 100*time.Millisecond || d > interval {
			t.Fatalf("first beat after %v of %v", d, interval)
		}
	}
}
