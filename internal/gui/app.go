// Package gui is the graphical interface of Linkcord, drawn with Gio so that
// it runs on Windows 7 and later.
package gui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
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
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/ningen/v3"
	"github.com/ezchr/linkcord/internal/auth"
	lchttp "github.com/ezchr/linkcord/internal/http"
	"github.com/ezchr/linkcord/internal/logger"
	"github.com/ezchr/linkcord/internal/tray"
	"github.com/ezchr/linkcord/internal/version"
)

const appName = "Linkcord"

// tokenEnvVar overrides the saved token, which helps testing.
const tokenEnvVar = "LINKCORD_TOKEN"

// Run opens the main window and blocks until it is closed.
func Run() error {
	moveLegacyDir()
	tuneMemory()
	// The demo, which tests run beside a signed-in Linkcord, keeps its own
	// log rather than start that one's over.
	log := logPath()
	if os.Getenv("LINKCORD_DEMO") == "1" {
		log = filepath.Join(filepath.Dir(log), "demo-logs.txt")
	}
	if closer, err := logger.Load(log, slog.LevelInfo); err == nil {
		defer closer.Close()
	}
	// A crash, of Go or of C code, is written beside the log, which a
	// window without a console would otherwise lose.
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(logPath()), "crash.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "\n--- %s started %s\n", version.String(), time.Now().Format(time.RFC3339))
		if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
			slog.Warn("could not keep crashes", "err", err)
		}
		f.Close()
	}

	errc := make(chan error, 1)
	go func() {
		w := new(app.Window)
		w.Option(
			app.Title(appName),
			app.Size(unit.Dp(1100), unit.Dp(680)),
			app.MinSize(unit.Dp(720), unit.Dp(460)),
		)
		u := newUI(w)
		// A panic on the window's goroutine is sent as app_crashed, as the
		// web client's crash screen sends it, then crashes as before, so
		// crash.txt still has it.
		defer func() {
			if r := recover(); r != nil {
				if c := u.chat; c != nil && c.sci != nil {
					c.sci.crashed(strings.TrimPrefix(currentPage(), "https://discord.com"), fmt.Sprint(r), string(debug.Stack()))
					waitScienceSends(2 * time.Second)
				}
				panic(r)
			}
		}()
		// Files dropped on the window are attached, as Discord does.
		app.FilesDropped = func(paths []string) {
			u.post(func() {
				if c := u.chat; c != nil {
					c.attachPaths(paths)
				}
			})
		}
		errc <- u.loop()
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
	// soonPending is set while a frame is due for what postSoon posted.
	soonPending atomic.Bool
	// pendingSince is when the oldest work pending was posted, zero while
	// none is, for the freeze watchdog.
	pendingSince time.Time

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
	// frameMu is held while a window handles an event: the call popped out
	// draws from the same state as the main window.
	frameMu sync.Mutex
	popout  popoutState
	// watchingAny reports whether a stream is watched, for the pop-out's
	// redraws.
	watchingAny atomic.Bool
	// appearance are the colors the user chose, and bg the background
	// picture made for the window.
	appearance appearanceSettings
	bg         backgroundPicture
	// app is how Linkcord looks and behaves otherwise.
	app appSettings
	// rpcStarted reports whether Discord's RPC is served, and rpcMe is who
	// it says the user is.
	rpcStarted bool
	rpcMe      atomic.Pointer[discord.User]
	// linkcordUsers are who use Linkcord, as its server says, marked with
	// its logo; communityStarted reports whether it is asked.
	linkcordUsers    map[discord.UserID]bool
	communityStarted bool

	// tray is the icon in the notification area, made when the window is
	// first closed, and trayHinted reports whether its balloon was shown.
	tray       *tray.Tray
	trayHinted bool
	// quitting is set to close Linkcord rather than hide it.
	quitting atomic.Bool

	// The connecting screen.
	connectErr string
	// connectTries is how many times in a row connecting failed, for the
	// wait before trying again.
	connectTries int
	retry        widget.Clickable
	cancel       widget.Clickable
	token        string
}

