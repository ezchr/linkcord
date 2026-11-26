package gui

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// science sends a small, plausible set of the analytics events Discord's own
// client sends to /science, so that an account does not look automated for
// sending none. It sends only common "alive" events (the app opening, a
// channel or server being looked at), each throttled as Discord throttles
// it, never device or behaviour detail beyond the ids involved, and nothing
// at all while the user turns it off. It reuses the session's HTTP layer, so
// it carries no identity values the client does not already send.
type science struct {
	client *httputil.Client
	// off reports whether the user has turned telemetry off.
	off func() bool

	mu    sync.Mutex
	token string
	queue []scienceEvent
	last  map[string]time.Time

	wake chan struct{}
	done chan struct{}
}

type scienceEvent struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// scienceBatch is the body of a /science request.
type scienceBatch struct {
	Token  string         `json:"token"`
	Events []scienceEvent `json:"events"`
}

func newScience(client *httputil.Client, off func() bool) *science {
	s := &science{
		client: client,
		off:    off,
		last:   make(map[string]time.Time),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go s.run()
	return s
}

// setToken keeps the analytics token from READY; events wait in the queue
// until it is known, as Discord's client holds them until it has an identity.
func (s *science) setToken(token string) {
	s.mu.Lock()
	s.token = token
	s.mu.Unlock()
	s.poke()
}

func (s *science) stop() { close(s.done) }

func (s *science) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// track queues an event, unless telemetry is off or the same key was sent
// within every. An empty key is never throttled.
func (s *science) track(typ string, props map[string]any, key string, every time.Duration) {
	if s.off != nil && s.off() {
		return
	}
	s.mu.Lock()
	if key != "" {
		if t, ok := s.last[key]; ok && time.Since(t) < every {
			s.mu.Unlock()
			return
		}
		s.last[key] = time.Now()
	}
	s.queue = append(s.queue, scienceEvent{Type: typ, Properties: props})
	// The same cap Discord's client keeps; the oldest go first.
	if len(s.queue) > 100 {
		s.queue = s.queue[len(s.queue)-100:]
	}
	s.mu.Unlock()
	s.poke()
}

// run flushes the queue a little after events arrive, as Discord's client
// sends on an idle callback rather than at once.
func (s *science) run() {
	for {
		select {
		case <-s.done:
			return
		case <-s.wake:
		}
		select {
		case <-s.done:
			return
		case <-time.After(2 * time.Second):
		}
		s.flush()
	}
}

func (s *science) flush() {
	s.mu.Lock()
	if s.token == "" || len(s.queue) == 0 {
		s.mu.Unlock()
		return
	}
	batch := scienceBatch{Token: s.token, Events: s.queue}
	s.queue = nil
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := s.client.WithContext(ctx).FastRequest("POST", api.Endpoint+"science", httputil.WithJSONBody(batch))
		if err != nil {
			slog.Debug("could not send analytics", "err", err)
			// The events are let go: account health does not need every
			// one, and a failed send must not pile up.
		}
	}()
}
