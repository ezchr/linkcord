package gui

import (
	"log/slog"

	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/session"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	clientgateway "github.com/ayn2op/discordo/internal/gateway"
	"github.com/ayn2op/discordo/internal/http"
	"github.com/ayn2op/ningen/v3"
)

// closeCodeAuthenticationFailed is the close code of the gateway for a token
// it rejects.
const closeCodeAuthenticationFailed = 4004

// authTokenRefreshEvent is sent by Discord when it replaces the token of the
// session, which it does to clients that identify with the AuthTokenRefresh
// capability. The new token must be kept, or the next start may use a token
// that no longer works.
type authTokenRefreshEvent struct {
	Token string `json:"token"`
}

func (*authTokenRefreshEvent) Op() ws.OpCode           { return 0 } // dispatch
func (*authTokenRefreshEvent) EventType() ws.EventType { return "AUTH_TOKEN_REFRESH" }

func init() {
	// Arikawa does not know this event; teach its decoder before any gateway
	// is created, as the gateway copies the registry.
	gateway.OpUnmarshalers.Add(func() ws.Event { return new(authTokenRefreshEvent) })
}

// newState returns the Discord state of a user account, set up like the web
// client so that the gateway treats it as one.
func newState(token string) *ningen.State {
	id := gateway.NewIdentifier(gateway.IdentifyCommand{
		Token:      token,
		Properties: http.IdentifyProperties(),
		Capabilities: gateway.LazyUserNotes |
			gateway.VersionedReadStates |
			gateway.VersionedUserGuildSetttings |
			gateway.DedupeUserObjects |
			gateway.PrioritizedReadyPayload |
			gateway.MultipleGuildExperimentPopulations |
			gateway.NonChannelReadStates |
			gateway.AuthTokenRefresh |
			gateway.DebounceMessageReactions,
	})

	sess := session.NewWithGateway(clientgateway.New(id), handler.New())
	sess.Client = http.NewClient(token)
	st := ningen.FromState(state.NewFromSession(sess, defaultstore.New()))
	st.StateLog = func(err error) {
		slog.Error("state log", "err", err)
	}
	st.OnRequest = append(st.OnRequest, httputil.WithHeaders(http.Headers()))
	return st
}
