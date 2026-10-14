package gui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ezchr/linkcord/internal/hub"
	"github.com/ezchr/linkcord/internal/version"
)

// Linkcord marks those who use it with its logo, as its server says, and
// sends its errors there to be read; both can be turned off in the
// settings.

//go:embed linkcord-logo.png
var logoPNG []byte

var logoOp = sync.OnceValue(func() paint.ImageOp {
	img, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		return paint.ImageOp{}
	}
	return paint.NewImageOp(img)
})

// linkcordLogo is Linkcord's logo, size wide.
func linkcordLogo(gtx layout.Context, size unit.Dp) layout.Dimensions {
	s := gtx.Dp(size)
	gtx.Constraints = layout.Exact(image.Pt(s, s))
	return widget.Image{Src: logoOp(), Fit: widget.Contain}.Layout(gtx)
}

// linkcordBadge is Linkcord's logo beside the name of someone who uses it,
// nothing for others.
func (c *chatView) linkcordBadge(id discord.UserID, size unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !c.u.linkcordUsers[id] {
			return layout.Dimensions{}
		}
		return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return linkcordLogo(gtx, size)
		})
	}
}

// communitySettings are whether the user is shown as using Linkcord and
// sends errors, and how much of each error file was sent, kept in
// community.json beside the log.
type communitySettings struct {
	NoHello   bool             `json:"no_hello,omitempty"`
	NoReports bool             `json:"no_reports,omitempty"`
	Sent      map[string]int64 `json:"sent,omitempty"`
}

// communityMu keeps the settings, which the freeze watchdog reads too.
var communityMu sync.Mutex

func communityPath() string {
	return filepath.Join(filepath.Dir(logPath()), "community.json")
}

func loadCommunity() communitySettings {
	var s communitySettings
	if data, err := os.ReadFile(communityPath()); err == nil {
		json.Unmarshal(data, &s)
	}
	return s
}

// updateCommunity changes the settings with f and keeps them.
func updateCommunity(f func(*communitySettings)) communitySettings {
	communityMu.Lock()
	defer communityMu.Unlock()
	s := loadCommunity()
	f(&s)
	if data, err := json.Marshal(s); err == nil {
		os.MkdirAll(filepath.Dir(communityPath()), 0o700)
		os.WriteFile(communityPath(), data, 0o600)
	}
	return s
}

// startCommunity, once signed in, says the user uses Linkcord, learns who
// else does, and sends the errors not sent yet; and keeps doing so.
func (c *chatView) startCommunity() {
	u := c.u
	if c.demo {
		// In the demo, the user and Ana use Linkcord.
		u.linkcordUsers = map[discord.UserID]bool{c.me.ID: true}
		guilds, _ := c.st.Cabinet.Guilds()
		for _, g := range guilds {
			members, _ := c.st.Cabinet.Members(g.ID)
			for _, m := range members {
				if m.User.Username == "ana" {
					u.linkcordUsers[m.User.ID] = true
				}
			}
		}
		return
	}
	if u.communityStarted {
		return
	}
	u.communityStarted = true
	me := c.me.ID.String()
	go func() {
		for i := 0; ; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			s := loadCommunity()
			if !s.NoHello && i%36 == 0 {
				if err := hub.Hello(ctx, me, version.String()); err != nil {
					slog.Warn("could not reach Linkcord's server", "err", err)
				}
			}
			if ids, err := hub.Users(ctx); err == nil {
				set := make(map[discord.UserID]bool, len(ids))
				for _, id := range ids {
					if sf, err := discord.ParseSnowflake(id); err == nil {
						set[discord.UserID(sf)] = true
					}
				}
				u.post(func() { u.linkcordUsers = set })
			}
			if i == 0 {
				sendReports(ctx)
			}
			cancel()
			time.Sleep(10 * time.Minute)
		}
	}()
}

// sendReports sends what was written to crash.txt and freeze.txt since last
// sent, unless reports are off. The first time, what is there is old, and
// left.
func sendReports(ctx context.Context) {
	dir := filepath.Dir(logPath())
	for _, kind := range []string{"crash", "freeze"} {
		path := filepath.Join(dir, kind+".txt")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var from int64
		var off, first bool
		communityMu.Lock()
		s := loadCommunity()
		off = s.NoReports
		from, seen := s.Sent[kind]
		first = !seen
		communityMu.Unlock()
		size := int64(len(data))
		if from > size {
			from = 0 // the file was started again
		}
		if !off && !first {
			// A crash file has a line for every start; only what follows
			// one is a crash.
			for _, part := range strings.Split(string(data[from:]), "\n--- ") {
				lines := strings.Split(strings.TrimSpace(part), "\n")
				if len(lines) < 3 {
					continue
				}
				osName := runtime.GOOS + "/" + runtime.GOARCH + " " + windowsVersion()
				if err := hub.Report(ctx, version.String(), osName, kind, "--- "+strings.TrimSpace(part)); err != nil {
					slog.Warn("could not send an error report", "err", err)
					return
				}
				slog.Info("sent an error report", "kind", kind)
			}
		}
		updateCommunity(func(s *communitySettings) {
			if s.Sent == nil {
				s.Sent = make(map[string]int64)
			}
			s.Sent[kind] = size
		})
	}
}
