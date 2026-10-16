package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestWithPlatform(t *testing.T) {
	props := func() gateway.IdentifyProperties {
		return gateway.IdentifyProperties{gateway.IdentifyBrowser: "Chrome", gateway.IdentifyOS: "Windows", "browser_user_agent": "Mozilla"}
	}
	identifyPlatform.Store("")
	if p := withPlatform(props()); p[gateway.IdentifyBrowser] != "Chrome" {
		t.Fatalf("web changed: %v", p)
	}
	identifyPlatform.Store("android")
	p := withPlatform(props())
	if p[gateway.IdentifyBrowser] != "Discord Android" || p[gateway.IdentifyOS] != "Android" || p["browser_user_agent"] != nil {
		t.Fatalf("android: %v", p)
	}
	identifyPlatform.Store("")
}
