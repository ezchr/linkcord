// Package handler handles incoming Gateway events. Handlers are registered with generic methods, so the event type is known at compile time and dispatching an event does not go through reflection.
//
// # Usage
//
// Handler's usage is mostly similar to Discordgo, in that AddHandler expects a function with only one argument. The event type is inferred from the function's argument:
//
//	h.AddHandler(func(ev *gateway.MessageCreateEvent) {})
//
// The argument can also be an interface, in which case the handler is called for every event that implements it:
//
//	h.AddHandler(func(ev gateway.Event) {})
//
// For more information, refer to AddHandler.
package handler

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
)

// Handler is a container for event handlers. A zero-value instance is a valid instance.
//
// Handlers may be added and removed at any time, including from within a handler that is currently being called.
type Handler struct {
	// mutex serializes writers. Readers never lock; they load snapshot.
	mutex    sync.Mutex
	snapshot atomic.Pointer[snapshot]
}

// snapshot is an immutable view of all handlers. Writers replace it wholesale.
type snapshot struct {
	typed  map[reflect.Type][]*entry // keyed by the concrete event type
	ifaces []*entry                  // handlers taking an interface type
}

type entry struct {
	call    func(ev any)
	removed atomic.Bool
}

func New() *Handler {
	return &Handler{}
}

// Call calls all handlers with the given event. Handlers for the event's concrete type are called first, followed by handlers taking an interface that the event implements.
func (h *Handler) Call(ev any) {
	s := h.snapshot.Load()
	if s == nil {
		return
	}

	for _, entry := range s.typed[reflect.TypeOf(ev)] {
		if !entry.removed.Load() {
			entry.call(ev)
		}
	}

	for _, entry := range s.ifaces {
		if !entry.removed.Load() {
			entry.call(ev)
		}
	}
}

// AddHandler adds the handler, returning a function that would remove this handler when called. The handler is called in its own goroutine for each event.
//
// The event type E must either be a pointer, which matches only that exact event type, or an interface, which matches every event implementing it.
//
//	// An example of a valid function handler.
//	h.AddHandler(func(*gateway.MessageCreateEvent) {})
func (h *Handler) AddHandler[E any](fn func(E)) (rm func()) {
	return h.add(eventType[E](), matcher[E](func(ev E) { go fn(ev) }))
}

// AddSyncHandler is a synchronous variant of AddHandler. Handlers added using this method will block the Call method, which is helpful if the user needs to rely on the order of events arriving. Handlers added using this method should not block for very long, as it may clog up other handlers.
func (h *Handler) AddSyncHandler[E any](fn func(E)) (rm func()) {
	return h.add(eventType[E](), matcher(fn))
}

// AddChanHandler adds a channel that receives every event of type E. The type rules are the same as AddHandler.
//
// Keep in mind that the user must NOT close the channel. In fact, the channel should not be closed at all. The caller function WILL PANIC if the channel is closed!
//
// When the rm callback that is returned is called, it will also guarantee that all blocking sends will be cancelled. This helps prevent dangling goroutines.
//
//	ch := make(chan *gateway.MessageCreateEvent)
//	h.AddChanHandler(ch)
func (h *Handler) AddChanHandler[E any](ch chan<- E) (rm func()) {
	closer := make(chan struct{})

	rmHandler := h.add(eventType[E](), matcher[E](func(ev E) {
		go func() {
			select {
			case ch <- ev:
			case <-closer:
			}
		}()
	}))

	return sync.OnceFunc(func() {
		rmHandler()
		close(closer)
	})
}

// WaitFor blocks until an event of type E for which fn returns true arrives, or until ctx is done, in which case ctx.Err() is returned. fn is called synchronously, so no events of type E are skipped.
func (h *Handler) WaitFor[E any](ctx context.Context, fn func(E) bool) (E, error) {
	result := make(chan E, 1)

	rm := h.AddSyncHandler(func(ev E) {
		if fn(ev) {
			select {
			case result <- ev:
			default:
			}
		}
	})
	defer rm()

	select {
	case ev := <-result:
		return ev, nil
	case <-ctx.Done():
		var zero E
		return zero, ctx.Err()
	}
}

// ChanFor returns a channel that would receive all incoming events of type E that match the callback given. The cancel() function removes the handler and drops all hanging goroutines.
//
// This method is more intended to be used as a filter. For a persistent event channel, consider using AddChanHandler.
func (h *Handler) ChanFor[E any](fn func(E) bool) (out <-chan E, cancel func()) {
	result := make(chan E)
	closer := make(chan struct{})

	removeHandler := h.AddHandler(func(ev E) {
		if fn(ev) {
			select {
			case result <- ev:
			case <-closer:
			}
		}
	})

	cancel = sync.OnceFunc(func() {
		removeHandler()
		close(closer)
	})

	return result, cancel
}

// eventType returns the reflect.Type of E, panicking if E is neither a pointer nor an interface. Interfaces return nil.
func eventType[E any]() reflect.Type {
	t := reflect.TypeFor[E]()

	switch t.Kind() {
	case reflect.Interface:
		return nil
	case reflect.Pointer:
		return t
	default:
		panic(fmt.Sprintf("handler: event type %s is not a pointer or interface", t))
	}
}

// matcher wraps fn into a function that calls it only if ev is an E. For handlers keyed by their concrete type, the check always succeeds.
func matcher[E any](fn func(E)) func(any) {
	return func(v any) {
		if ev, ok := v.(E); ok {
			fn(ev)
		}
	}
}

// add adds the given entry for t, or as an interface handler if t is nil.
func (h *Handler) add(t reflect.Type, call func(any)) (rm func()) {
	e := &entry{call: call}

	h.update(func(s *snapshot) {
		if t == nil {
			s.ifaces = append(slices.Clip(s.ifaces), e)
		} else {
			s.typed[t] = append(slices.Clip(s.typed[t]), e)
		}
	})

	return sync.OnceFunc(func() {
		e.removed.Store(true)

		h.update(func(s *snapshot) {
			if t == nil {
				s.ifaces = deleteEntry(s.ifaces, e)
			} else if entries := deleteEntry(s.typed[t], e); len(entries) > 0 {
				s.typed[t] = entries
			} else {
				delete(s.typed, t)
			}
		})
	})
}

// update replaces the snapshot with a shallow copy modified by fn. fn must not mutate the slices in the copy in place.
func (h *Handler) update(fn func(*snapshot)) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	var next snapshot
	if old := h.snapshot.Load(); old != nil {
		next.typed = make(map[reflect.Type][]*entry, len(old.typed)+1)
		for t, entries := range old.typed {
			next.typed[t] = entries
		}
		next.ifaces = old.ifaces
	} else {
		next.typed = make(map[reflect.Type][]*entry)
	}

	fn(&next)
	h.snapshot.Store(&next)
}

func deleteEntry(entries []*entry, e *entry) []*entry {
	return slices.DeleteFunc(slices.Clone(entries), func(v *entry) bool { return v == e })
}
