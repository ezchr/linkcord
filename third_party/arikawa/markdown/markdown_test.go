package markdown

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
	"github.com/yuin/goldmark/ast"
)

var parseSink ast.Node

func strcmp(t *testing.T, name, got, expected string) {
	t.Helper()
	if got != expected {
		t.Errorf("Mismatch %s:\n"+
			"expected %q\n"+
			"got      %q", name, expected, got)
	}
}

func TestParses(t *testing.T) {
	var tests = []string{
		">\n```\r",
		"```\n\n",
		"+ a\n+ b",
	}

	for _, test := range tests {
		Parse([]byte(test))
	}
}

func TestParseLink(t *testing.T) {
	content := []byte("[example](https://example.com)")
	message := &discord.Message{}
	cabinet := store.Cabinet{}
	for name, root := range map[string]ast.Node{
		"plain":   Parse(content),
		"message": ParseWithMessage(content, cabinet, message),
	} {
		t.Run(name, func(t *testing.T) {
			var destination string
			_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
				if link, ok := node.(*ast.Link); ok && entering {
					destination = string(link.Destination)
				}
				return ast.WalkContinue, nil
			})
			if destination != "https://example.com" {
				t.Fatalf("link destination = %q, want %q", destination, "https://example.com")
			}
		})
	}
}

func TestParseConcurrent(t *testing.T) {
	for range 8 {
		t.Run("worker", func(t *testing.T) {
			t.Parallel()
			for i := range 100 {
				large := i%2 == 0
				content := []byte("text <:wave:123>")
				if large {
					content = []byte("<:wave:123>")
				}
				if got := parsedEmojiLarge(Parse(content)); got != large {
					t.Fatalf("large emoji = %v, want %v", got, large)
				}
			}
		})
	}
}

func parsedEmojiLarge(root ast.Node) bool {
	var large bool
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if emoji, ok := node.(*Emoji); ok {
			large = emoji.Large
		}
		return ast.WalkContinue, nil
	})
	return large
}

func BenchmarkParse(b *testing.B) {
	content := []byte("hello **world** https://example.com `code` <:wave:123>")
	b.Run("plain", func(b *testing.B) {
		for b.Loop() {
			parseSink = Parse(content)
		}
	})
	b.Run("message", func(b *testing.B) {
		message := &discord.Message{}
		cabinet := store.Cabinet{}
		for b.Loop() {
			parseSink = ParseWithMessage(content, cabinet, message)
		}
	})
}

func dump(n ast.Node, src []byte) string {
	// goldmark is a dogshit library with a god awful API.
	// To work around this joke, we will just hijack os.Stdout and os.Stderr to
	// get the output of the Dump function.
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	r, w, _ := os.Pipe()

	os.Stdout = w
	os.Stderr = w

	go func() {
		n.Dump(src, 0)
		w.Close()
	}()

	var buf strings.Builder
	io.Copy(&buf, r)
	r.Close()

	return buf.String()
}
