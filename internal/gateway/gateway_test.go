package gateway

import (
	"testing"

	discordgateway "github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

func TestNew(t *testing.T) {
	t.Run("authentication failed", func(t *testing.T) {
		opts := New(discordgateway.DefaultIdentifier("token")).Opts()
		if !opts.ErrorIsFatalClose(&ws.CloseEvent{Code: 4004}) {
			t.Error("gateway reconnects after the token is rejected")
		}
	})
}

func TestGatewayAddressesAsWeb(t *testing.T) {
	base := discordgateway.AddGatewayParams(gatewayURL)
	if base != "wss://gateway.discord.gg/?encoding=json&v=9" {
		t.Fatalf("gateway address %q", base)
	}
	if got := discordgateway.ResumeAddr(base, "wss://gateway-us-east1-b.discord.gg"); got != "wss://gateway-us-east1-b.discord.gg/?encoding=json&v=9" {
		t.Fatalf("resume address %q", got)
	}
	if got := discordgateway.ResumeAddr(base, ""); got != base {
		t.Fatalf("no resume address gave %q", got)
	}
}
