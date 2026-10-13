// Package tray shows Linkcord's icon in the notification area of the
// taskbar while its window is hidden, and shows or hides that window.
package tray

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procSetMenuDefaultItem  = user32.NewProc("SetMenuDefaultItem")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindowAsync     = user32.NewProc("ShowWindowAsync")
	procIsIconic            = user32.NewProc("IsIconic")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull        = 0x0000
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmCommand     = 0x0111
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmApp         = 0x8000
	wmTrayIcon    = wmApp + 1
	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nifMessage    = 0x1
	nifIcon       = 0x2
	nifTip        = 0x4
	nifInfo       = 0x10
	niifInfo      = 0x1
	imageIcon     = 1
	smCxSmIcon    = 49
	smCySmIcon    = 50
	mfString      = 0x0
	mfSeparator   = 0x800
	tpmRightBtn   = 0x2
	tpmReturnCmd  = 0x100
	tpmBottomAlgn = 0x20
	swHide        = 0
	swShow        = 5
	swRestore     = 9
	idiApp        = 32512
	cmdOpen       = 1
	cmdQuit       = 2
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type msg struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

type notifyIconData struct {
	size            uint32
	hwnd            windows.Handle
	id              uint32
	flags           uint32
	callbackMessage uint32
	icon            windows.Handle
	tip             [128]uint16
	state           uint32
	stateMask       uint32
	info            [256]uint16
	version         uint32
	infoTitle       [64]uint16
	infoFlags       uint32
	guid            windows.GUID
	balloonIcon     windows.Handle
}

// Tray is the icon in the notification area.
type Tray struct {
	hwnd windows.Handle
	nid  notifyIconData
	// taskbarCreated is the message Explorer sends when it starts again,
	// after which the icon must be added again.
	taskbarCreated uint32
	onOpen, onQuit func()
	openText       string
	quitText       string

	mu    sync.Mutex
	shown bool
}

var (
	current   *Tray
	classOnce sync.Once
	classErr  error
	className = windows.StringToUTF16Ptr("LinkcordTray")
)

// Start runs the icon, with tip as its tooltip. Clicking it calls onOpen,
// and its menu has open and quit items, written openText and quitText. The
// icon is not shown until Show is called.
func Start(tip, openText, quitText string, onOpen, onQuit func()) (*Tray, error) {
	t := &Tray{onOpen: onOpen, onQuit: onQuit, openText: openText, quitText: quitText}
	ready := make(chan error, 1)
	go t.run(tip, ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Tray) run(tip string, ready chan<- error) {
	// The window and its messages belong to this thread.
	runtime.LockOSThread()
	instance, _, _ := procGetModuleHandleW.Call(0)
	classOnce.Do(func() {
		wc := wndClassEx{wndProc: windows.NewCallback(wndProc), instance: windows.Handle(instance), className: className}
		wc.size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = err
		}
	})
	if classErr != nil {
		ready <- classErr
		return
	}
	// A hidden top-level window, unlike a message-only one, hears that
	// Explorer started again.
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- err
		return
	}
	t.hwnd = windows.Handle(hwnd)
	tc, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TaskbarCreated"))))
	t.taskbarCreated = uint32(tc)

	t.nid.size = uint32(unsafe.Sizeof(t.nid))
	t.nid.hwnd = t.hwnd
	t.nid.id = 1
	t.nid.flags = nifMessage | nifIcon | nifTip
	t.nid.callbackMessage = wmTrayIcon
	t.nid.icon = appIcon(instance)
	copy(t.nid.tip[:len(t.nid.tip)-1], windows.StringToUTF16(tip))
	current = t
	ready <- nil

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// appIcon is the icon of the executable, small, or Windows' own.
func appIcon(instance uintptr) windows.Handle {
	cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	if h, _, _ := procLoadImageW.Call(instance, 1, imageIcon, cx, cy, 0); h != 0 {
		return windows.Handle(h)
	}
	h, _, _ := procLoadIconW.Call(0, idiApp)
	return windows.Handle(h)
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	t := current
	if t == nil || windows.Handle(hwnd) != t.hwnd {
		r, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
		return r
	}
	switch uint32(message) {
	case wmTrayIcon:
		switch uint32(lParam) {
		case wmLButtonUp, wmLButtonDbl:
			go t.onOpen()
		case wmRButtonUp:
			t.menu()
		}
		return 0
	case wmClose:
		t.notify(nimDelete)
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	if t.taskbarCreated != 0 && uint32(message) == t.taskbarCreated {
		t.mu.Lock()
		if t.shown {
			t.notify(nimAdd)
		}
		t.mu.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

// menu shows the menu of the icon where the mouse is.
func (t *Tray) menu() {
	m, _, _ := procCreatePopupMenu.Call()
	if m == 0 {
		return
	}
	defer procDestroyMenu.Call(m)
	procAppendMenuW.Call(m, mfString, cmdOpen, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(t.openText))))
	procAppendMenuW.Call(m, mfSeparator, 0, 0)
	procAppendMenuW.Call(m, mfString, cmdQuit, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(t.quitText))))
	procSetMenuDefaultItem.Call(m, cmdOpen, 0)
	var pt struct{ x, y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without this, the menu does not close when clicking elsewhere.
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(m, tpmRightBtn|tpmReturnCmd|tpmBottomAlgn, uintptr(pt.x), uintptr(pt.y), 0, uintptr(t.hwnd), 0)
	procPostMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)
	switch cmd {
	case cmdOpen:
		go t.onOpen()
	case cmdQuit:
		go t.onQuit()
	}
}

func (t *Tray) notify(op uintptr) bool {
	t.nid.flags &^= nifInfo
	r, _, _ := procShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&t.nid)))
	return r != 0
}

// Show shows the icon, and the balloon with title and text if text is not
// "".
func (t *Tray) Show(title, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.shown {
		if !t.notify(nimAdd) {
			return errors.New("tray: the icon could not be added")
		}
		t.shown = true
	}
	if text != "" {
		n := t.nid
		n.flags = nifInfo
		n.infoFlags = niifInfo
		copy(n.info[:len(n.info)-1], windows.StringToUTF16(text))
		copy(n.infoTitle[:len(n.infoTitle)-1], windows.StringToUTF16(title))
		procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
	}
	return nil
}

// Hide removes the icon.
func (t *Tray) Hide() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.shown {
		t.notify(nimDelete)
		t.shown = false
	}
}

// Close removes the icon and ends its window.
func (t *Tray) Close() {
	t.Hide()
	procPostMessageW.Call(uintptr(t.hwnd), wmClose, 0, 0)
}

// HideWindow hides the window hwnd, taking its button off the taskbar. It
// does not wait for the window's thread, which may be busy asking whether to
// close.
func HideWindow(hwnd uintptr) {
	procShowWindowAsync.Call(hwnd, swHide)
}

// ShowWindow shows the window hwnd again, restored if it was minimized,
// and brings it to the front.
func ShowWindow(hwnd uintptr) {
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		procShowWindowAsync.Call(hwnd, swRestore)
	} else {
		procShowWindowAsync.Call(hwnd, swShow)
	}
	procSetForegroundWindow.Call(hwnd)
}

// CloseWindow asks the window hwnd to close, as its close button does.
func CloseWindow(hwnd uintptr) {
	procPostMessageW.Call(hwnd, wmClose, 0, 0)
}
