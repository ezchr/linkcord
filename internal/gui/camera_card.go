package gui

import (
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/golive"
)

// cameraView is the camera of someone in the call as last drawn: op is made
// again only when the camera gives a new picture.
type cameraView struct {
	r        *golive.VideoReceiver
	op       paint.ImageOp
	shown    *image.RGBA
	shownGPU *golive.SharedFrame
}

// cameraReceiver is the receiver of the camera of id in the voice channel
// joined, nil while it is off.
func (c *chatView) cameraReceiver(id discord.UserID) *golive.VideoReceiver {
	s := c.u.voice.session
	if s == nil {
		return nil
	}
	r, _ := s.Camera(id).(*golive.VideoReceiver)
	return r
}

// cameraFrame is the latest picture of the camera r of id, or nil.
func (c *chatView) cameraFrame(id discord.UserID, r *golive.VideoReceiver) *paint.ImageOp {
	if c.cams == nil {
		c.cams = make(map[discord.UserID]*cameraView)
	}
	v := c.cams[id]
	if v == nil || v.r != r {
		v = &cameraView{r: r}
		c.cams[id] = v
	}
	if f := r.SharedFrame(); f != nil {
		if f != v.shownGPU {
			v.op, v.shown, v.shownGPU = paint.NewSharedImageOp(f), nil, f
		}
		return &v.op
	}
	v.shownGPU = nil
	img := r.Frame()
	if img == nil {
		return nil
	}
	if img != v.shown {
		v.op, v.shown = paint.NewImageOp(img), img
	}
	return &v.op
}

// forgetCameras drops what was drawn of cameras that are off.
func (c *chatView) forgetCameras() {
	for id, v := range c.cams {
		if c.cameraReceiver(id) != v.r {
			delete(c.cams, id)
		}
	}
}

// layoutCameraCard fills a card with the camera of vs and reports true
// while it has a picture. The size it is shown at is told each frame, which
// the quality asked for of the voice server and the size it is decoded at
// follow. Without a picture, a note under the avatar at y says why.
func (c *chatView) layoutCameraCard(gtx layout.Context, vs discord.VoiceState, size image.Point, radius, y int) (bool, func()) {
	r := c.cameraReceiver(vs.UserID)
	if r == nil {
		if c.cams[vs.UserID] != nil {
			delete(c.cams, vs.UserID)
		}
		// Cameras are only received in the voice channel joined.
		if st := c.u.voiceStatus(); !vs.SelfVideo || !st.Joined || st.ChannelID != vs.ChannelID {
			return false, nil
		}
		// Their camera is on, but no video has been told of yet.
		return false, c.cameraNote(gtx, "Camera loading…", size, y)
	}
	r.SetView(size.X, size.Y)
	frame := c.cameraFrame(vs.UserID, r)
	if frame == nil {
		// Asked for again while the card shows.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second)})
		text := "Camera loading…"
		if r.Unavailable() {
			text = "This camera cannot be shown here"
		}
		return false, c.cameraNote(gtx, text, size, y)
	}
	defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, rgb(0x000000), clip.Rect{Max: size}.Op())
	igtx := gtx
	igtx.Constraints = layout.Exact(size)
	b := frame.Size()
	if b.X <= 0 || b.Y <= 0 {
		return true, nil
	}
	scale := min(float32(size.X)/float32(b.X), float32(size.Y)/float32(b.Y)) / gtx.Metric.PxPerDp
	layout.Center.Layout(igtx, func(gtx layout.Context) layout.Dimensions {
		return widget.Image{Src: *frame, Fit: widget.Contain, Position: layout.Center, Scale: scale}.Layout(gtx)
	})
	return true, nil
}

// cameraNote is a note drawn over a card, in the middle at y.
func (c *chatView) cameraNote(gtx layout.Context, text string, size image.Point, y int) func() {
	macro := op.Record(gtx.Ops)
	ngtx := gtx
	ngtx.Constraints.Min = image.Point{}
	ngtx.Constraints.Max.X = size.X
	d := rounded(ngtx, withAlpha(rgb(0x000000), 0xb0), 6, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx,
			label(c.u.th, 12, colorHeader, text).Layout)
	})
	call := macro.Stop()
	at := image.Pt((size.X-d.Size.X)/2, max(min(y, size.Y-d.Size.Y-gtx.Dp(36)), 0))
	return func() {
		t := op.Offset(at).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
	}
}
