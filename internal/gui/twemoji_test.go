package gui

import (
	"strings"
	"testing"
)

func TestTwemojiURL(t *testing.T) {
	for e, want := range map[string]string{
		"😀":          "72x72/1f600.png",
		"❤️":         "72x72/2764.png",
		"1️⃣":        "72x72/31-20e3.png",
		"🇫🇷":         "72x72/1f1eb-1f1f7.png",
		"👩‍💻":         "72x72/1f469-200d-1f4bb.png",
		"🏳️‍🌈":        "72x72/1f3f3-fe0f-200d-1f308.png",
		"👍🏽":          "72x72/1f44d-1f3fd.png",
	} {
		if got := twemojiURL(e, 48); !strings.HasSuffix(got, want) {
			t.Errorf("%q: %s, want …%s", e, got, want)
		}
	}
}

func TestSplitEmoji(t *testing.T) {
	parts := splitEmoji("hi 👋🏽 there ❤️ © 1")
	var got []string
	for _, p := range parts {
		if p.emoji {
			got = append(got, "["+p.text+"]")
		} else {
			got = append(got, p.text)
		}
	}
	if want := "hi |[👋🏽]| there |[❤️]| © 1"; strings.Join(got, "|") != want {
		t.Errorf("got %q, want %q", strings.Join(got, "|"), want)
	}
	if !jumboEmoji("😀 <:a:1>") || jumboEmoji("😀 hi") {
		t.Error("jumbo emoji")
	}
}
