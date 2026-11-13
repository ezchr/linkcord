package api

import (
	"testing"
	"time"
)

func TestAckDay(t *testing.T) {
	// Discord's epoch is 2015-01-01; a moment into its first day is day 1.
	if d := ackDay(time.Date(2015, 1, 1, 12, 0, 0, 0, time.UTC)); d != 1 {
		t.Fatalf("day %d", d)
	}
	if d := ackDay(time.Date(2015, 1, 3, 0, 0, 1, 0, time.UTC)); d != 3 {
		t.Fatalf("day %d", d)
	}
}
