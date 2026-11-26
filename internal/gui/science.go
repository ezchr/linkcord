package gui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	lchttp "github.com/ezchr/linkcord/internal/http"
)

// science sends the everyday analytics events Discord's web client sends to
// /science, shaped as it shapes them (docs 05 of the client study): each
// event carries client_track_timestamp, client_heartbeat_session_id,
// event_sequence_number and client_uuid, plus the common properties
// (rendered_locale, uptime_app, cpu_core_count, client_performance_memory),
// and batches carry client_send_timestamp. Events are throttled per key as
// the client throttles them. Device values are real ones read here; the
// launch signature and heartbeat session are the launch's own, shared with
// the super properties. The fingerprint, which the web client only holds
// logged out, is not sent with events, as there. Nothing is sent while the
// user turns analytics off.
type science struct {
	client *httputil.Client
	off    func() bool

	mu      sync.Mutex
	token   string
	userID  discord.UserID
	queue   []scienceEvent
	last    map[string]time.Time
	seq     int
	started time.Time
	// sessionID is client_heartbeat_session_id, a UUID per session.
	sessionID string
	// initialSession is the session when the queue was made, to tell it from
	// one set by hand.
	initialSession string
	// tested is set once the first batch carried x-science-test.
	tested bool
	uuid   clientUUID
	// stats are what the hourly client_telemetry report counts.
	stats struct {
		ok, failed, batches, minBatch, maxBatch, total int
		firstSeq, lastSeq                              int
		periodStart                                    time.Time
	}

	// stopped is set once stop ran; events after it, as a call's
	// network_action_* answered while closing, are sent at once.
	stopped  bool
	stopOnce sync.Once

	wake chan struct{}
	done chan struct{}
}

// scienceSends counts the /science requests in flight, so that quitting
// can wait for the last ones, as the web client sends its queue on page
// hide.
var scienceSends sync.WaitGroup

// waitScienceSends waits for the /science requests in flight, at most d.
func waitScienceSends(d time.Duration) {
	ch := make(chan struct{})
	go func() {
		scienceSends.Wait()
		close(ch)
	}()
	select {
	case <-ch:
	case <-time.After(d):
	}
}

type scienceEvent struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
}

type scienceBatch struct {
	Token  string         `json:"token"`
	Events []scienceEvent `json:"events"`
}

// clientUUID makes client_uuid as the web client's generator does: 24 bytes
// of the user id, a random prefix per load, the generator's creation time
// and a sequence number, each as two little-endian int32 halves, in base64.
type clientUUID struct {
	prefix  uint32
	created uint64
	n       uint32
}

func (g *clientUUID) generate(user discord.UserID) string {
	var b [24]byte
	binary.LittleEndian.PutUint64(b[0:], uint64(user))
	binary.LittleEndian.PutUint32(b[8:], g.prefix)
	binary.LittleEndian.PutUint64(b[12:], g.created)
	binary.LittleEndian.PutUint32(b[20:], g.n)
	g.n++
	return base64.StdEncoding.EncodeToString(b[:])
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func newScience(client *httputil.Client, off func() bool) *science {
	s := &science{
		client:         client,
		off:            off,
		last:           make(map[string]time.Time),
		started:        time.Now(),
		sessionID:      lchttp.HeartbeatSessionID(),
		initialSession: lchttp.HeartbeatSessionID(),
		uuid:           clientUUID{prefix: mrand.Uint32(), created: uint64(time.Now().UnixMilli())},
		wake:           make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	s.stats.periodStart = time.Now()
	bindScienceHTTP(client, s)
	go s.run()
	go s.beaker()
	return s
}

// setSession keeps the analytics token from READY and whose events they are;
// events wait until it is known, as the web client holds them for an
// identity.
func (s *science) setSession(token string, user discord.UserID) {
	s.mu.Lock()
	s.token, s.userID = token, user
	s.mu.Unlock()
	s.poke()
}

// stop ends the session's analytics and starts sending what is queued, with
// a short timeout. It does not wait; quitting waits with waitScienceSends.
func (s *science) stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()
		close(s.done)
		s.flushWithin(2 * time.Second)
	})
}