func newUI(w *app.Window) *ui {
	// The colors chosen are set before anything takes its own copy of them.
	appearance := loadAppearance()
	applyAppearance(appearance)
	_, statErr := os.Stat(appSettingsPath())
	firstRun := errors.Is(statErr, os.ErrNotExist)
	settings := loadAppSettings()

	// The first time, Linkcord speaks the language Windows is in; the
	// choice is kept, and can be changed in the settings. Who used it
	// before keeps English.
	if settings.Language == "" && firstRun && os.Getenv(demoEnvVar) != "1" {
		settings.Language = systemLanguage()
		saveAppSettingsFile(settings)
	}
	uiLang = settings.Language
	u := &ui{win: w, th: newThemeFont(settings.Font), focused: true, appearance: appearance, app: settings}
	u.images = newImages(u)
	// Text that needs the fonts for symbols or Asian scripts has them
	// read, and the interface shaped again with them.
	var reshaping atomic.Bool
	onFontNeeds = func() {
		if !reshaping.CompareAndSwap(false, true) {
			return
		}
		go func() {
			// Fonts needed together, as names in their own fonts appearing
			// with a list, are loaded with one rebuild, which reads every
			// font again.
			time.Sleep(300 * time.Millisecond)
			sh := newShaper(u.app.Font)
			u.post(func() {
				u.th.Shaper = sh
				reshaping.Store(false)
				// The fonts of the old shaper are many megabytes, given
				// back to Windows at once rather than kept by Go.
				go func() {
					time.Sleep(2 * time.Second)
					debug.FreeOSMemory()
				}()
				u.win.Invalidate()
				// More may have been needed meanwhile.
				if fontNeeds.Load() != fontsLoaded.Load() {
					onFontNeeds()
				}
			})
		}()
	}
	u.voice.muted, u.voice.deafened = settings.VoiceMuted, settings.VoiceDeafened
	u.voice.mutedBefore = settings.VoiceMuted && !settings.VoiceDeafened
	go u.watchNetwork()
	// How the download of FFmpeg goes shows as the voice's messages do.
	mediaNotify = func(s string) { u.post(func() { u.voiceInfo(s) }) }
	if os.Getenv(demoEnvVar) == "1" {
		u.startDemo()
		return u
	}
	token := os.Getenv(tokenEnvVar)
	if token == "" {
		token = loadSavedToken()
	}
	u.beginStartup(token)
	return u
}

// postSoon runs f on the window's goroutine before a frame that comes
// within half a second, rather than one at once: for what changes nothing
// in view, such as messages of other channels, many of which come each
// second in busy servers and each of which would redraw the window.
func (u *ui) postSoon(f func()) {
	u.mu.Lock()
	if len(u.pending) == 0 {
		u.pendingSince = time.Now()
	}
	u.pending = append(u.pending, f)
	u.mu.Unlock()
	if u.soonPending.CompareAndSwap(false, true) {
		time.AfterFunc(500*time.Millisecond, func() {
			u.soonPending.Store(false)
			u.win.Invalidate()
		})
	}
}

// post runs f on the window's goroutine before the next frame. It is safe to
// call from any goroutine.
func (u *ui) post(f func()) {
	u.mu.Lock()
	if len(u.pending) == 0 {
		u.pendingSince = time.Now()
	}
	u.pending = append(u.pending, f)
	u.mu.Unlock()
	u.win.Invalidate()
}

func (u *ui) runPending() {
	u.mu.Lock()
	pending := u.pending
	u.pending, u.pendingSince = nil, time.Time{}
	u.mu.Unlock()
	for _, f := range pending {
		f()
	}
}

