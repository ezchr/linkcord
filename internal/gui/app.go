// Package gui is the graphical interface of reDiscord, drawn with Gio so that
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

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/discordo/internal/auth"
	"github.com/ayn2op/discordo/internal/logger"
	"github.com/ayn2op/ningen/v3"
)

const appName = "reDiscord"

// tokenEnvVar overrides the saved token, which helps testing.
const tokenEnvVar = "REDISCORD_TOKEN"

// Run opens the main window and blocks until it is closed.
func Run() error {
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

// logPath is where the log is written: %LocalAppData%\rediscord\logs.txt.
func logPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "rediscord", "logs.txt")
}

type screen int

const (
	screenLogin screen = iota
	screenConnecting
	screenChat
)

type ui struct {
	win *app.Window
	th  *material.Theme

	// pending holds work posted from other goroutines, run on the window's
	// goroutine before the next frame.
	mu      sync.Mutex
	pending []func()

	screen screen
	login  *loginView
	chat   *chatView

	// focused reports whether the window has the keyboard focus.
	focused bool
	// hwnd is the native window, once known.
	hwnd uintptr

	voice  voiceState
	images *images

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
	if token != "" {
		u.connect(token)
	} else {
		u.showLogin("")
	}
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
	var ops op.Ops
	for {
		switch e := u.win.Event().(type) {
		case app.DestroyEvent:
			if u.chat != nil {
				u.chat.close()
			}
			return e.Err
		case app.ViewEvent:
			u.viewEvent(e)
		case app.ConfigEvent:
			if e.Config.Focused != u.focused {
				u.focused = e.Config.Focused
				if u.focused && u.chat != nil {
					u.chat.windowFocused()
				}
			}
		case app.FrameEvent:
			u.runPending()
			gtx := app.NewContext(&ops, e)
			u.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	fillMax(gtx, colorChat)
	switch u.screen {
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
				u.logout("La sesión expiró o el token no es válido. Inicia sesión de nuevo.")
				return
			}
			u.connectErr = "No se pudo conectar con Discord: " + err.Error()
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
				txt := "Conectando con Discord…"
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
						return button(gtx, u.th, &u.retry, colorAccent, "Reintentar")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return button(gtx, u.th, &u.cancel, colorSelected, "Cerrar sesión")
					}),
				)
			}),
		)
	})
}
