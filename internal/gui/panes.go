package gui

import (
	"encoding/json"
	"image"
	"log/slog"
	"os"
	"path/filepath"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

type paneSettings struct {
	Channels unit.Dp `json:"channels"`
	Members  unit.Dp `json:"members"`
}

type paneResize struct {
	dragging bool
	hovered  bool
	// limit is the most the window allows.
	limit unit.Dp
	// origin is the window x, in pixels, that the width is measured from:
	// the left of the channels, or the right edge for the members.
	origin float32
}

func paneSettingsPath() string {
	return filepath.Join(filepath.Dir(logPath()), "panes.json")
}

func (c *chatView) paneWidths(gtx layout.Context) (int, int, bool) {
	if !c.panesLoaded {
		c.panes = paneSettings{Channels: 240, Members: 240}
		if !c.demo {
			if data, err := os.ReadFile(paneSettingsPath()); err == nil {
				var settings paneSettings
				if json.Unmarshal(data, &settings) == nil {
					if settings.Channels >= 160 && settings.Channels <= 480 {
						c.panes.Channels = settings.Channels
					}
					if settings.Members >= 160 && settings.Members <= 480 {
						c.panes.Members = settings.Members
					}
				}
			}
		}
		c.panesLoaded = true
	}
	changed := c.channelResize.update(gtx, &c.panes.Channels, 1)
	changed = c.memberResize.update(gtx, &c.panes.Members, -1) || changed
	if changed && !c.demo {
		c.savePanes()
	}
	available := max(gtx.Constraints.Max.X-gtx.Dp(72), 0)
	channels := min(gtx.Dp(c.panes.Channels), max(available-gtx.Dp(280), 0))
	members := gtx.Dp(c.panes.Members)
	show := c.membersShown() && available >= gtx.Dp(160+320+160)
	if show {
		channels = min(channels, max(available-gtx.Dp(320+160), 0))
		members = min(members, max(available-channels-gtx.Dp(320), 0))
	}
	dp := func(px int) unit.Dp { return unit.Dp(float32(px) / max(gtx.Metric.PxPerDp, 1e-3)) }
	c.channelResize.origin = float32(min(gtx.Dp(72), gtx.Constraints.Max.X))
	c.memberResize.origin = float32(gtx.Constraints.Max.X)
	c.channelResize.limit = dp(max(available-gtx.Dp(280), 0))
	if show {
		c.channelResize.limit = dp(max(available-gtx.Dp(320+160), 0))
		c.memberResize.limit = dp(max(available-channels-gtx.Dp(320), 0))
	}
	return channels, members, show
}

func (c *chatView) savePanes() {
	path := paneSettingsPath()
	data, err := json.Marshal(c.panes)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		slog.Warn("could not save sidebar widths", "err", err)
	}
}

func (r *paneResize) update(gtx layout.Context, width *unit.Dp, direction float32) bool {
	changed := false
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: r, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Enter | pointer.Leave})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Enter:
			r.hovered = true
		case pointer.Leave:
			r.hovered = false
		case pointer.Press:
			if e.Buttons != pointer.ButtonPrimary {
				continue
			}
			r.dragging = true
			gtx.Execute(pointer.GrabCmd{Tag: r, ID: e.PointerID})
		case pointer.Drag:
			// The edge goes where the pointer is, measured from the side of
			// the window, so that no event lost on the way can make it jump.
			// Windows may cancel the press as the drag begins; a held button
			// still drags.
			if !r.dragging && !e.Buttons.Contain(pointer.ButtonPrimary) {
				continue
			}
			r.dragging = true
			scale := max(gtx.Metric.PxPerDp, 1e-3)
			most := float32(480)
			if r.limit >= 160 {
				most = min(most, float32(r.limit))
			}
			*width = unit.Dp(min(max(direction*(e.Position.X-r.origin)/scale, 160), most))
		case pointer.Release:
			changed = changed || r.dragging
			r.dragging = false
		case pointer.Cancel:
			// Kept dragging: real input sends a cancel when the drag begins.
			changed = changed || r.dragging
			r.hovered = false
		}
	}
	return changed
}

func (r *paneResize) layout(gtx layout.Context, x int) {
	size := gtx.Constraints.Max
	half := max(gtx.Dp(3), 1)
	rect := image.Rect(max(x-half, 0), 0, min(x+half, size.X), size.Y)
	area := clip.Rect(rect).Push(gtx.Ops)
	pointer.CursorColResize.Add(gtx.Ops)
	event.Op(gtx.Ops, r)
	if r.hovered || r.dragging {
		paint.FillShape(gtx.Ops, colorAccent, clip.Rect(image.Rect(x-1, 0, x+1, size.Y)).Op())
	}
	area.Pop()
}