func (u *ui) loop() error {
	stopProfile := u.startPerformanceProfile()
	go watchCPU()
	defer stopProfile()
	stopResumeWatchdog := u.startResumeWatchdog()
	defer stopResumeWatchdog()
	stopFreezeWatchdog := u.startFreezeWatchdog()
	defer stopFreezeWatchdog()
	u.applyKeybinds()
	u.soundLibrary()
	var ops op.Ops
	for {
		ev := u.win.Event()
		u.frameMu.Lock()
		switch e := ev.(type) {
		case *app.ClosingEvent:
			// Closing the window leaves Linkcord running in the
			// notification area, as Discord does, unless quitting.
			slog.Info("window closing", "quitting", u.quitting.Load())
			if !u.quitting.Load() && !u.app.QuitOnClose && u.hideToTray() {
				e.Abort()
			}
		case app.DestroyEvent:
			slog.Info("window closed", "err", e.Err)
			if u.tray != nil {
				u.tray.Close()
			}
			if u.voice.keys != nil {
				u.voice.keys.Close()
			}
			// Tell Discord the stream ended, rather than leave it hanging.
			if s := u.voice.stream; s != nil {
				u.voice.stream = nil
				s.Stop()
			}
			if u.startup.cancel != nil {
				u.startup.cancel()
			}
			if u.chat != nil {
				u.chat.close()
			}
			// What analytics were still queued are sent before quitting,
			// as the web client sends them on page hide.
			waitScienceSends(2 * time.Second)
			// Closed as it should: no voice channel was lost.
			forgetVoiceChannel()
			u.frameMu.Unlock()
			return e.Err
		case app.ViewEvent:
			u.viewEvent(e)
		case app.ConfigEvent:
			restored := u.minimized && e.Config.Mode != app.Minimized
			gainedFocus := !u.focused && e.Config.Focused
			u.minimized = e.Config.Mode == app.Minimized
			u.focused = e.Config.Focused
			// Focus is use of the heartbeat session, as on the web; a new
			// one after it lapsed is told to the gateway.
			if gainedFocus && lchttp.TouchHeartbeatSession() && u.chat != nil && !u.chat.closed {
				u.chat.sendTimeSpentSession()
			}
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
			if u.images != nil {
				u.images.newFrame()
			}
			u.applyZoom(&gtx)
			u.layout(gtx)
			laid := time.Since(start)
			e.Frame(gtx.Ops)
			u.slowFrame(start, pending, laid)
			u.frameCount++
			u.markHealthy()
		default:
			// A window hidden in the notification area draws no frames, but
			// what is posted, as by a keybind, is still woken up for.
			u.runPending()
		}
		u.frameMu.Unlock()
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
	// A new token starts a new heartbeat session, as on the web.
	lchttp.ResetHeartbeatSession()
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
	hookScienceHTTP(st.Client.Client)
	u.chat = newChatView(u, st)
	go func() {
		err := st.Open(context.Background())
		if err == nil {
			// Once connected, the gateway can still stop for good, as when
			// the token is revoked by a password change or a log out
			// elsewhere. Discord's client then signs out; Linkcord did not
			// notice and stayed on a chat that no longer updates.
			u.watchSession(st)
			return
		}
		slog.Error("failed to open session", "err", err)
		u.post(func() {
			if u.chat == nil || u.chat.st != st {
				return
			}
			// A rejected token never works again.
			if event, ok := errors.AsType[*ws.CloseEvent](err); ok && event.Code == closeCodeAuthenticationFailed {
				slog.Warn("Discord rejected the saved token; signing out")
				u.logout("Your session expired or the token is invalid. Sign in again.")
				return
			}
			// Other failures, as no network yet right after Windows starts,
			// are tried again by themselves, sooner first.
			u.connectTries++
			delay := min(3*time.Second<<min(u.connectTries-1, 4), 30*time.Second)
			u.connectErr = fmt.Sprintf("Could not connect to Discord: %v\nTrying again in %d seconds…", err, int(delay.Seconds()))
			slog.Info("connecting again later", "in", delay, "tries", u.connectTries)
			time.AfterFunc(delay, func() {
				u.post(func() {
					if u.screen == screenConnecting && u.connectErr != "" && u.chat != nil && u.chat.st == st {
						u.chat.close()
						u.connect(u.token)
					}
				})
			})
		})
	}()
}

// watchSession waits for st's gateway to stop for good after it connected:
// a refused token signs out, as at start; anything else connects again.
// Closing the session here, to log out or to connect again, is not acted on.
func (u *ui) watchSession(st *ningen.State) {
	err := st.Wait(context.Background())
	u.post(func() {
		if u.chat == nil || u.chat.st != st || u.chat.closed {
			return
		}
		slog.Warn("the gateway stopped", "err", err)
		if event, ok := errors.AsType[*ws.CloseEvent](err); ok && event.Code == closeCodeAuthenticationFailed {
			u.logout("Your session expired or the token is invalid. Sign in again.")
			return
		}
		u.chat.close()
		u.connect(u.token)
	})
}

// onReady switches to the chat once the gateway sent the ready event.
func (u *ui) onReady(st *ningen.State, ready *gateway.ReadyEvent) {
	if u.chat == nil || u.chat.st != st {
		return
	}
	u.connectTries = 0
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
	lchttp.ResetHeartbeatSession()
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
	lchttp.ResetHeartbeatSession()
	// Discord is told the session ends, as its client does on log out, so
	// the token stops working there too. It is not waited for: a token that
	// was already refused, or no connection, still logs out here.
	if u.chat != nil && u.chat.st != nil && !u.chat.demo {
		cl := u.chat.st.Client
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := cl.WithContext(ctx).FastRequest("POST", api.Endpoint+"auth/logout",
				httputil.WithJSONBody(map[string]any{"provider": nil, "voip_provider": nil}))
			if err != nil {
				slog.Info("Discord was not told of the log out", "err", err)
			}
		}()
	}
	if err := auth.DeleteToken(); err != nil {
		slog.Error("failed to delete token", "err", err)
	}
	u.showLogin(errMsg)
	// What this account left here goes too, as Discord's client clears its
	// local data on log out: the next account must not see it.
	go func() {
		for _, p := range []string{deletedLogPath(), strings.TrimSuffix(deletedLogPath(), ".json") + ".jsonl", inboxPath(), lastPlacePath(), lostVoiceFile()} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("could not clear local data", "path", p, "err", err)
			}
		}
	}()
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
	hint := ""
	if !u.trayHinted {
		hint = appName + " is still running. Click its icon to open it, or right-click it to quit."
		u.trayHinted = true
	}
	if !u.showTrayIcon(hint) {
		return false
	}
	tray.HideWindow(u.hwnd)
	return true
}

