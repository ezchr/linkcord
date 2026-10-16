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
	"github.com/ayn2op/ningen/v3"
	clientgateway "github.com/ezchr/linkcord/internal/gateway"
	"github.com/ezchr/linkcord/internal/http"
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

// interactionResult is what Discord says of an interaction a user sent:
// INTERACTION_SUCCESS once the bot answered, or INTERACTION_FAILURE.
type interactionResult struct {
	ID    string `json:"id"`
	Nonce string `json:"nonce"`
}

type (
	interactionSuccessEvent struct{ interactionResult }
	interactionFailureEvent struct{ interactionResult }
)

func (*interactionSuccessEvent) Op() ws.OpCode           { return 0 }
func (*interactionSuccessEvent) EventType() ws.EventType { return "INTERACTION_SUCCESS" }
func (*interactionFailureEvent) Op() ws.OpCode           { return 0 }
func (*interactionFailureEvent) EventType() ws.EventType { return "INTERACTION_FAILURE" }
func (*modalCreateEvent) Op() ws.OpCode                  { return 0 }
func (*modalCreateEvent) EventType() ws.EventType        { return "INTERACTION_MODAL_CREATE" }

func init() {
	// Arikawa does not know this event; teach its decoder before any gateway
	// is created, as the gateway copies the registry.
	gateway.OpUnmarshalers.Add(func() ws.Event { return new(authTokenRefreshEvent) })
	gateway.OpUnmarshalers.Add(
		func() ws.Event { return new(interactionSuccessEvent) },
		func() ws.Event { return new(interactionFailureEvent) },
		func() ws.Event { return new(modalCreateEvent) },
	)
}

// newState returns the Discord state of a user account, set up like the web
// client so that the gateway treats it as one.
func newState(token string) *ningen.State {
	id := gateway.NewIdentifier(gateway.IdentifyCommand{
		Token:      token,
		Properties: withPlatform(http.IdentifyProperties()),
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
