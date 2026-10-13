// Package gui is the graphical interface of Linkcord, drawn with Gio so that
// it runs on Windows 7 and later.
package gui

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/ningen/v3"
	"github.com/ezchr/linkcord/internal/auth"
	"github.com/ezchr/linkcord/internal/logger"
	"github.com/ezchr/linkcord/internal/tray"
)

const appName = "Linkcord"

// tokenEnvVar overrides the saved token, which helps testing.
const tokenEnvVar = "LINKCORD_TOKEN"

// Run opens the main window and blocks until it is closed.
func Run() error {
	moveLegacyDir()
	if closer, err := logger.Load(logPath(), slog.LevelInfo); err == nil {
		defer closer.Close()
	}

	errc := make(chan error, 1)
	go func() {
		w := new(app.Window)
		w.Option(
			app.Title(appName),
			app.Size(unit.Dp(1100), unit.Dp(680)),
			app.MinSize(unit.Dp(720), unit.Dp(460)),
		)
		errc <- newUI(w).loop()
		os.Exit(0)
	}()
	app.Main()
	return <-errc
}

// logPath is where the log is written: %LocalAppData%\linkcord\logs.txt.
func logPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "linkcord", "logs.txt")
}

// moveLegacyDir renames the folder that versions named reDiscord used
// (%LocalAppData%\rediscord: logs and the image cache) to the current one.
func moveLegacyDir() {
	dir := filepath.Dir(logPath())
	old := filepath.Join(filepath.Dir(dir), "rediscord")
	if _, err := os.Stat(dir); err == nil {
		return
	}
	if _, err := os.Stat(old); err != nil {
		return
	}
	_ = os.Rename(old, dir)
}

type screen int

const (
	screenLogin screen = iota
	screenConnecting
	screenChat
	screenStartup
)

type ui struct {
	win        *app.Window
	th         *material.Theme
	frameCount uint64

	// pending holds work posted from other goroutines, run on the window's
	// goroutine before the next frame.
	mu      sync.Mutex
	pending []func()

	screen  screen
	login   *loginView
	chat    *chatView
	startup startupState

	// focused reports whether the window has the keyboard focus.
	focused   bool
	minimized bool
	// hwnd is the native window, once known.
	hwnd uintptr

	voice  voiceState
	images *images

	// tray is the icon in the notification area, made when the window is
	// first closed, and trayHinted reports whether its balloon was shown.
	tray       *tray.Tray
	trayHinted bool
	// quitting is set to close Linkcord rather than hide it.
	quitting atomic.Bool

	// The connecting screen.
	connectErr string
	retry      widget.Clickable
	cancel     widget.Clickable
	token      string
}

func newUI(w *app.Window) *ui {
	u := &ui{win: w, th: newTheme(), focused: true}
	u.images = newImages(u)
	if os.Getenv(demoEnvVar) == "1" {
		u.startDemo()
		return u
	}
	token := os.Getenv(tokenEnvVar)
	if token == "" {
		var err error
		if token, err = auth.LoadToken(); err != nil {
			slog.Error("failed to load token", "err", err)
		}
	}
	u.beginStartup(token)
	return u
}

// post runs f on the window's goroutine before the next frame. It is safe to
// call from any goroutine.
func (u *ui) post(f func()) {
	u.mu.Lock()
	u.pending = append(u.pending, f)
	u.mu.Unlock()
	u.win.Invalidate()
}

func (u *ui) runPending() {
	u.mu.Lock()
	pending := u.pending
	u.pending = nil
	u.mu.Unlock()
	for _, f := range pending {
		f()
	}
}

func (u *ui) loop() error {
	stopProfile := u.startPerformanceProfile()
	defer stopProfile()
	stopResumeWatchdog := u.startResumeWatchdog()
	defer stopResumeWatchdog()
	var ops op.Ops
	for {
		switch e := u.win.Event().(type) {
		case *app.ClosingEvent:
			// Closing the window leaves Linkcord running in the
			// notification area, as Discord does, unless quitting.
			if !u.quitting.Load() && u.hideToTray() {
				e.Abort()
			}
		case app.DestroyEvent:
			if u.tray != nil {
				u.tray.Close()
			}
			if u.startup.cancel != nil {
				u.startup.cancel()
			}
			if u.chat != nil {
				u.chat.close()
			}
			return e.Err
		case app.ViewEvent:
			u.viewEvent(e)
		case app.ConfigEvent:
			restored := u.minimized && e.Config.Mode != app.Minimized
			gainedFocus := !u.focused && e.Config.Focused
			u.minimized = e.Config.Mode == app.Minimized
			u.focused = e.Config.Focused
			if restored || gainedFocus {
				u.win.Invalidate()
				if u.chat != nil {
					u.chat.windowFocused()
				}
			}
		case app.FrameEvent:
			start := time.Now()
			u.runPending()
			pending := time.Since(start)
			gtx := app.NewContext(&ops, e)
			u.layout(gtx)
			laid := time.Since(start)
			e.Frame(gtx.Ops)
			u.slowFrame(start, pending, laid)
			u.frameCount++
			u.markHealthy()
		}
	}
}

