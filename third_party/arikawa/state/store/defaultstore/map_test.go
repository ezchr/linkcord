package defaultstore

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestAtomicMap(t *testing.T) {
	var calls atomic.Int64
	m := newAtomicMap[int](func() *int {
		calls.Add(1)
		return new(int)
	})

	values := make(chan *int, 32)
	var wg sync.WaitGroup
	for range cap(values) {
		wg.Go(func() {
			value, _ := m.LoadOrStore(1)
			values <- value
		})
	}
	wg.Wait()
	close(values)

	first := <-values
	for value := range values {
		if value != first {
			t.Fatal("LoadOrStore returned different values")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("constructor called %d times", calls.Load())
	}

	m.Reset()
	if _, ok := m.Load(1); ok {
		t.Fatal("Reset retained a value")
	}
}
