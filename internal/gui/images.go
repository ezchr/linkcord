package gui

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "image/gif"
	_ "image/jpeg"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// imageState is how far the loading of an image got.
type imageState int

const (
	imageLoading imageState = iota
	imageReady
	imageFailed
)

type imageEntry struct {
	state imageState
	op    paint.ImageOp
}

// images loads small pictures such as avatars and server icons in the
// background, scales them to the size they are shown at, and keeps them in
// memory and on disk so that they download once.
type images struct {
	u *ui

	mu      sync.Mutex
	entries map[string]*imageEntry

	// sem limits the downloads at once, for slow connections.
	sem chan struct{}
	// decode limits the cached images read from disk at once, so a fast
	// scroll doesn't start dozens of decodes competing with the window.
	decode chan struct{}
	// redraw is set while a redraw for loaded images is pending.
	redraw atomic.Bool
	client *http.Client
	dir    string
}

func newImages(u *ui) *images {
	dir := filepath.Join(filepath.Dir(logPath()), "imagenes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("failed to create image cache dir", "err", err)
		dir = ""
	}
	return &images{
		u:       u,
		entries: make(map[string]*imageEntry),
		sem:     make(chan struct{}, 6),
		decode:  make(chan struct{}, 2),
		client:  &http.Client{Timeout: 30 * time.Second},
		dir:     dir,
	}
}

// get returns the picture at url scaled to size pixels, or nil while it loads
// or if it failed. The window is redrawn once it loads.
func (im *images) get(url string, size int) *paint.ImageOp {
	if url == "" {
		return nil
	}
	key := fmt.Sprintf("%d:%s", size, url)
	im.mu.Lock()
	defer im.mu.Unlock()
	e, ok := im.entries[key]
	if !ok {
		e = &imageEntry{state: imageLoading}
		im.entries[key] = e
		go im.load(key, url, size, e)
	}
	if e.state != imageReady {
		return nil
	}
	op := e.op
	return &op
}

func (im *images) load(key, url string, size int, e *imageEntry) {
	im.decode <- struct{}{}
	img, err := im.fromDisk(key)
	<-im.decode
	if err != nil {
		im.sem <- struct{}{}
		img, err = im.download(url, size)
		<-im.sem
		if err == nil {
			im.toDisk(key, img)
		}
	}
	im.mu.Lock()
	if err != nil {
		slog.Warn("failed to load image", "url", url, "err", err)
		e.state = imageFailed
	} else {
		e.op = paint.NewImageOp(img)
		e.state = imageReady
	}
	im.mu.Unlock()
	// One redraw at most every 50 ms, however many images finish loading.
	if err == nil && im.u != nil && im.redraw.CompareAndSwap(false, true) {
		time.AfterFunc(50*time.Millisecond, func() {
			im.redraw.Store(false)
			im.u.win.Invalidate()
		})
	}
}

func (im *images) download(url string, size int) (image.Image, error) {
	resp, err := im.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	// Avatars and icons are small; a limit keeps a broken server from
	// filling the memory.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return scale(src, size), nil
}

// scale returns src shrunk to fit a square of size pixels.
func scale(src image.Image, size int) image.Image {
	b := src.Bounds()
	if b.Dx() <= size && b.Dy() <= size {
		return src
	}
	w, h := size, size
	if b.Dx() > b.Dy() {
		h = size * b.Dy() / b.Dx()
	} else if b.Dy() > b.Dx() {
		w = size * b.Dx() / b.Dy()
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

func (im *images) path(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(im.dir, hex.EncodeToString(sum[:])+".png")
}

func (im *images) fromDisk(key string) (image.Image, error) {
	if im.dir == "" {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(im.path(key))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func (im *images) toDisk(key string, img image.Image) {
	if im.dir == "" {
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return
	}
	// Write then rename, so that a crash never leaves half a file.
	path := im.path(key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
	}
}

// cdnSize asks the Discord CDN for a picture of 128 pixels, enough for every
// place it is shown without downloading the full one.
func cdnSize(url string) string {
	if url == "" {
		return ""
	}
	return url + "?size=128"
}

// picture lays out the picture at url as a square of size, with corners of
// radius (negative for a circle), or the initials of name over bg while it
// loads or if there is none.
func (u *ui) picture(gtx layout.Context, url string, size unit.Dp, bg colorT, name string, radius int) layout.Dimensions {
	px := gtx.Dp(size)
	if radius < 0 {
		radius = px / 2
	}
	img := u.images.get(url, px)
	if img == nil {
		return avatar(gtx, u.th, size, bg, name, radius)
	}
	r := image.Rectangle{Max: image.Pt(px, px)}
	defer clip.UniformRRect(r, radius).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(r.Max)
	return widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
}
