package gui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"syscall"
	"time"
	"unsafe"
)

// Reading files and pictures from the Windows clipboard, which Gio's
// clipboard does not offer: it only carries text.

var (
	shell32                        = syscall.NewLazyDLL("shell32.dll")
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

const (
	cfDIB   = 8
	cfHDROP = 15
	cfDIBV5 = 17
)

// clipboardContents is what the clipboard holds besides text: files copied in
// Explorer, or a picture as PNG data.
type clipboardContents struct {
	files []string
	png   []byte
}

// readClipboard returns the files or picture on the clipboard, if any.
func readClipboard() (clipboardContents, error) {
	var out clipboardContents
	// Another program may hold the clipboard for a moment.
	opened := false
	for range 10 {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !opened {
		return out, errors.New("the clipboard is busy")
	}
	defer procCloseClipboard.Call()

	if available(cfHDROP) {
		if h, _, _ := procGetClipboardData.Call(cfHDROP); h != 0 {
			out.files = dropFiles(h)
		}
		if len(out.files) > 0 {
			return out, nil
		}
	}
	// Browsers and the snipping tools put the picture as PNG too, which
	// keeps its transparency; the bitmap formats are the fallback.
	name, _ := syscall.UTF16PtrFromString("PNG")
	if f, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name))); f != 0 && available(f) {
		if data := globalBytes(f); len(data) > 0 {
			out.png = data
			return out, nil
		}
	}
	for _, f := range []uintptr{cfDIBV5, cfDIB} {
		if !available(f) {
			continue
		}
		if img, err := decodeDIB(globalBytes(f)); err == nil {
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err == nil {
				out.png = buf.Bytes()
				return out, nil
			}
		}
	}
	return out, nil
}

func available(format uintptr) bool {
	r, _, _ := procIsClipboardFormatAvailable.Call(format)
	return r != 0
}

// dropFiles lists the paths of a CF_HDROP handle.
func dropFiles(h uintptr) []string {
	n, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	var files []string
	for i := uintptr(0); i < n; i++ {
		size, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
		if size == 0 {
			continue
		}
		buf := make([]uint16, size+1)
		procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		files = append(files, syscall.UTF16ToString(buf))
	}
	return files
}

// globalBytes copies the data of a clipboard format held in global memory.
func globalBytes(format uintptr) []byte {
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil
	}
	size, _, _ := procGlobalSize.Call(h)
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 || size == 0 {
		return nil
	}
	defer procGlobalUnlock.Call(h)
	// The handle is memory locked by GlobalLock until GlobalUnlock.
	return bytes.Clone(unsafe.Slice((*byte)(unsafe.Add(nil, p)), size))
}

// decodeDIB decodes a device-independent bitmap of 24 or 32 bits per pixel,
// as Windows puts screenshots on the clipboard.
func decodeDIB(data []byte) (image.Image, error) {
	if len(data) < 40 {
		return nil, errors.New("bitmap too short")
	}
	le := binary.LittleEndian
	headerSize := int(le.Uint32(data[0:]))
	width := int(int32(le.Uint32(data[4:])))
	height := int(int32(le.Uint32(data[8:])))
	bits := int(le.Uint16(data[14:]))
	compression := le.Uint32(data[16:])
	colorsUsed := int(le.Uint32(data[32:]))
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width <= 0 || height <= 0 || width > 1<<15 || height > 1<<15 || (bits != 24 && bits != 32) {
		return nil, errors.New("unsupported bitmap")
	}
	// BI_RGB or BI_BITFIELDS with the usual masks; the masks of a plain
	// BITMAPINFOHEADER follow it.
	offset := headerSize + colorsUsed*4
	if compression == 3 && headerSize == 40 {
		offset += 12
	} else if compression != 0 && compression != 3 {
		return nil, errors.New("compressed bitmap")
	}
	stride := (width*bits/8 + 3) &^ 3
	if offset+stride*height > len(data) {
		return nil, errors.New("bitmap truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	anyAlpha := false
	for y := range height {
		row := data[offset+y*stride:]
		dy := height - 1 - y
		if topDown {
			dy = y
		}
		for x := range width {
			var c color.NRGBA
			if bits == 32 {
				p := row[x*4:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: p[3]}
				anyAlpha = anyAlpha || p[3] != 0
			} else {
				p := row[x*3:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: 0xff}
			}
			img.SetNRGBA(x, dy, c)
		}
	}
	// Most programs leave the fourth byte at zero, which would make the
	// whole picture transparent.
	if bits == 32 && !anyAlpha {
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0xff
		}
	}
	return img, nil
}
