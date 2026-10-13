# Linkcord

A lightweight graphical Discord client for **Windows 7, 8, 10, and 11**, distributed as a single `.exe`. Linkcord uses [discordo](https://github.com/ayn2op/discordo), a Go terminal client, for its connection, authentication, and voice foundations. Its graphical interface is built with [Gio](https://gioui.org).

Linkcord was created for people on Windows 7 who can no longer run the official desktop application.

> **Unofficial client:** using an unofficial client carries a risk of account suspension under Discord's terms of service.

## Features

| Available | Remaining work |
|---|---|
| QR-code, email/password with 2FA, and token sign-in | Captcha rendering |
| Windows Credential Manager session storage and refreshed-token persistence | |
| Servers in the user's order, direct messages, collapsible categories, and channel permissions | Unread counters and notification previews |
| Discord formatting, mentions, quotes, code, spoilers, links, and BOT badges | Threads and forums |
| Server emoji as images, large emoji-only messages, and animated GIFs | |
| PNG, animated APNG, and GIF stickers; animated Tenor embeds | Lottie sticker rendering |
| Image viewer with zoom, pan, and animation | |
| Video thumbnails and a play button that opens the video in the browser | In-client video playback |
| Cached avatars and server icons, presence, member lists, and profile cards | Friends, requests, and settings |
| Attachments, emoji picker, mention autocomplete, and typing indicators | |
| Voice join/leave, mute/deafen, connection status, and speaking indicators | Verification in a real voice call |
| React, reply, edit, copy, delete, and inline reaction controls | |
| English interface and project documentation | Testing on a physical Windows 7 machine |

Shift-click Delete skips confirmation. Up Arrow in an empty composer edits your most recent message.

## Build

Windows and two user-installed tools are required; administrator permissions are unnecessary.

1. **Go with Windows 7 support:** [go-legacy-win7 v1.27.2-1](https://github.com/thongtech/go-legacy-win7/releases/tag/v1.27.2-1), asset `go-legacy-win7-1.27.2-1.windows_amd64.zip`. Extract it to `%LOCALAPPDATA%\Programs\go-legacy-win7`. Standard Go builds do not retain Windows 7 runtime support.
2. **C compiler for voice audio:** [WinLibs GCC 16.2.0 with MSVCRT](https://github.com/brechtsanders/winlibs_mingw/releases/tag/16.2.0posix-14.0.0-msvcrt-r2), asset `winlibs-x86_64-posix-seh-gcc-16.2.0-mingw-w64msvcrt-14.0.0-r2.zip`. Extract its `mingw64` folder to `%LOCALAPPDATA%\Programs\mingw64`. Use **MSVCRT**, which is available on Windows 7, rather than UCRT.

From the repository:

```powershell
.\build-linkcord.ps1            # writes ..\linkcord.exe
.\build-linkcord.ps1 -Console   # retains a console for logs and panics
```

Tests and static checks:

```powershell
$env:PATH = "$env:LOCALAPPDATA\Programs\go-legacy-win7\bin;$env:LOCALAPPDATA\Programs\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
$env:CC = "gcc"
go test ./internal/gui ./internal/auth ./internal/http
go test ./internal/update
go vet ./internal/gui ./internal/auth ./internal/http ./internal/update ./cmd/linkcord
```

The updater has an optional Windows integration test that replaces and restores temporary executables, including paths containing spaces and apostrophes:

```powershell
$env:LINKCORD_TEST_HANDOFF = "1"
go test ./internal/update -run '^TestWindowsHandoff$' -count=1 -v
Remove-Item Env:\LINKCORD_TEST_HANDOFF
```

## Automatic updates

Normal startup opens a short update screen before sign-in. Discord metadata and GitHub release checks run in parallel. Offline failures leave the installed client usable; **Skip checks** continues with the cached or built-in Discord metadata.

Discord's current web-client build number is fetched from its login page, cached in `%LOCALAPPDATA%\linkcord\discord-build.json`, and used in HTTP headers and gateway identification. This updates metadata without a new executable. API, gateway, and authentication behavior still require code changes.

For newer stable GitHub releases, Linkcord downloads the `linkcord.exe` asset, verifies its size and GitHub SHA-256 digest, and checks that it is a Windows AMD64 executable. A hidden PowerShell helper waits for the current process to exit, replaces the file, and starts the new client. The new client must acknowledge its version after its first GUI frame; otherwise the helper restores the previous executable. A failed startup is recorded so the same release and digest do not cause a restart loop.

Replacement requires write access to the executable's folder. A release without a digest or with an invalid asset leaves the installed version in place. Update staging folders contain a backup and diagnostic logs beside the executable.

The repository is currently private. Release checks use `LINKCORD_GITHUB_TOKEN`, if set, or the current GitHub CLI sign-in when `gh` is installed. The GitHub account must have access to `ezchr/linkcord`. Public releases require no GitHub credential. GitHub and Discord credentials are separate; neither is embedded in the executable, release cache, or helper script.

| Option | Behavior |
|---|---|
| `--version` | Prints the embedded application version and exits. |
| `--check-updates` | Checks GitHub and Discord metadata, prints a JSON report, and exits without installing. |
| `--no-update` or `LINKCORD_NO_UPDATE=1` | Skips executable update checks but still refreshes Discord metadata. |
| `--demo` or `LINKCORD_DEMO=1` | Shows account-free demo data and performs no startup update requests. |
| `--low-power` or `LINKCORD_LOW_POWER=1` | Shows static GIF/APNG previews to reduce animation CPU and memory use. |
| `--system-fonts` | Compatibility option; full system font fallback is already enabled. |

Development builds identify themselves as `v1.4.1-dev`; they cannot downgrade to `v1.4.0`. For a release, build the graphical entry point with its exact GitHub tag:

```powershell
.\build-linkcord.ps1 -Version v1.4.1 -Out ..\linkcord.exe
```

Publish that executable as `linkcord.exe` on the matching stable release. The helper checks the restarted application's embedded version against the release tag. A fresh build uploaded under an incorrect tag will not acknowledge startup.

If a fixed asset is uploaded under the same tag with a new digest, it can be tried again. The failure record is `%LOCALAPPDATA%\linkcord\updates\failed-version`; release metadata is cached in the same folder.

## Resource usage

Normal mode retains full system font fallback, bundled Go and Noto Color Emoji fonts, and animated GIF/APNG previews. Visible animations retain their original frame scheduling even when the window is unfocused; minimized windows do not request animation redraws.

Decoded images use a 64 MiB cache budget with eviction of older completed entries. Up to two image loads decode at once, with at most 64 pending loads. Each animation retains at most 48 MiB of frames, with a maximum decoded side of 2048 pixels. Animations that exceed the retained-frame budget merge frame delays, preserving duration with fewer retained frames. The currently requested entry is protected from eviction.

Open your own user profile and click **Performance Mode** to toggle still GIF/APNG previews. The choice is saved locally and applies immediately. On uses the first animation frame without retaining subsequent frames or scheduling animation redraws. Off restores animation loading. Both modes retain full font fallback. `--low-power` or `LINKCORD_LOW_POWER=1` overrides the saved startup preference; `LINKCORD_LOW_POWER=0` starts in normal mode.

Video attachments and direct video embeds open inside Linkcord with pause, mute, replay, and close controls. Closing the viewer or logging out cancels playback. Playback requires `ffmpeg` and `ffprobe` on PATH, beside `linkcord.exe`, or in its `media` subfolder. Videos download to a temporary file before playback; closing the viewer removes the file after decoder shutdown. The current limits are 256 MiB per download, a 4K pixel count, and 120 fps. Provider watch pages without direct media URLs and DRM streams are unsupported. Variable-frame-rate input is normalized to its reported average frame rate. Builds with `no_voice` play video silently.

These are application cache limits, not a limit on total process memory. Fonts, message state, the Go runtime, and graphics-driver allocations also consume memory.

For a controlled diagnostic session, set `LINKCORD_PROFILE` to an output folder. On a normal window close it writes `cpu.pprof`, `heap.pprof`, and `summary.json`, including rendered frame count and heap statistics. Ordinary launches do not enable profiling.

## Test without an account

| Environment variable | Purpose |
|---|---|
| `LINKCORD_DEMO=1` | Displays made-up servers, users, and messages without connecting to Discord. Fixtures are in `internal/gui/demo.go`. |
| `GIO_LEGACY_MOUSE=1` | Uses Windows 7 mouse messages to test that path on Windows 10/11. |
| `GIO_D3D11_WARP=1` | Uses software rendering instead of a graphics card. |
| `LINKCORD_TOKEN=...` | Overrides the locally saved Discord token. |
| `LINKCORD_RENDER=...` | Output folder for screenless rendering tests. |
| `LINKCORD_LIVE_TENOR=...` | A Tenor media URL for the optional live animation smoke test. |
| `LINKCORD_LIVE_APNG=...` | An APNG URL for the optional live animation smoke test. |

`scripts/test-window.ps1` sends clicks, wheel events, and keys directly to the application's window without moving the real mouse, then captures its content even when another window covers it. See the script's header for usage.

```powershell
.\scripts\test-window.ps1 -Demo -Script { Start-Sleep 4; Snap "start"; MouseClick 500 632; TypeText "@an"; Snap "mentions" }
```

For screenless media renders, set `LINKCORD_RENDER` and run `go test ./internal/gui -run '^TestRenderMedia$' -count=1`.

Local application data:

- Log: `%LOCALAPPDATA%\linkcord\logs.txt`.
- Image cache: `%LOCALAPPDATA%\linkcord\images`.
- Discord token: Windows Credential Manager, under the name `linkcord`.

## Project layout

| Path | Responsibility |
|---|---|
| `cmd/linkcord/main.go` | Graphical application entry point. |
| `internal/auth/` | QR-code and password/2FA sign-in, session cookies and fingerprints, credential storage. |
| `internal/gui/app.go` | Window lifecycle, event loop, screens, and work posted to the UI goroutine. |
| `internal/gui/session.go` | Arikawa/ningen state setup and `AUTH_TOKEN_REFRESH` handling. |
| `internal/gui/chat.go` | Servers, channels, DMs, user panel, and gateway events. |
| `internal/gui/messages.go` | History, message bodies, attachments, embeds, and channel header. |
| `internal/gui/markdown.go` | Discord formatting, links, mentions, and spoilers. |
| `internal/gui/composer.go` | Message sending, attachments, mention autocomplete, and typing indicators. |
| `internal/gui/emoji.go` | Emoji picker and English search keywords. |
| `internal/gui/actions.go` | Message actions and reactions. |
| `internal/gui/media.go` | Sticker, Tenor, and video preview routing. |
| `internal/gui/apng.go` | APNG frame decoding and compositing. |
| `internal/gui/images.go` | Background downloads, scaling, animation timing, and image caching. |
| `internal/gui/viewer.go` | Image viewing, zoom, and pan. |
| `internal/gui/members.go` | Discord's lazy member list through ningen. |
| `internal/gui/profile.go`, `presence.go` | Profile cards, status menus, activities, and presence indicators. |
| `internal/gui/voice.go` | Voice controls and speaking indicators, backed by `internal/voice`. |
| `internal/gui/theme.go`, `widgets.go` | Colors, fonts, and shared interface components. |
| `internal/gui/demo.go` | Account-free demo fixtures. |
| `internal/gui/render_test.go`, `media_render_test.go` | Screenless interface rendering tests. |
| `internal/http/` | Discord request headers, client properties, and cached web-build refresh. |
| `internal/update/` | GitHub release checks, verified downloads, Windows replacement, and rollback. |
| `internal/version/` | Embedded version shared by both entry points. |
| `third_party/patches/` | Patches to the vendored Gio libraries. |

The UI state belongs to the window goroutine. Background work must enter through `u.post(func() { ... })`; `u.win.Invalidate()` requests a new frame.

When merging upstream discordo changes, replace `github.com/ayn2op/discordo` module imports with `github.com/ezchr/linkcord`. Linkcord's graphical interface is separate from the original terminal interface.

## Vendored library patches

`go.mod` uses local copies of Gio and its extensions. Patch files are retained in `third_party/patches/` so the changes can be reapplied to newer versions.

- **Gio v0.10.3:** checks for Windows APIs unavailable on Windows 7 and falls back to classic mouse messages. Direct3D hardware failures fall back to WARP.
- **gioui.org/x v0.10.3:** draws image glyphs in rich text so color emoji remain visible; also supports the project's rich-text span callback.

## Fonts and color emoji

Noto Color Emoji is embedded in the executable and supplies PNG-based CBDT glyphs. Segoe UI Emoji's COLR layers are unsupported by the current renderer.

`narrowSpace` in `theme.go` reduces the emoji font's unusually wide space; `theme_test.go` covers it. Segoe UI Symbol and Cambria Math provide fallback symbols and decorative letters.

Do not set a fixed `th.Face`: bold text must retain fallback-font lookup.

## Interface implementation notes

- Use `vcenter` rather than `layout.W` to center labels vertically.
- Expanded `layout.Stack` children must return `gtx.Constraints.Min` instead of zero dimensions.
- Popups must register input over their entire area; see `solid()` in `profile.go`.
- Read composer key events with a focused `key.Filter` before calling `composer.Update`.
- PowerShell 5's default text encoding can damage Unicode. Scripts should use UTF-8 without a BOM through `System.IO.File`.

## Discord integration notes

Captcha challenges cannot currently be displayed in the client. QR-code and token sign-in are available alternatives.

`ReadyEvent.UserSettings` may be empty. Tokens can change through `AUTH_TOKEN_REFRESH` and must be saved. Fetch profiles on demand rather than loading every member's profile.

Refreshing a Discord web-build number can update client metadata. Actual API, gateway, or authentication changes still require source changes and a new executable.

## Remaining work

- Test on a physical Windows 7 machine, including its root-certificate configuration.
- Verify voice in a real call.
- Unread counters, mention counters, and notification previews.
- Lottie stickers, threads, forums, friends, and settings.
- Executable icon/manifest and installation packaging.

## Licenses

Linkcord is **GPL-3.0**, inherited from discordo; see `LICENSE`. Distributing the executable requires making its corresponding source available under that license.

Gio uses MIT/Unlicense terms. Noto Color Emoji uses the SIL Open Font License 1.1; see `internal/gui/fonts/OFL.txt`.