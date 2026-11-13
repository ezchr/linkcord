package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestWithPlatformIsWeb(t *testing.T) {
	props := gateway.IdentifyProperties{gateway.IdentifyBrowser: "Chrome", gateway.IdentifyOS: "Windows", "browser_user_agent": "Mozilla"}
	p := withPlatform(props)
	if p[gateway.IdentifyBrowser] != "Chrome" || p[gateway.IdentifyOS] != "Windows" || p["browser_user_agent"] != "Mozilla" {
		t.Fatalf("identify changed from the web's: %v", p)
	}
}
