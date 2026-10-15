package gui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Copying pictures and files to the clipboard, and dragging files to other
// windows, as Explorer and Discord do. Gio's clipboard only holds text.

var (
	msUser32  = windows.NewLazySystemDLL("user32.dll")
	msKernel  = windows.NewLazySystemDLL("kernel32.dll")
	msShell32 = windows.NewLazySystemDLL("shell32.dll")
	msOle32   = windows.NewLazySystemDLL("ole32.dll")

	msOpenClipboard      = msUser32.NewProc("OpenClipboard")
	msCloseClipboard     = msUser32.NewProc("CloseClipboard")
	msEmptyClipboard     = msUser32.NewProc("EmptyClipboard")
	msSetClipboardData   = msUser32.NewProc("SetClipboardData")
	msRegisterClipFormat = msUser32.NewProc("RegisterClipboardFormatW")
	msGetForegroundWnd   = msUser32.NewProc("GetForegroundWindow")
	msGetWindowThread    = msUser32.NewProc("GetWindowThreadProcessId")
	msAttachThreadInput  = msUser32.NewProc("AttachThreadInput")
	msGetCurrentThread   = msKernel.NewProc("GetCurrentThreadId")
	msGlobalAlloc        = msKernel.NewProc("GlobalAlloc")
	msGlobalLock         = msKernel.NewProc("GlobalLock")
	msGlobalUnlock       = msKernel.NewProc("GlobalUnlock")
	msSHParseDisplayName = msShell32.NewProc("SHParseDisplayName")
	msSHCreateDataObject = msShell32.NewProc("SHCreateDataObject")
	msSHDoDragDrop       = msShell32.NewProc("SHDoDragDrop")
	msOleInitialize      = msOle32.NewProc("OleInitialize")
	msOleUninitialize    = msOle32.NewProc("OleUninitialize")
	msCoTaskMemFree      = msOle32.NewProc("CoTaskMemFree")
)

const (
	msCfDIB          = 8
	msCfHDROP        = 15
	msGmemMoveable   = 0x0002
	msDropEffectCopy = 1
)

// msGlobalCopy copies data into memory the clipboard can own.
func msGlobalCopy(data []byte) (uintptr, error) {
	h, _, err := msGlobalAlloc.Call(msGmemMoveable, uintptr(len(data)))
	if h == 0 {
		return 0, err
	}
	p, _, err := msGlobalLock.Call(h)
	if p == 0 {
		return 0, err
	}
	copy(unsafe.Slice((*byte)(unsafe.Add(nil, p)), len(data)), data)
	msGlobalUnlock.Call(h)
	return h, nil
}

// setClipboard puts data in the clipboard in each of its formats.
func setClipboard(formats map[uintptr][]byte) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, err := msOpenClipboard.Call(0); r == 0 {
		return err
	}
	defer msCloseClipboard.Call()
	msEmptyClipboard.Call()
	for f, data := range formats {
		h, err := msGlobalCopy(data)
		if err != nil {
			return err
		}
		if r, _, err := msSetClipboardData.Call(f, h); r == 0 {
			return err
		}
	}
	return nil
}

// copyImage puts a picture in the clipboard, as PNG, which keeps its
// transparency, and as a bitmap, which every program reads.
func copyImage(img image.Image) error {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("PNG")
	pngFormat, _, _ := msRegisterClipFormat.Call(uintptr(unsafe.Pointer(name)))
	formats := map[uintptr][]byte{msCfDIB: imageDIB(img)}
	if pngFormat != 0 {
		formats[pngFormat] = pngData.Bytes()
	}
	return setClipboard(formats)
}

// imageDIB is img as a 32-bit device-independent bitmap, bottom row first.
func imageDIB(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, 40+w*h*4)
	binary.LittleEndian.PutUint32(out[0:], 40)
	binary.LittleEndian.PutUint32(out[4:], uint32(w))
	binary.LittleEndian.PutUint32(out[8:], uint32(h))
	binary.LittleEndian.PutUint16(out[12:], 1)
	binary.LittleEndian.PutUint16(out[14:], 32)
	binary.LittleEndian.PutUint32(out[20:], uint32(w*h*4))
	px := out[40:]
	for y := 0; y < h; y++ {
		row := px[(h-1-y)*w*4:]
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = byte(bl>>8), byte(g>>8), byte(r>>8), byte(a>>8)
		}
	}
	return out
}

// copyFile puts the file at path in the clipboard, to paste it as a file.
func copyFile(path string) error {
	u16, err := windows.UTF16FromString(path)
	if err != nil {
		return err
	}
	// DROPFILES, then the path, then an empty one ending the list.
	data := make([]byte, 20, 20+len(u16)*2+2)
	binary.LittleEndian.PutUint32(data[0:], 20)
	binary.LittleEndian.PutUint32(data[16:], 1)
	for _, c := range u16 {
		data = binary.LittleEndian.AppendUint16(data, c)
	}
	data = append(data, 0, 0)
	return setClipboard(map[uintptr][]byte{msCfHDROP: data})
}

var iidIDataObject = windows.GUID{Data1: 0x0000010e, Data2: 0, Data3: 0, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

// dragFile drags the file at path out of Linkcord, to drop it on a folder,
// the desktop or another program. It returns once it is dropped or let go.
func dragFile(path string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	msOleInitialize.Call(0)
	defer msOleUninitialize.Call()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var pidl uintptr
	if hr, _, _ := msSHParseDisplayName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0); hr != 0 || pidl == 0 {
		return errors.New("the file could not be found by Windows")
	}
	defer msCoTaskMemFree.Call(pidl)
	var obj uintptr
	if hr, _, _ := msSHCreateDataObject.Call(0, 1, uintptr(unsafe.Pointer(&pidl)), 0, uintptr(unsafe.Pointer(&iidIDataObject)), uintptr(unsafe.Pointer(&obj))); hr != 0 || obj == 0 {
		return errors.New("Windows could not prepare the file to drag")
	}
	// Released by its own method, the third of its table.
	defer func() {
		vtbl := *(*[3]uintptr)(unsafe.Add(nil, *(*uintptr)(unsafe.Add(nil, obj))))
		syscall.SyscallN(vtbl[2], obj)
	}()
	// The drag runs on this thread, which shares the window's input, so that
	// it sees the button held there: on its own, the button reads as up and
	// the drag ends at once.
	hwnd, _, _ := msGetForegroundWnd.Call()
	if hwnd != 0 {
		window, _, _ := msGetWindowThread.Call(hwnd, 0)
		self, _, _ := msGetCurrentThread.Call()
		if window != 0 && window != self {
			if r, _, _ := msAttachThreadInput.Call(self, window, 1); r != 0 {
				defer msAttachThreadInput.Call(self, window, 0)
			}
		}
	}
	var effect uint32
	msSHDoDragDrop.Call(hwnd, obj, 0, msDropEffectCopy, uintptr(unsafe.Pointer(&effect)))
	return nil
}
