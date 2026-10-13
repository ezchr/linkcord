package gui

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "image/jpeg"

	"gioui.org/layout"
	"gioui.org/op"
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
	// op is the picture, or the first frame of an animation.
	op paint.ImageOp
	// frames and delays are the frames of an animated GIF and how long each
	// shows; cycle is how long they all take.
	frames []paint.ImageOp
	delays []time.Duration
	cycle  time.Duration
}

// animEpoch is when animations start, so that the same GIF shown twice
// moves in step.
var animEpoch = time.Now()

// animBudget is how many bytes the decoded frames of one animation may take;
// longer or bigger ones keep fewer frames.
const animBudget = 48 << 20

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
// or if it failed. The window is redrawn once it loads. An animated GIF
// returns its first frame; frame returns the one due.
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

// frame returns the picture at url scaled to size pixels like get, and for an
// animated GIF the frame due now, asking for a redraw when the next is due.
func (im *images) frame(gtx layout.Context, url string, size int) *paint.ImageOp {
	img := im.get(url, size)
	if img == nil {
		return nil
	}
	im.mu.Lock()
	defer im.mu.Unlock()
	e := im.entries[fmt.Sprintf("%d:%s", size, url)]
	if len(e.frames) < 2 || e.cycle <= 0 {
		return img
	}
	now := gtx.Now
	if now.IsZero() {
		now = time.Now()
	}
	pos := now.Sub(animEpoch) % e.cycle
	for i, d := range e.delays {
		if pos < d {
			gtx.Execute(op.InvalidateCmd{At: now.Add(d - pos)})
			f := e.frames[i]
			return &f
		}
		pos -= d
	}
	return img
}

// failed reports whether the picture at url could not be loaded.
func (im *images) failed(url string, size int) bool {
	im.mu.Lock()
	defer im.mu.Unlock()
	e, ok := im.entries[fmt.Sprintf("%d:%s", size, url)]
	return ok && e.state == imageFailed
}