func (s *science) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// track queues an event with the common properties, unless analytics are
// off or the same key was sent within every. An empty key is not throttled.
func (s *science) track(typ string, props map[string]any, key string, every time.Duration) {
	if s.off != nil && s.off() {
		return
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	features, cpu, cpuOK := currentDeviceProps()
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != "" {
		if t, ok := s.last[key]; ok && time.Since(t) < every {
			return
		}
		s.last[key] = time.Now()
	}
	p := map[string]any{
		"client_track_timestamp":      time.Now().UnixMilli(),
		"client_heartbeat_session_id": s.heartbeatSession(),
		"rendered_locale":             lchttp.Locale(),
		"uptime_app":                  int(time.Since(s.started).Seconds()),
		"cpu_core_count":              runtime.NumCPU(),
		"client_performance_memory":   mem.Sys / 1024,
		"launch_signature":            lchttp.LaunchSignature(),
		"accessibility_features":      features,
	}
	if cpuOK {
		p["client_performance_cpu"] = cpu
	}
	for k, v := range props {
		p[k] = v
	}
	s.seq++
	p["event_sequence_number"] = s.seq
	if s.userID.IsValid() {
		p["client_uuid"] = s.uuid.generate(s.userID)
	}
	s.queue = append(s.queue, scienceEvent{Type: typ, Properties: p})
	// The web client keeps at most 10,000 waiting; far fewer happen here.
	if len(s.queue) > 1000 {
		s.queue = s.queue[len(s.queue)-1000:]
	}
	if s.stopped {
		go s.flushWithin(2 * time.Second)
		return
	}
	go s.poke()
}

// run sends the queue shortly after events arrive, as the web client sends
// on an idle callback with a 1.5 s timeout.
func (s *science) run() {
	for {
		select {
		case <-s.done:
			// stop sent the queue, as the web client flushes when the
			// connection closes.
			return
		case <-s.wake:
		}
		select {
		case <-s.done:
			return
		case <-time.After(1500 * time.Millisecond):
		}
		s.flush()
	}
}

func (s *science) flush() { s.flushWithin(20 * time.Second) }

// flushWithin sends the queue, giving the request at most timeout.
func (s *science) flushWithin(timeout time.Duration) {
	s.mu.Lock()
	if s.token == "" || len(s.queue) == 0 {
		s.mu.Unlock()
		return
	}
	now := time.Now().UnixMilli()
	for _, e := range s.queue {
		e.Properties["client_send_timestamp"] = now
	}
	batch := scienceBatch{Token: s.token, Events: s.queue}
	s.queue = nil
	opts := []httputil.RequestOption{httputil.WithJSONBody(batch)}
	if !s.tested {
		// The first batch of a load carries this test header.
		s.tested = true
		h := http.Header{}
		h.Set("x-science-test", newUUID())
		opts = append(opts, httputil.WithHeaders(h))
	}
	s.mu.Unlock()

	scienceSends.Add(1)
	go func() {
		defer scienceSends.Done()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := s.client.WithContext(ctx).FastRequest("POST", api.Endpoint+"science", opts...)
		s.mu.Lock()
		defer s.mu.Unlock()
		st := &s.stats
		if err != nil {
			st.failed++
			slog.Debug("could not send analytics", "err", err)
			return
		}
		st.ok++
		n := len(batch.Events)
		st.batches++
		st.total += n
		if st.minBatch == 0 || n < st.minBatch {
			st.minBatch = n
		}
		st.maxBatch = max(st.maxBatch, n)
		first := batch.Events[0].Properties["event_sequence_number"].(int)
		lastN := batch.Events[n-1].Properties["event_sequence_number"].(int)
		if st.firstSeq == 0 {
			st.firstSeq = first
		}
		st.lastSeq = lastN
	}()
}

// beaker sends the client_telemetry report to /beaker, as the web client
// does: first 1 to 60 minutes after connecting, then about every hour.
func (s *science) beaker() {
	wait := time.Duration(mrand.Int64N(int64(59*time.Minute))) + time.Minute
	for {
		select {
		case <-s.done:
			return
		case <-time.After(wait):
		}
		wait = time.Hour + time.Duration(mrand.Int64N(int64(12*time.Minute))) - 6*time.Minute
		if s.off != nil && s.off() {
			continue
		}
		s.mu.Lock()
		st := s.stats
		token := s.token
		s.stats = struct {
			ok, failed, batches, minBatch, maxBatch, total int
			firstSeq, lastSeq                              int
			periodStart                                    time.Time
		}{periodStart: time.Now()}
		s.mu.Unlock()
		if token == "" {
			continue
		}
		avg := 0
		if st.batches > 0 {
			avg = st.total / st.batches
		}
		ev := scienceEvent{Type: "client_telemetry", Properties: map[string]any{
			"rpc_success_count":                st.ok,
			"rpc_failure_count":                st.failed,
			"first_seen_event_sequence_number": st.firstSeq,
			"last_seen_event_sequence_number":  st.lastSeq,
			"telemetry_period_start_timestamp": st.periodStart.UnixMilli(),
			"telemetry_period_end_timestamp":   time.Now().UnixMilli(),
			"event_queue_rejection_count":      0,
			"event_queue_batch_count":          st.batches,
			"event_queue_batch_min_size":       st.minBatch,
			"event_queue_batch_max_size":       st.maxBatch,
			"event_queue_batch_avg_size":       avg,
			"launch_signature":                 lchttp.LaunchSignature(),
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := s.client.WithContext(ctx).FastRequest("POST", api.Endpoint+"beaker",
			httputil.WithJSONBody(scienceBatch{Token: token, Events: []scienceEvent{ev}}))
		cancel()
		if err != nil {
			slog.Debug("could not send client telemetry", "err", err)
		}
	}
}

// crashed sends app_crashed for a panic Linkcord caught, as the web
// client's crash screen does, and starts sending it at once. The report the
// web client also sends to Sentry is not sent.
func (s *science) crashed(path, msg, stack string) {
	s.track("app_crashed", map[string]any{
		"path":             path,
		"error_message":    msg,
		"error_stack":      stack,
		"uses_client_mods": false,
		"error_level":      "fatal",
	}, "", 0)
	s.flushWithin(2 * time.Second)
}

// heartbeatSession is the current client_heartbeat_session_id, read for
// each event as the web client reads it, so one that expired or was reset
// is not kept; a session set by hand (tests) wins.
func (s *science) heartbeatSession() string {
	if s.sessionID != "" && s.sessionID != s.initialSession {
		return s.sessionID
	}
	return lchttp.HeartbeatSessionID()
}
