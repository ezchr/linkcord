package gui

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// mediaFiles are the pictures, GIFs and videos downloaded to copy or drag,
// by their link, kept in the temporary folder for the session.
var mediaFiles struct {
	sync.Mutex
	paths   map[string]string
	pending map[string]chan struct{}
}

// mediaTemp downloads the media at raw once, and returns its file.
func mediaTemp(raw string) (string, error) {
	mf := &mediaFiles
	mf.Lock()
	if mf.paths == nil {
		mf.paths, mf.pending = make(map[string]string), make(map[string]chan struct{})
	}
	if p, ok := mf.paths[raw]; ok {
		mf.Unlock()
		return p, nil
	}
	if ch, ok := mf.pending[raw]; ok {
		mf.Unlock()
		<-ch
		return mediaTemp(raw)
	}
	ch := make(chan struct{})
	mf.pending[raw] = ch
	mf.Unlock()
	p, err := fetchMediaTemp(raw)
	mf.Lock()
	delete(mf.pending, raw)
	if err == nil {
		mf.paths[raw] = p
	}
	mf.Unlock()
	close(ch)
	return p, err
}

func fetchMediaTemp(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	name := safeFileName(path.Base(u.Path))
	if name == "" || name == "." || name == "/" {
		name = "media"
	}
	sum := sha1.Sum([]byte(raw))
	dir := filepath.Join(os.TempDir(), "linkcord-media", hex.EncodeToString(sum[:6]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	resp, err := http.Get(raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := p + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 500<<20))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// isStillPicture reports whether the media at raw is a picture that does
// not move, which is copied as a picture rather than as a file.
func isStillPicture(raw string) bool {
	switch strings.ToLower(mediaKind(raw)) {
	case "png", "jpg", "jpeg", "webp", "bmp":
		return true
	}
	return false
}

// copyMedia copies the media at raw: a picture as a picture, to paste in
// any program, and a GIF or video as its file.
func (c *chatView) copyMedia(raw string) {
	go func() {
		p, err := mediaTemp(raw)
		if err == nil {
			if isStillPicture(raw) {
				var img image.Image
				img, err = decodeFile(p)
				if err == nil {
					err = copyImage(img)
				}
			} else {
				err = copyFile(p)
			}
		}
		c.u.post(func() {
			if err != nil {
				c.showNotice("Could not copy it: " + err.Error())
				return
			}
			c.showNotice("Copied to the clipboard")
		})
	}()
}

// dragMedia drags the media at raw to another window, as a file.
func (c *chatView) dragMedia(raw string) {
	go func() {
		p, err := mediaTemp(raw)
		if err == nil {
			err = dragFile(p)
		}
		if err != nil {
			c.u.post(func() { c.showNotice("Could not drag it: " + err.Error()) })
		}
	}()
}

// mediaDrag follows a press on a picture in the chat, which becomes a drag
// to another window once it moves.
type mediaDrag struct {
	from    f32.Point
	pressed bool
}

// mediaDragArea lets the picture of size, whose file is at raw, be dragged
// to another window, leaving its click to open it.
func (c *chatView) mediaDragArea(gtx layout.Context, raw string, size image.Point) {
	if c.mediaDrags == nil {
		c.mediaDrags = make(map[string]*mediaDrag)
	}
	d := c.mediaDrags[raw]
	if d == nil {
		d = &mediaDrag{}
		c.mediaDrags[raw] = d
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: d, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if e.Buttons == pointer.ButtonPrimary {
				d.from, d.pressed = e.Position, true
				// Fetched at once, to be ready if it is dragged.
				go mediaTemp(raw)
			}
		case pointer.Drag:
			if m := e.Position.Sub(d.from); d.pressed && m.X*m.X+m.Y*m.Y > 64 {
				d.pressed = false
				c.dragMedia(raw)
			}
		case pointer.Release, pointer.Cancel:
			d.pressed = false
		}
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, d)
}
