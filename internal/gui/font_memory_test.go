package gui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gioui.org/font/opentype"
)

// Optional diagnostic: isolates the retained cost of each fallback font.
func TestFontMemory(t *testing.T) {
	if os.Getenv("LINKCORD_TEST_FONT_MEMORY") != "1" {
		t.Skip("LINKCORD_TEST_FONT_MEMORY is not set")
	}
	for _, name := range []string{"Noto Color Emoji", "segoeui.ttf", "seguisym.ttf", "cambria.ttc", "consola.ttf", "arial.ttf"} {
		t.Run(name, func(t *testing.T) {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			var data []byte
			var err error
			if name == "Noto Color Emoji" {
				data = narrowSpace(colorEmoji)
			} else {
				data, err = os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "Fonts", name))
				if err != nil {
					t.Skip(err)
				}
			}
			faces, err := opentype.ParseCollection(data)
			if err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			t.Logf("file %.1f MB; parsed %d faces; retained %.1f MB",
				float64(len(data))/(1<<20), len(faces),
				float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
			for _, r := range "╭╰【】" {
				covered := false
				for _, face := range faces {
					_, ok := face.Face.Face().NominalGlyph(r)
					covered = covered || ok
				}
				t.Logf("%c U+%04X covered: %v", r, r, covered)
			}
			runtime.KeepAlive(faces)
		})
	}
}