func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	fillMax(gtx, colorChat)
	switch u.screen {
	case screenStartup:
		return u.layoutStartup(gtx)
	case screenLogin:
		return u.login.layout(gtx)
	case screenConnecting:
		return u.layoutConnecting(gtx)
	default:
		return u.chat.layout(gtx)
	}
}

func (u *ui) showLogin(errMsg string) {
	if u.chat != nil {
		u.chat.close()
		u.chat = nil
	}
	u.login = newLoginView(u)
	u.login.err = errMsg
	u.screen = screenLogin
	u.win.Option(app.Title(appName))
}

// loggedIn saves token and connects with it.
func (u *ui) loggedIn(token string) {
	if err := auth.SaveToken(token); err != nil {
		slog.Error("failed to save token", "err", err)
	}
	u.connect(token)
}

// connect opens a gateway session with token and shows the chat once it is
// ready.
func (u *ui) connect(token string) {
	if u.login != nil {
		u.login.stop()
		u.login = nil
	}
	u.token, u.connectErr = token, ""
	u.screen = screenConnecting

	st := newState(token)
	u.chat = newChatView(u, st)
	go func() {
		err := st.Open(context.Background())
		if err == nil {
			return
		}
		slog.Error("failed to open session", "err", err)
		u.post(func() {
			if u.chat == nil || u.chat.st != st {
				return
			}
			// A rejected token never works again.
			if event, ok := errors.AsType[*ws.CloseEvent](err); ok && event.Code == closeCodeAuthenticationFailed {
				u.logout("Your session expired or the token is invalid. Sign in again.")
				return
			}
			u.connectErr = "Could not connect to Discord: " + err.Error()
		})
	}()
}

// onReady switches to the chat once the gateway sent the ready event.
func (u *ui) onReady(st *ningen.State, ready *gateway.ReadyEvent) {
	if u.chat == nil || u.chat.st != st {
		return
	}
	u.chat.load(ready)
	u.screen = screenChat
}

// tokenRefreshed keeps the token that Discord gave the session in place of
// the one it logged in with.
func (u *ui) tokenRefreshed(st *ningen.State, token string) {
	if token == "" || u.chat == nil || u.chat.st != st {
		return
	}
	slog.Info("Discord refreshed the token")
	st.Client.Session.Token = token
	if os.Getenv(tokenEnvVar) == "" {
		if err := auth.SaveToken(token); err != nil {
			slog.Error("failed to save refreshed token", "err", err)
		}
	}
	u.token = token
}

// logout closes the session, forgets the token, and shows the login with
// errMsg, if any.
func (u *ui) logout(errMsg string) {
	if err := auth.DeleteToken(); err != nil {
		slog.Error("failed to delete token", "err", err)
	}
	u.showLogin(errMsg)
}

func (u *ui) layoutConnecting(gtx layout.Context) layout.Dimensions {
	if u.retry.Clicked(gtx) {
		if u.chat != nil {
			u.chat.close()
		}
		u.connect(u.token)
	}
	if u.cancel.Clicked(gtx) {
		u.logout("")
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.connectErr != "" {
					return layout.Dimensions{}
				}
				size := gtx.Dp(40)
				gtx.Constraints = layout.Exact(image.Pt(size, size))
				l := material.Loader(u.th)
				l.Color = colorAccent
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				txt := "Connecting to Discord…"
				c := colorText
				if u.connectErr != "" {
					txt, c = u.connectErr, colorRed
				}
				l := label(u.th, 16, c, txt)
				l.Alignment = text.Middle
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.connectErr == "" {
					return layout.Dimensions{}
				}
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, u.th, &u.retry, colorAccent, "Retry")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, u.th, &u.cancel, colorSelected, "Log out")
					}),
				)
			}),
		)
	})
}

// hideToTray hides the window and shows Linkcord's icon in the
// notification area, from which it opens again, as does starting it again.
// It reports false when there is no icon to come back from.
func (u *ui) hideToTray() bool {
	if u.hwnd == 0 {
		return false
	}
	if u.tray == nil {
		t, err := tray.Start(appName, "Open "+appName, "Quit "+appName, u.showFromTray, u.quit)
		if err != nil {
			slog.Warn("could not make the notification area icon", "err", err)
			return false
		}
		u.tray = t
	}
	hint := ""
	if !u.trayHinted {
		hint = appName + " is still running. Click its icon to open it, or right-click it to quit."
		u.trayHinted = true
	}
	if err := u.tray.Show(appName, hint); err != nil {
		slog.Warn("could not show the notification area icon", "err", err)
		return false
	}
	tray.HideWindow(u.hwnd)
	return true
}

// showFromTray shows the window again. It is called from the icon's thread.
func (u *ui) showFromTray() {
	tray.ShowWindow(u.hwnd)
	u.win.Invalidate()
}

// quit closes Linkcord, leaving the voice channel and the icon. It may be
// called from any goroutine.
func (u *ui) quit() {
	u.quitting.Store(true)
	tray.CloseWindow(u.hwnd)
}
