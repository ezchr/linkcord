//go:build windows && !no_voice

package golive

import (
	"testing"
	"time"
)

func TestAllowReconnect(t *testing.T) {
	now := time.Now()
	var past []time.Time
	var ok bool
	for i := range reconnectTries {
		if past, ok = allowReconnect(past, now.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("try %d refused", i+1)
		}
	}
	if _, ok = allowReconnect(past, now.Add(time.Minute)); ok {
		t.Fatal("a try past the limit was allowed")
	}
	// Once the first ones are old enough, one more fits.
	if past, ok = allowReconnect(past, now.Add(reconnectWithin+time.Second/2)); !ok {
		t.Fatal("a try after the window was refused")
	}
	if len(past) != reconnectTries {
		t.Fatalf("kept %d tries, want %d", len(past), reconnectTries)
	}
}

func TestDaveChannelOf(t *testing.T) {
	if got := daveChannelOf(&StreamCreateEvent{RTCServerID: "100", RTCChannelID: "42"}).String(); got != "42" {
		t.Fatalf("got %s", got)
	}
	if got := daveChannelOf(&StreamCreateEvent{RTCServerID: "100"}).String(); got != "99" {
		t.Fatalf("got %s, want 99", got)
	}
}
