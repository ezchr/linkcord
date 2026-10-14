package gui

import (
	"fmt"
	"image"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
)

func TestImageCacheEviction(t *testing.T) {
	const entryBytes = imageCacheBudget / 2
	oldest := &imageEntry{state: imageReady, bytes: entryBytes, used: 1}
	recent := &imageEntry{state: imageReady, bytes: entryBytes, used: 3}
	current := &imageEntry{state: imageReady, bytes: entryBytes, used: 4}
	loading := &imageEntry{state: imageLoading}
	im := &images{
		cacheBytes: 3 * entryBytes,
		entries: map[string]*imageEntry{
			"oldest": oldest, "recent": recent, "current": current, "loading": loading,
		},
	}
	im.trimCacheLocked(current)
	if _, ok := im.entries["oldest"]; ok {
		t.Fatal("least recently used pixels were retained")
	}
	if im.entries["recent"] != recent || im.entries["current"] != current || im.entries["loading"] != loading {
		t.Fatal("recent or in-flight entries were evicted")
	}
	if im.cacheBytes != imageCacheBudget {
		t.Fatalf("cache bytes = %d, want %d", im.cacheBytes, imageCacheBudget)
	}
}

// TestImageCacheKeepsShown keeps the pictures on the window while a big one,
// as a GIF opened, holds the cache over its budget: released, they would
// load again and release one another over and over.
func TestImageCacheKeepsShown(t *testing.T) {
	big := &imageEntry{state: imageReady, bytes: imageCacheBudget}
	avatar := &imageEntry{state: imageReady, bytes: 1 << 10}
	old := &imageEntry{state: imageReady, bytes: 1 << 10, used: 1}
	im := &images{
		cacheBytes: imageCacheBudget + 2<<10,
		entries:    map[string]*imageEntry{"big": big, "avatar": avatar, "old": old},
		clock:      5,
	}
	im.newFrame()
	im.newFrame()
	avatar.used, big.used = 6, 7
	im.trimCacheLocked(big)
	if im.entries["avatar"] != avatar {
		t.Fatal("a picture on the window was released")
	}
	if _, ok := im.entries["old"]; ok {
		t.Fatal("a picture off the window was kept over the budget")
	}
}

func TestImageCacheEntryLimit(t *testing.T) {
	im := &images{entries: make(map[string]*imageEntry)}
	for i := range imageCacheEntries + 10 {
		im.entries[fmt.Sprint(i)] = &imageEntry{state: imageFailed, used: uint64(i)}
	}
	im.trimCacheLocked(nil)
	if len(im.entries) != imageCacheEntries {
		t.Fatalf("retained %d entries", len(im.entries))
	}
	for i := range 10 {
		if _, ok := im.entries[fmt.Sprint(i)]; ok {
			t.Fatal("old failure entries were retained")
		}
	}
}

func TestImagePendingLimit(t *testing.T) {
	im := &images{entries: make(map[string]*imageEntry), pendingLoads: imagePendingLoads}
	if im.get("https://example.invalid/image.png", 32) != nil || len(im.entries) != 0 {
		t.Fatal("a new worker was registered beyond the pending-load limit")
	}
}

func TestMinimizedAnimationDoesNotRedraw(t *testing.T) {
	first := paint.NewImageOp(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	second := paint.NewImageOp(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	im := &images{
		u: &ui{focused: false, minimized: true},
		entries: map[string]*imageEntry{
			"2:test": {
				state: imageReady, op: first, frames: []paint.ImageOp{first, second},
				delays: []time.Duration{time.Millisecond, time.Millisecond},
				cycle:  2 * time.Millisecond,
			},
		},
	}
	var ops op.Ops
	var router input.Router
	gtx := layout.Context{Ops: &ops, Source: router.Source(), Now: animEpoch.Add(time.Millisecond)}
	if im.frame(gtx, "test", 2) == nil {
		t.Fatal("background animation lost its poster")
	}
	router.Frame(&ops)
	if _, redraw := router.WakeupTime(); redraw {
		t.Fatal("background animation requested a redraw")
	}

	// A visible window in the background pauses too, as Discord plays GIFs
	// only while it is in front: the frames redraw the whole window.
	ops.Reset()
	var unfocusedRouter input.Router
	gtx.Source = unfocusedRouter.Source()
	im.u.minimized = false
	gtx.Now = animEpoch
	im.frame(gtx, "test", 2)
	unfocusedRouter.Frame(&ops)
	if _, redraw := unfocusedRouter.WakeupTime(); redraw {
		t.Fatal("background window requested a redraw")
	}

	// In front, it plays at its own pace.
	ops.Reset()
	var focusedRouter input.Router
	gtx.Source = focusedRouter.Source()
	im.u.focused = true
	im.frame(gtx, "test", 2)
	focusedRouter.Frame(&ops)
	at, redraw := focusedRouter.WakeupTime()
	if !redraw || !at.Equal(gtx.Now.Add(time.Millisecond)) {
		t.Fatalf("focused window lost original frame timing: %v, %v", at, redraw)
	}

	ops.Reset()
	var lowPowerRouter input.Router
	gtx.Source = lowPowerRouter.Source()
	im.lowPower = true
	im.frame(gtx, "test", 2)
	lowPowerRouter.Frame(&ops)
	if _, redraw := lowPowerRouter.WakeupTime(); redraw {
		t.Fatal("low-power preview requested an animation redraw")
	}
}
