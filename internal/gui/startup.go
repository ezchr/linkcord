package gui

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	discordhttp "github.com/ezchr/linkcord/internal/http"
	"github.com/ezchr/linkcord/internal/update"
	"github.com/ezchr/linkcord/internal/version"
)

type startupState struct {
	token    string
	status   string
	cancel   context.CancelFunc
	done     bool
	applying bool
	healthy  bool
	skip     widget.Clickable
}

func (u *ui) beginStartup(token string) {
	cache := filepath.Join(filepath.Dir(logPath()), "discord-build.json")
	info := discordhttp.InitializeClientBuild(cache)
	slog.Info("loaded Discord client metadata", "build", info.Number, "source", info.Source)
	ctx, cancel := context.WithCancel(context.Background())
	u.startup = startupState{token: token, status: "Checking for updates…", cancel: cancel}
	u.screen = screenStartup
	go u.checkStartup(ctx, cache)
}

func (u *ui) startupMessage(message string) {
	u.post(func() {
		if !u.startup.done {
			u.startup.status = message
		}
	})
}

func (u *ui) finishStartup() {
	if u.startup.done {
		return
	}
	u.startup.done = true
	u.startup.applying = false
	if u.startup.cancel != nil {
		u.startup.cancel()
	}
	if u.startup.token != "" {
		u.connect(u.startup.token)
	} else {
		u.showLogin("")
	}
}

// lockUpdate resolves a Skip click on the UI goroutine before a helper can
// be launched. Once replacement is ready, the window closes for restart.
func (u *ui) lockUpdate(ctx context.Context) bool {
	result := make(chan bool, 1)
	u.post(func() {
		if u.startup.done || ctx.Err() != nil {
			result <- false
			return
		}
		u.startup.applying = true
		u.startup.status = "Installing update and restarting…"
		result <- true
	})
	select {
	case ok := <-result:
		return ok
	case <-ctx.Done():
		return false
	}
}

func (u *ui) checkStartup(ctx context.Context, buildCache string) {
	type releaseResult struct {
		release *update.Release
		err     error
	}
	buildDone := make(chan struct{}, 1)
	releaseDone := make(chan releaseResult, 1)
	manager := update.NewManager(version.String(), "")
	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		info, err := discordhttp.RefreshClientBuild(checkCtx, buildCache)
		if err != nil {
			slog.Warn("Discord metadata refresh unavailable", "build", info.Number, "err", err)
		} else {
			slog.Info("refreshed Discord client metadata", "build", info.Number)
		}
		buildDone <- struct{}{}
	}()
	go func() {
		if os.Getenv("LINKCORD_NO_UPDATE") == "1" {
			releaseDone <- releaseResult{}
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		release, err := manager.Check(checkCtx)
		releaseDone <- releaseResult{release, err}
	}()
	var result releaseResult
	select {
	case result = <-releaseDone:
	case <-ctx.Done():
		return
	}
	select {
	case <-buildDone:
	case <-ctx.Done():
		return
	}
	if result.err != nil {
		slog.Warn("GitHub update check unavailable; continuing with installed client", "err", result.err)
	} else if result.release != nil {
		newer, err := update.Newer(result.release.Tag, version.String())
		if err != nil {
			slog.Warn("could not compare release versions", "err", err)
		} else if newer && runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
			target, err := os.Executable()
			if err == nil {
				u.startupMessage("Downloading Linkcord " + result.release.Tag + "…")
				downloadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				plan, prepareErr := manager.Prepare(downloadCtx, result.release, target, func(percent int) {
					u.startupMessage(fmt.Sprintf("Downloading Linkcord %s… %d%%", result.release.Tag, percent))
				})
				cancel()
				err = prepareErr
				if err == nil && plan != nil {
					if !u.lockUpdate(ctx) {
						_ = os.RemoveAll(plan.Dir)
						return
					}
					err = plan.Handoff(ctx)
					if err == nil {
						u.post(func() {
							u.startup.done = true
							u.win.Perform(system.ActionClose)
						})
						return
					}
				}
			}
			slog.Warn("automatic update unavailable; keeping installed client", "err", err)
		}
	}
	if ctx.Err() == nil {
		u.post(u.finishStartup)
	}
}

func (u *ui) markHealthy() {
	if u.startup.healthy {
		return
	}
	u.startup.healthy = true
	if err := update.MarkHealthy(version.String()); err != nil {
		slog.Error("could not acknowledge updated executable", "err", err)
	}
}

func (u *ui) layoutStartup(gtx layout.Context) layout.Dimensions {
	if !u.startup.applying && u.startup.skip.Clicked(gtx) {
		u.finishStartup()
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(bold(label(u.th, 26, colorHeader, appName)).Layout),
			layout.Rigid(label(u.th, 12, colorMuted, version.String()).Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(24)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				d := gtx.Dp(36)
				gtx.Constraints = layout.Exact(image.Pt(d, d))
				return loader(u.th).Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(label(u.th, 15, colorText, u.startup.status).Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.startup.applying {
					return layout.Dimensions{}
				}
				return button(gtx, u.th, &u.startup.skip, colorSelected, "Skip checks")
			}),
		)
	})
}
