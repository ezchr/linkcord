package gui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ezchr/linkcord/internal/install"
	"github.com/ezchr/linkcord/internal/tray"
	"github.com/ncruces/zenity"
	xdraw "golang.org/x/image/draw"
)

// The app's icon, chosen by the user: a picture is made into an .ico kept
// beside the settings, which the window (and so its taskbar button), the
// notification area and Linkcord's shortcuts then show. The executable file
// keeps the icon it was built with: Windows reads that from the file, which
// cannot be rewritten while it runs.

// iconSizes are the sizes of the icon made, the last kept as a PNG as
// Windows wants its large icons.
var iconSizes = []int{16, 24, 32, 48, 64, 256}

// maxIconFile is the largest picture read for an icon.
const maxIconFile = 32 << 20

// makeICO is src as an .ico: its middle square, in every size of iconSizes.
func makeICO(src image.Image) ([]byte, error) {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	if side <= 0 {
		return nil, errors.New("empty picture")
	}
	sq := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	var entries [][]byte
	for _, s := range iconSizes {
		img := image.NewNRGBA(image.Rect(0, 0, s, s))
		xdraw.CatmullRom.Scale(img, img.Bounds(), src, sq, xdraw.Src, nil)
		if s >= 256 {
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				return nil, err
			}
			entries = append(entries, buf.Bytes())
			continue
		}
		entries = append(entries, iconBitmap(img))
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&out, le, [3]uint16{0, 1, uint16(len(entries))})
	offset := 6 + 16*len(entries)
	for i, e := range entries {
		s := iconSizes[i]
		// A size of 256 is written as 0.
		out.Write([]byte{byte(s), byte(s), 0, 0})
		binary.Write(&out, le, [2]uint16{1, 32})
		binary.Write(&out, le, [2]uint32{uint32(len(e)), uint32(offset)})
		offset += len(e)
	}
	for _, e := range entries {
		out.Write(e)
	}
	return out.Bytes(), nil
}

// iconBitmap is img as an icon's bitmap: a header saying twice its height,
// its pixels from the bottom row up as BGRA, and a mask of nothing, as the
// pixels say how clear they are themselves.
func iconBitmap(img *image.NRGBA) []byte {
	s := img.Bounds().Dx()
	var out bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&out, le, struct {
		Size          uint32
		Width, Height int32
		Planes, Bits  uint16
		Rest          [6]uint32
	}{Size: 40, Width: int32(s), Height: int32(2 * s), Planes: 1, Bits: 32})
	for y := s - 1; y >= 0; y-- {
		for x := 0; x < s; x++ {
			c := img.NRGBAAt(x, y)
			out.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	out.Write(make([]byte, ((s+31)/32)*4*s))
	return out.Bytes()
}

// readIcon is the icon for the picture or .ico file at path.
func readIcon(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxIconFile+1))
	f.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > maxIconFile {
		return nil, errors.New("picture too large")
	}
	if strings.EqualFold(filepath.Ext(path), ".ico") {
		return data, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return makeICO(orient(src, exifOrientation(data)))
}

// pickIcon asks for a picture to make the app's icon of.
func (c *chatView) pickIcon() {
	owner := zenity.Attach(c.u.hwnd)
	go func() {
		path, err := zenity.SelectFile(zenity.Title(tr("App icon")), owner, zenity.FileFilter{Name: "Images", Patterns: []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.ico"}})
		if err != nil {
			return
		}
		ico, err := readIcon(path)
		var kept string
		if err == nil {
			// A new name each time: Windows remembers icons by their file.
			kept = filepath.Join(filepath.Dir(logPath()), fmt.Sprintf("icon-%d.ico", time.Now().Unix()))
			err = os.WriteFile(kept, ico, 0o600)
		}
		c.u.post(func() {
			cv := &c.settings.look.custom
			if err != nil {
				slog.Warn("could not make the icon", "err", err)
				cv.iconErr = "Could not make an icon of that picture."
				return
			}
			cv.iconErr = ""
			c.setIcon(kept)
		})
	}()
}

// setIcon makes the .ico at path the app's icon, or its own again if path
// is "", and keeps the choice.
func (c *chatView) setIcon(path string) {
	u := c.u
	old := u.appearance.Icon
	u.appearance.Icon = path
	if !c.demo {
		saveAppearance(u.appearance)
	}
	u.applyIcon()
	go func() {
		if err := install.SetShortcutIcons(path); err != nil {
			slog.Warn("could not change the shortcuts' icon", "err", err)
		}
		if old != "" && old != path && filepath.Dir(old) == filepath.Dir(logPath()) {
			os.Remove(old)
		}
	}()
}

// applyIcon shows the icon chosen on the window and in the notification
// area; a file that is gone leaves Linkcord's own.
func (u *ui) applyIcon() {
	path := u.appearance.Icon
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			path = ""
		}
	}
	tray.SetIconFile(path)
	setWindowIcon(u.hwnd, path)
}
