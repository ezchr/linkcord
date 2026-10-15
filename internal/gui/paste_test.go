package gui

import (
	"io"
	"os"
	"strings"
	"testing"

	"gioui.org/app"
	"gioui.org/io/transfer"
)

func TestPasteLongTextAttaches(t *testing.T) {
	c := &chatView{u: &ui{win: new(app.Window)}}
	paste := func(s string) {
		c.pasteText(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }})
	}
	paste("hello")
	if got := c.composer.Text(); got != "hello" || len(c.comp.files) != 0 {
		t.Fatalf("short text: box %q, %d files", got, len(c.comp.files))
	}
	long := strings.Repeat("a", pasteAsFile+1)
	paste(long)
	if got := c.composer.Text(); got != "hello" {
		t.Fatalf("long text went into the box: %d characters", len(got))
	}
	if len(c.comp.files) != 1 || c.comp.files[0].name != "message.txt" {
		t.Fatalf("long text was not attached: %d files", len(c.comp.files))
	}
	data, err := os.ReadFile(c.comp.files[0].path)
	if err != nil || string(data) != long {
		t.Fatalf("message.txt holds %d bytes, err %v", len(data), err)
	}
}
