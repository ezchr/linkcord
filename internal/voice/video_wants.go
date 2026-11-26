package voice

import (
	"sort"
	"strconv"
	"time"
)

// The ladder of Discord's clients for the quality of the videos received
// (MediaSinkWantsManager): a budget of pixels, 1280×720 by default, is
// spread across the videos, and each is asked for at the want value, 10 to
// 100, of the biggest 16:9 size that fits its share, or that the tile it is
// shown in needs.

// videoBudget is the pixels received in all, as Discord's videoBudget.
const videoBudget = 1280 * 720

// ladderStep is a size of the ladder and the want value that asks for it.
type ladderStep struct {
	want          int
	width, height int
	pixels        int
}

// wantsLadder is the ladder for a budget, its steps by want value, least
// first.
type wantsLadder struct {
	budget int
	steps  []ladderStep
}

// newWantsLadder is calculateLadder and calculateOrderedLadder of the web
// client: the 16:9 sizes whose sides are multiples of 16 (or end in 4, 8
// or 10 past one), the largest of them that fits a 1/t share of the budget
// for t from 1 to 25, each new one a want value 10 lower, from 100.
func newWantsLadder(budget int) wantsLadder {
	ok := func(v int) bool { v %= 16; return v == 0 || v == 4 || v == 8 || v == 10 }
	type size struct{ w, h int }
	var sizes []size
	for h := 1; h < 4096; h++ {
		if 16*h%9 != 0 {
			continue
		}
		w := 16 * h / 9
		if ok(w) && ok(h) {
			sizes = append(sizes, size{w, h})
		}
	}
	var steps []ladderStep
	lastW, want := 0, 100
	for t := 1; t <= 25; t++ {
		var w, h int
		for _, s := range sizes {
			if s.w*s.h*t > budget {
				break
			}
			w, h = s.w, s.h
		}
		if w != lastW {
			if want > 0 {
				steps = append(steps, ladderStep{want: want, width: w, height: h, pixels: w * h})
			}
			want -= 10
			lastW = w
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].want < steps[j].want })
	return wantsLadder{budget: budget, steps: steps}
}

// maxSinkValue is getMaxSinkValue: the want value for one of videos videos,
// shown in pixels, 0 if unknown. Each takes its share of the budget, and no
// more than the step just above what it is shown in.
func (l wantsLadder) maxSinkValue(videos, pixels int) int {
	if len(l.steps) == 0 {
		return 100
	}
	v := l.steps[0].want
	for _, s := range l.steps {
		if pixels > 0 && pixels < s.pixels {
			v = s.want
			break
		}
		if s.pixels*videos > l.budget {
			break
		}
		v = s.want
	}
	return v
}

// offscreenTimeout is how long a video not shown is still received, and
// offscreenKept how many such are, the longest not shown stopped first, as
// Discord's clients do.
const (
	offscreenTimeout = 30 * time.Second
	offscreenKept    = 3
)

// cameraWant is what the wants are made of for one camera.
type cameraWant struct {
	video  uint32
	pixels int
	// hidden is since when it is not shown, zero while it is.
	hidden time.Time
}

// sinkWants is the media sink wants (op 15) for the cameras at now: "any"
// at 100, which keeps the audio as it is, each camera shown at the want
// value of its tile with its pixelCounts, and those not shown at 0 once
// hidden longer than offscreenTimeout or beyond the offscreenKept most
// recently hidden.
func sinkWants(l wantsLadder, cams []cameraWant, now time.Time) map[string]any {
	wants := map[string]any{"any": 100}
	pixels := map[string]int{}
	var hidden []cameraWant
	for _, c := range cams {
		if c.video == 0 {
			continue
		}
		if !c.hidden.IsZero() {
			hidden = append(hidden, c)
		}
	}
	// The most recently hidden first: those kept.
	sort.Slice(hidden, func(i, j int) bool { return hidden[i].hidden.After(hidden[j].hidden) })
	kept := map[uint32]bool{}
	for i, c := range hidden {
		if i < offscreenKept && now.Sub(c.hidden) < offscreenTimeout {
			kept[c.video] = true
		}
	}
	n := 0
	for _, c := range cams {
		if c.video != 0 && (c.hidden.IsZero() || kept[c.video]) {
			n++
		}
	}
	for _, c := range cams {
		if c.video == 0 {
			continue
		}
		key := strconv.FormatUint(uint64(c.video), 10)
		switch {
		case c.hidden.IsZero():
			wants[key] = l.maxSinkValue(max(n, 1), c.pixels)
			if c.pixels > 0 {
				pixels[key] = c.pixels
			}
		case kept[c.video]:
			wants[key] = l.maxSinkValue(max(n, 1), 0)
		default:
			wants[key] = 0
		}
	}
	if len(pixels) > 0 {
		wants["pixelCounts"] = pixels
	}
	return wants
}
