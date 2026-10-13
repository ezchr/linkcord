package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func newMessage(content string) *gateway.MessageCreateEvent {
	return &gateway.MessageCreateEvent{
		Message: discord.Message{Content: content},
	}
}

func TestCall(t *testing.T) {
	var results = make(chan string)

	h := &Handler{}

	// Add handler test
	rm := h.AddHandler(func(m *gateway.MessageCreateEvent) {
		results <- m.Content
	})

	go h.Call(newMessage("hime arikawa"))

	if r := <-results; r != "hime arikawa" {
		t.Fatal("Returned results is wrong:", r)
	}

	// Delete handler test
	rm()

	go h.Call(newMessage("astolfo"))

	select {
	case <-results:
		t.Fatal("Unexpected results")
	case <-time.After(5 * time.Millisecond):
		break
	}
}

func TestInvalidEventType(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for non-pointer event type")
		}
	}()

	New().AddHandler(func(gateway.MessageCreateEvent) {})
}

func TestSyncOrder(t *testing.T) {
	h := New()

	var got []string
	h.AddSyncHandler(func(any) { got = append(got, "any") })
	h.AddSyncHandler(func(*gateway.MessageCreateEvent) { got = append(got, "typed") })
	h.AddSyncHandler(func(gateway.Event) { got = append(got, "event") })
	h.AddSyncHandler(func(*gateway.TypingStartEvent) { got = append(got, "typing") })

	h.Call(newMessage(""))

	want := []string{"typed", "any", "event"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestInterfaceMismatch(t *testing.T) {
	h := New()

	h.AddSyncHandler(func(error) { t.Fatal("called for event not implementing error") })
	h.Call(newMessage(""))
}

func TestReentrant(t *testing.T) {
	h := New()

	var calls int
	var rm func()
	rm = h.AddSyncHandler(func(*gateway.MessageCreateEvent) {
		calls++
		rm()
		// Adding and calling from within a handler must not deadlock either.
		h.AddSyncHandler(func(*gateway.TypingStartEvent) {})
		h.Call(&gateway.TypingStartEvent{})
	})

	done := make(chan struct{})
	go func() {
		h.Call(newMessage(""))
		h.Call(newMessage(""))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deadlock")
	}

	if calls != 1 {
		t.Fatal("handler called after removal:", calls)
	}
}

func TestChanHandler(t *testing.T) {
	h := New()

	ch := make(chan *gateway.MessageCreateEvent)
	rm := h.AddChanHandler(ch)

	h.Call(newMessage("hime arikawa"))

	if m := <-ch; m.Content != "hime arikawa" {
		t.Fatal("Returned results is wrong:", m.Content)
	}

	// Removing must unblock pending sends.
	h.Call(newMessage("astolfo"))
	rm()
	rm()
}

func TestHandlerWaitFor(t *testing.T) {
	inc := make(chan *gateway.TypingStartEvent, 1)

	h := New()

	wanted := &gateway.TypingStartEvent{
		ChannelID: 123456,
	}

	evs := []any{
		&gateway.TypingStartEvent{},
		&gateway.MessageCreateEvent{},
		&gateway.ChannelDeleteEvent{},
		wanted,
	}

	go func() {
		ev, err := h.WaitFor(context.Background(), func(tp *gateway.TypingStartEvent) bool {
			return tp.ChannelID == wanted.ChannelID
		})
		if err != nil {
			t.Error(err)
		}
		inc <- ev
	}()

	// Wait for WaitFor to add its handler:
	time.Sleep(time.Millisecond)

	for _, ev := range evs {
		h.Call(ev)
	}

	if recv := <-inc; recv != wanted {
		t.Fatal("Unexpected receive:", recv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	// Test timeout
	v, err := h.WaitFor(ctx, func(*gateway.TypingStartEvent) bool {
		return false
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Unexpected error:", err)
	}
	if v != nil {
		t.Fatal("Unexpected value:", v)
	}
}

func TestHandlerChanFor(t *testing.T) {
	h := New()

	wanted := &gateway.TypingStartEvent{
		ChannelID: 123456,
	}

	evs := []any{
		&gateway.TypingStartEvent{},
		&gateway.MessageCreateEvent{},
		&gateway.ChannelDeleteEvent{},
		wanted,
	}

	inc, cancel := h.ChanFor(func(tp *gateway.TypingStartEvent) bool {
		return tp.ChannelID == wanted.ChannelID
	})
	defer cancel()

	for _, ev := range evs {
		h.Call(ev)
	}

	if recv := <-inc; recv != wanted {
		t.Fatal("Unexpected receive:", recv)
	}
}

func benchCall(b *testing.B, register func(h *Handler)) {
	h := New()
	register(h)
	ev := newMessage("")
	b.ReportAllocs()
	for b.Loop() {
		h.Call(ev)
	}
}

func BenchmarkCallTyped(b *testing.B) {
	benchCall(b, func(h *Handler) { h.AddSyncHandler(func(*gateway.MessageCreateEvent) {}) })
}

func BenchmarkCallIface(b *testing.B) {
	benchCall(b, func(h *Handler) { h.AddSyncHandler(func(gateway.Event) {}) })
}

func BenchmarkCall20Mixed(b *testing.B) {
	benchCall(b, func(h *Handler) {
		for range 10 {
			h.AddSyncHandler(func(*gateway.MessageCreateEvent) {})
			h.AddSyncHandler(func(*gateway.TypingStartEvent) {})
		}
		h.AddSyncHandler(func(any) {})
		h.AddSyncHandler(func(gateway.Event) {})
	})
}
