package gui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitMessage(t *testing.T) {
	check := func(name string, s string, limit int) []string {
		t.Helper()
		parts := splitMessage(s, limit)
		for i, p := range parts {
			if n := utf8.RuneCountInString(p); n > limit {
				t.Errorf("%s: part %d has %d characters, over %d", name, i, n, limit)
			}
			if p == "" {
				t.Errorf("%s: part %d is empty", name, i)
			}
		}
		return parts
	}

	if got := check("short", "hola", 20); len(got) != 1 || got[0] != "hola" {
		t.Fatalf("short: %q", got)
	}

	words := strings.Repeat("palabra ", 30)
	parts := check("words", words, 50)
	if strings.Join(parts, " ") != strings.TrimSpace(words) {
		t.Fatalf("words lost or changed: %q", parts)
	}

	lines := strings.Repeat("una línea de texto\n", 10)
	for _, p := range check("lines", lines, 60) {
		if strings.HasPrefix(p, "\n") || strings.HasSuffix(p, "\n") {
			t.Fatalf("lines: part not cut at a line: %q", p)
		}
	}

	code := "mira:\n```go\n" + strings.Repeat("fmt.Println(1)\n", 10) + "```\nfin"
	parts = check("code", code, 70)
	for i, p := range parts {
		if strings.Count(p, "```")%2 != 0 {
			t.Fatalf("code: part %d leaves a block open: %q", i, p)
		}
	}
	if !strings.HasPrefix(parts[1], "```go\n") {
		t.Fatalf("code: next part does not reopen the block with its language: %q", parts[1])
	}

	if got := check("unbroken", strings.Repeat("x", 120), 50); len(got) != 3 {
		t.Fatalf("unbroken: %d parts", len(got))
	}
	if got := check("accents", strings.Repeat("ñ", 120), 50); strings.Join(got, "") != strings.Repeat("ñ", 120) {
		t.Fatalf("accents broken: %q", got)
	}
}
