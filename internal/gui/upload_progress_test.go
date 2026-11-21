package gui

import (
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCountingReader(t *testing.T) {
	var n atomic.Int64
	r := &countingReader{r: strings.NewReader(strings.Repeat("x", 10000)), n: &n}
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 10000 {
		t.Fatalf("counted %d, want 10000", n.Load())
	}
}