// showTrayIcon shows Linkcord's icon in the notification area, with hint
// in a balloon if it is not empty, and reports whether it is there.
func (u *ui) showTrayIcon(hint string) bool {
	if u.tray == nil {
		t, err := tray.Start(appName, "Open "+appName, "Quit "+appName, u.showFromTray, u.quit)
		if err != nil {
			slog.Warn("could not make the notification area icon", "err", err)
			return false
		}
		u.tray = t
	}
	if err := u.tray.Show(appName, hint); err != nil {
		slog.Warn("could not show the notification area icon", "err", err)
		return false
	}
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

// tuneMemory bounds Go's heap. By default it lets the heap grow to twice what
// is in use before collecting. A desktop program that sits open all day is
// better off collecting a little sooner: at 75% over the live heap, with a
// soft limit of 320 MB in all that makes collection harder past it.
//
// Measured with a synthetic heap and heavy allocation (60 to 150 MB live):
// GOGC 50 spent about twice the GC CPU of GOGC 100 to save 35 to 70 MB;
// GOGC 75 sits between, at some 35% less GC CPU than 50 for 15 to 30 MB
// more. A 256 MB limit with 150 MB live had the collector run nearly all the
// time (3.6 times the GC CPU) to save 20 MB, so the limit is a ceiling for
// spikes, not what keeps the heap small; letting go of the state of servers
// and channels not shown does that (memory_prune.go). A GOGC or GOMEMLIMIT
// set in the environment is left as it is.
func tuneMemory() {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(75)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(320 << 20)
	}
}
