package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/json"
)

func TestAuthTokenRefreshDecodes(t *testing.T) {
	// The gateway decodes with the registry built here, so the event must be
	// in it.
	fn := gateway.NewOpUnmarshalers(gateway.AuthTokenRefresh).Lookup(0, "AUTH_TOKEN_REFRESH")
	if fn == nil {
		t.Fatal("AUTH_TOKEN_REFRESH is not registered; refreshed tokens would be lost")
	}
	ev := fn()
	if err := json.Raw(`{"token":"new.token.value"}`).UnmarshalTo(ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := ev.(*authTokenRefreshEvent)
	if !ok {
		t.Fatalf("decoded %T; want *authTokenRefreshEvent", ev)
	}
	if got.Token != "new.token.value" {
		t.Fatalf("Token = %q; want %q", got.Token, "new.token.value")
	}
}