func (im *images) load(key, url string, size int, e *imageEntry) {
	im.decode <- struct{}{}
	frames, delays, err := im.animFromDisk(key, size)
	var img image.Image
	if err != nil {
		img, err = im.fromDisk(stillKey(key, url))
	}
	<-im.decode
	if err != nil {
		frames, delays, img, err = im.download(key, url, size)
	}
	im.mu.Lock()
	switch {
	case err != nil:
		slog.Warn("failed to load image", "url", url, "err", err)
		e.state = imageFailed
	case len(frames) > 1:
		for _, f := range frames {
			e.frames = append(e.frames, paint.NewImageOp(f))
		}
		e.delays = delays
		for _, d := range delays {
			e.cycle += d
		}
		e.op = e.frames[0]
		e.state = imageReady
	default:
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

// download fetches the picture at url and keeps it on disk: the frames of an
// animated GIF, or a still picture.
func (im *images) download(key, url string, size int) ([]image.Image, []time.Duration, image.Image, error) {
	im.sem <- struct{}{}
	data, err := im.fetch(url)
	<-im.sem
	if err != nil {
		return nil, nil, nil, err
	}
	if isGIF(data) {
		frames, delays, err := decodeGIF(data, size)
		if err == nil && len(frames) > 1 {
			im.animToDisk(key, data)
			return frames, delays, nil, nil
		}
	}
	img, err := decodeScaled(data, size)
	if err != nil {
		return nil, nil, nil, err
	}
	im.toDisk(stillKey(key, url), img)
	return nil, nil, img, nil
}

// stillKey is the key a still picture is kept on disk under. Versions before
// animation kept the first frame of every GIF as a still under its key; the
// stills of GIF addresses moved to another key, so those are not taken for
// the animation.
func stillKey(key, url string) string {
	path, _, _ := strings.Cut(strings.ToLower(url), "?")
	if strings.HasSuffix(path, ".gif") {
		return key + "#still"
	}
	return key
}

// fetch downloads the picture at url.
func (im *images) fetch(url string) ([]byte, error) {
	resp, err := im.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	// Pictures shown in the chat are resized by Discord; a limit keeps a
	// broken server from filling the memory.
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// decodeScaled decodes a still picture and shrinks it to size pixels.
func decodeScaled(data []byte, size int) (image.Image, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return scale(src, size), nil
}

func isGIF(data []byte) bool {
	return bytes.HasPrefix(data, []byte("GIF8"))
}

// decodeGIF decodes the frames of a GIF as they are seen, each drawn over
// the ones before as the GIF says, shrunk to size pixels. A GIF of one frame
// returns no frames. Frames beyond what animBudget allows are merged into
// the ones kept, so the animation keeps its speed.
func decodeGIF(data []byte, size int) ([]image.Image, []time.Duration, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	if len(g.Image) < 2 {
		return nil, nil, nil
	}
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	if bounds.Empty() {
		bounds = g.Image[0].Bounds()
	}
	sw, sh := fitSize(bounds.Dx(), bounds.Dy(), size)
	keep := max(1, min(len(g.Image), animBudget/max(sw*sh*4, 1)))
	step := (len(g.Image) + keep - 1) / keep

	canvas := image.NewRGBA(bounds)
	var frames []image.Image
	var delays []time.Duration
	for i, f := range g.Image {
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		var before *image.RGBA
		if disposal == gif.DisposalPrevious {
			before = image.NewRGBA(bounds)
			draw.Draw(before, bounds, canvas, bounds.Min, draw.Src)
		}
		draw.Draw(canvas, f.Bounds(), f, f.Bounds().Min, draw.Over)
		// Like browsers, delays of 0 or 1 hundredths of a second show for a
		// tenth.
		d := 100 * time.Millisecond
		if i < len(g.Delay) && g.Delay[i] > 1 {
			d = time.Duration(g.Delay[i]) * 10 * time.Millisecond
		}
		if i%step == 0 {
			frames = append(frames, scaleCopy(canvas, size))
			delays = append(delays, d)
		} else {
			delays[len(delays)-1] += d
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, f.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = before
		}
	}
	return frames, delays, nil
}

// fitSize is the size of a w×h picture shrunk to fit a square of size.
func fitSize(w, h, size int) (int, int) {
	if w <= size && h <= size {
		return max(w, 1), max(h, 1)
	}
	if w > h {
		return size, max(size*h/w, 1)
	}
	return max(size*w/h, 1), size
}

// scale returns src shrunk to fit a square of size pixels.
func scale(src image.Image, size int) image.Image {
	b := src.Bounds()
	if b.Dx() <= size && b.Dy() <= size {
		return src
	}
	w, h := fitSize(b.Dx(), b.Dy(), size)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// scaleCopy is scale that never returns src itself, for a canvas that keeps
// changing.
func scaleCopy(src *image.RGBA, size int) image.Image {
	b := src.Bounds()
	w, h := fitSize(b.Dx(), b.Dy(), size)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	}
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
	writeFile(im.path(key), buf.Bytes())
}

// Animated GIFs are kept as downloaded, since a PNG holds one frame, and
// decoded again when loaded.
func (im *images) animPath(key string) string {
	return strings.TrimSuffix(im.path(key), ".png") + ".gif"
}

func (im *images) animToDisk(key string, data []byte) {
	if im.dir != "" {
		writeFile(im.animPath(key), data)
	}
}

func (im *images) animFromDisk(key string, size int) ([]image.Image, []time.Duration, error) {
	if im.dir == "" {
		return nil, nil, os.ErrNotExist
	}
	data, err := os.ReadFile(im.animPath(key))
	if err != nil {
		return nil, nil, err
	}
	frames, delays, err := decodeGIF(data, size)
	if err == nil && len(frames) < 2 {
		err = errors.New("not animated")
	}
	return frames, delays, err
}

// writeFile writes then renames, so that a crash never leaves half a file.
func writeFile(path string, data []byte) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
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
// loads or if there is none. Animated GIFs move.
func (u *ui) picture(gtx layout.Context, url string, size unit.Dp, bg colorT, name string, radius int) layout.Dimensions {
	px := gtx.Dp(size)
	if radius < 0 {
		radius = px / 2
	}
	img := u.images.frame(gtx, url, px)
	if img == nil {
		return avatar(gtx, u.th, size, bg, name, radius)
	}
	r := image.Rectangle{Max: image.Pt(px, px)}
	defer clip.UniformRRect(r, radius).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(r.Max)
	return widget.Image{Src: *img, Fit: widget.Cover}.Layout(gtx)
}
