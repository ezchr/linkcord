// Command linkcord is a lightweight graphical Discord client that runs on
// Windows 7 and later.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"gioui.org/app"
	"github.com/ezchr/linkcord/internal/gui"
	discordhttp "github.com/ezchr/linkcord/internal/http"
	"github.com/ezchr/linkcord/internal/instance"
	"github.com/ezchr/linkcord/internal/update"
	"github.com/ezchr/linkcord/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print the embedded version and exit")
	demo := flag.Bool("demo", false, "show demo data without connecting or checking for updates")
	noUpdate := flag.Bool("no-update", false, "skip executable updates; still refresh Discord metadata")
	systemFonts := flag.Bool("system-fonts", false, "enable full system font fallback for additional scripts")
	lowPower := flag.Bool("low-power", false, "show static animation previews to reduce CPU and memory use")
	check := flag.Bool("check-updates", false, "check release and Discord metadata, print JSON, and exit without installing")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}
	if *check {
		if err := checkUpdates(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *systemFonts {
		os.Setenv("LINKCORD_SYSTEM_FONTS", "1")
	}
	if *lowPower {
		os.Setenv("LINKCORD_LOW_POWER", "1")
	}
	if *demo {
		os.Setenv("LINKCORD_DEMO", "1")
	}
	if *noUpdate {
		os.Setenv("LINKCORD_NO_UPDATE", "1")
	}
	// A fixed window class lets a second start find this window whatever
	// the executable is called.
	app.ID = instance.WindowClass
	// Only one Linkcord runs per session; starting it again brings the open
	// one to the front. The demo is left out, so that test tools can run it
	// beside a signed-in Linkcord.
	if os.Getenv("LINKCORD_DEMO") != "1" && !instance.Acquire() {
		return
	}
	if err := gui.Run(); err != nil {
		log.SetOutput(os.Stderr)
		log.Fatal(err)
	}
}

func checkUpdates() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager := update.NewManager(version.String(), "")
	cache := filepath.Join(filepath.Dir(update.DefaultCacheDir()), "discord-build.json")
	discordhttp.InitializeClientBuild(cache)
	build, buildErr := discordhttp.RefreshClientBuild(ctx, cache)
	release, releaseErr := manager.Check(ctx)
	report := map[string]any{
		"current_version": version.String(), "discord_build": build,
		"update_available": false,
	}
	if buildErr != nil {
		report["discord_error"] = buildErr.Error()
	}
	if releaseErr != nil {
		report["release_error"] = releaseErr.Error()
	} else {
		report["latest_version"] = release.Tag
		newer, err := update.Newer(release.Tag, version.String())
		if err != nil {
			report["release_error"] = err.Error()
		} else {
			report["update_available"] = newer
		}
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
