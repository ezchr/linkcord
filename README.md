# Linkcord

A lightweight graphical Discord client for **Windows 7, 8, 10, and 11**, distributed as a single `.exe`. Linkcord uses [discordo](https://github.com/ayn2op/discordo), a Go terminal client, for its connection, authentication, and voice foundations. Its graphical interface is built with [Gio](https://gioui.org).

Linkcord was created for people on Windows 7 who can no longer run the official desktop application.

> **Unofficial client:** using an unofficial client carries a risk of account suspension under Discord's terms of service.

## Missing features

Linkcord does most of what Discord's app does. What it still lacks:

| Missing | Notes |
|---|---|
| Captcha | Discord's captcha cannot be shown; QR-code or token sign-in work instead. |
| Camera | Video in voice channels and calls; the video call button joins without it. |
| Activities | Games and apps played together in voice channels. |
| Notification previews | Unread markers and mention counts are there; desktop pop-ups with the message are not. |
| Threads and forums | |
| Lottie stickers | PNG, APNG and GIF stickers work. |
| Creating polls | Voting in polls works. |
| Multi-value select menus | Bot menus with one choice work. |
| RPC commands beyond activities | Rich Presence from games works. |
| Other languages | English and Spanish only; some text built from several parts is still English in Spanish. |
| Testing on a physical Windows 7 machine | Built and run for Windows 7, but only tested on newer Windows. |

Discord keeps one voice connection for an account, across all its devices, so Linkcord, as Discord's app, is in one voice channel or call at a time: joining a call while in a server's channel moves you to it.

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

Linkcord runs from one installed copy, `%LOCALAPPDATA%\Programs\Linkcord\linkcord.exe`, which updates replace. The first time any `linkcord.exe` starts, it copies itself there, puts a **Linkcord** shortcut to it on the desktop, and starts it. From then on, starting another copy opens the installed one instead, after installing itself first if it is the newer of the two, so an old download never updates itself again. The shortcut is made once; if it is deleted, it is not made again. Set `LINKCORD_PORTABLE=1` to run a copy where it is, as the test scripts do.

Normal startup opens a short update screen before sign-in. Discord metadata and GitHub release checks run in parallel. Offline failures leave the installed client usable; **Skip checks** continues with the cached or built-in Discord metadata.

Discord's current web-client build number is fetched from its login page, cached in `%LOCALAPPDATA%\linkcord\discord-build.json`, and used in HTTP headers and gateway identification. This updates metadata without a new executable. API, gateway, and authentication behavior still require code changes.

For newer stable GitHub releases, Linkcord downloads the `linkcord.exe` asset, verifies its size and GitHub SHA-256 digest, and checks that it is a Windows AMD64 executable. A hidden PowerShell helper waits for the current process to exit, replaces the file, and starts the new client. The new client must acknowledge its version after its first GUI frame; otherwise the helper restores the previous executable. A failed startup is recorded so the same release and digest do not cause a restart loop.

When the release also carries `linkcord-<running version>.patch`, Linkcord downloads only that patch, a small fraction of the executable, and applies it to the running copy. The result must match the release's `linkcord.exe` size and SHA-256 exactly; otherwise, as when no patch fits the version running, the whole executable is downloaded instead.

Replacement requires write access to the executable's folder. A release without a digest or with an invalid asset leaves the installed version in place. Each update is staged in its own folder in `%LOCALAPPDATA%\linkcord\updates`, out of the executable's folder, with the download, the executable it replaced and a log; the folders of the last three updates are kept and older ones removed. A copy on another drive than that folder stages beside itself, since a file can only be replaced from the same drive.

The repository is currently private. Release checks use `LINKCORD_GITHUB_TOKEN`, if set, or the current GitHub CLI sign-in when `gh` is installed. The GitHub account must have access to `ezchr/linkcord`. Public releases require no GitHub credential. GitHub and Discord credentials are separate; neither is embedded in the executable, release cache, or helper script.

| Option | Behavior |
|---|---|
| `--version` | Prints the embedded application version and exits. |
| `--check-updates` | Checks GitHub and Discord metadata, prints a JSON report, and exits without installing. |
| `--no-update` or `LINKCORD_NO_UPDATE=1` | Skips executable update checks but still refreshes Discord metadata. |
| `--demo` or `LINKCORD_DEMO=1` | Shows account-free demo data and performs no startup update requests. |
| `--low-power` or `LINKCORD_LOW_POWER=1` | Shows static GIF/APNG previews to reduce animation CPU and memory use. |
| `--system-fonts` or `LINKCORD_SYSTEM_FONTS=1` | Lets Gio look through all of Windows' fonts for characters Linkcord's own fonts lack, which uses much more memory. |

Development builds identify themselves as `v1.4.2-dev`; they cannot downgrade to an older stable release. For a release, build the graphical entry point with its exact GitHub tag:

```powershell
.\build-linkcord.ps1 -Version v1.4.2 -Out ..\linkcord.exe
```

Publish that executable as `linkcord.exe` on the matching stable release, with a patch from the previous release so that its users download only what changed:

```powershell
gh release download vPREVIOUS -p linkcord.exe -D prev
go run ./cmd/linkcord-patch prev\linkcord.exe ..\linkcord.exe linkcord-vPREVIOUS.patch
```

Upload `linkcord-vPREVIOUS.patch` beside `linkcord.exe`. The tool checks that the patch reproduces the new executable exactly before writing it. The helper checks the restarted application's embedded version against the release tag. A fresh build uploaded under an incorrect tag will not acknowledge startup.

If a fixed asset is uploaded under the same tag with a new digest, it can be tried again. The failure record is `%LOCALAPPDATA%\linkcord\updates\failed-version`; release metadata is cached in the same folder.

## Interface and resume behavior

Images, GIFs, and videos share the same full-window viewer with zoom, pan, and corner controls. Videos also have pause, mute, and replay controls. GIF previews play automatically in normal mode; GIF embeds served as videos use FFmpeg to create a cached animation.

Drag the channel pane's right edge or the member pane's left edge to adjust its width. Widths are saved between launches. The interface uses a darker palette.

The active chat refreshes from Discord when the window regains focus, returns from minimization, or the gateway recovers. A lightweight watchdog also detects sleep and hibernation gaps when focus stays unchanged. Refresh closes multi-page message gaps, preserves drafts and older loaded history, and keeps gateway changes received during the request. Temporary failures retry automatically.

## Resource usage

Fonts are most of what Linkcord keeps in memory, as Gio reads every glyph of a font it loads. So Segoe UI, Go and Noto Color Emoji are loaded at start, and the rest only once text shown needs them: Segoe UI Symbol and Cambria for symbols and decorative letters, Yu Gothic (also used for Chinese characters) for Japanese and Chinese, Malgun Gothic for Korean, and Nirmala, Leelawadee, Myanmar Text and Ebrima for Indic scripts, Thai, Myanmar and Ethiopic, one face of each. Gio does not look through the rest of Windows' fonts for characters these lack, which would read a whole font for one mark; `--system-fonts` lets it, at that cost. Emoji variation selectors (U+FE0E, U+FE0F) are left out of drawn text, as no font has them.

Animated GIFs, APNGs, emoji and avatars play while Linkcord's window is in front, and stop while it is in the background or minimized, as Discord plays GIFs only while it is focused: every frame of an animation redraws the window. Avatar decorations and nameplates move only under the pointer, and on profile cards.

Decoded images use a 64 MiB cache budget with eviction of older completed entries. Up to four cached images are read from disk at once and twelve downloads run at once, with at most 64 pending loads. Small files, such as emoji and stickers, are also kept as downloaded, so that showing them at another size needs no download. Each animation retains at most 48 MiB of frames, with a maximum decoded side of 2048 pixels. Animations that exceed the retained-frame budget merge frame delays, preserving duration with fewer retained frames. The currently requested entry is protected from eviction.

Open your own user profile and click **Performance Mode** to toggle still GIF/APNG previews. The choice is saved locally and applies immediately. On uses the first animation frame without retaining subsequent frames or scheduling animation redraws. Off restores animation loading. `--low-power` or `LINKCORD_LOW_POWER=1` overrides the saved startup preference; `LINKCORD_LOW_POWER=0` starts in normal mode.

Video attachments and direct video embeds open inside Linkcord with pause, mute, replay, and close controls. Closing the viewer or logging out cancels playback. Playback requires `ffmpeg` and `ffprobe` on PATH, beside `linkcord.exe`, or in its `media` subfolder. Videos download to a temporary file before playback; closing the viewer removes the file after decoder shutdown. The current limits are 256 MiB per download, a 4K pixel count, and 120 fps. Provider watch pages without direct media URLs and DRM streams are unsupported. Variable-frame-rate input is normalized to its reported average frame rate. Builds with `no_voice` play video silently.

These are application cache limits, not a limit on total process memory. Fonts, message state, the Go runtime, and graphics-driver allocations also consume memory.

For a controlled diagnostic session, set `LINKCORD_PROFILE` to an output folder. On a normal window close, or after `LINKCORD_PROFILE_SECONDS` seconds with Linkcord still open, it writes `cpu.pprof`, `heap.pprof`, and `summary.json`, including rendered frame count and heap statistics. Ordinary launches do not enable profiling.

## Screen sharing

Screen sharing encodes with Cisco's OpenH264 2.6.0, which is not in the executable: the first stream downloads it from Cisco, as browsers do, so that Cisco covers its H264 licence, checks its SHA-256 digest, and keeps it in `%LOCALAPPDATA%\linkcord`. It needs only kernel32, so it runs on Windows 7. A screen is captured with DXGI's desktop duplication on Windows 8 and later, which also sees games in full screen and follows the screen when they change its resolution, and with GDI on Windows 7. A window's sound is captured from its own processes on Windows 10 version 2004 and later. A screen's is that of every program playing but Linkcord and Discord, each captured on its own and mixed, since Windows leaves only one program out of a capture: Discord's sound is the voice channel's, which would otherwise be sent back to it. Older Windows send the sound of the whole system. Frames are encrypted with DAVE as Discord's own clients do. While streaming, your own card shows what you share as wide as the card, ten times a second.

## Rich Presence

Linkcord serves Discord's local RPC on the first free pipe of `\\.\pipe\discord-ipc-0` to `-9`, as Discord's app does, so that games and programs that show Rich Presence show it on the user's profile. Only the handshake, `SET_ACTIVITY` and pings are answered. The activity is sent to the gateway as Discord's app sends it: with the application's name, its pictures by ID, or proxied from the web, times in milliseconds, and buttons. When Discord's app runs too, it has the first pipe, and programs talk to it.

## Linkcord's server

`server/linkcord-api` is a small server, run at `https://zidpvp.xyz/linkcord`, that Linkcord tells the user uses Linkcord, by Discord ID and version, and learns every ten minutes who else does, to show Linkcord's logo beside their names. Crashes and freezes written to `crash.txt` and `freeze.txt` are sent there once; what was in them before is not. No messages, tokens or addresses are kept. Both can be turned off in the Advanced settings; they are kept in `community.json`. Reports are read at `/admin/reports?token=…`, with the token the server writes to its data folder.

The server runs as the `linkcord-api` systemd service (`server/linkcord-api/linkcord-api.service`) on `127.0.0.1:5310`, behind nginx's `location /linkcord/`. To update it: `GOOS=linux CGO_ENABLED=0 go build` in its folder, copy the binary to `/opt/linkcord-api/`, and restart the service.

## Settings

The gear beside the microphone and headset buttons opens the settings; Escape or the round button closes them. Voice & Video has the microphone and speakers, their volumes from 0 to 200%, a microphone test, and the input mode: voice activity, with a sensitivity dragged over the level of the microphone, or push to talk. Keybinds sets the keys to talk, mute and deafen; they work in any window, even while Linkcord is hidden, by reading the state of the keys rather than taking them, so other programs still get them, and on Windows 7. Mouse side buttons can be keybinds too. The choices are saved in `voice.json` beside the log. The demo does not save them.

What the microphone captures is made as loud as the input volume says, cleared of noise by Xiph's RNNoise, evened out by an automatic gain control, and kept from clipping by a limiter, in blocks of 10 ms; the microphone test plays it back as it is sent. Noise suppression and the gain control are on by default, as in Discord.

Discord's sounds are downloaded from discord.com, as its web app does, the first time Linkcord starts, and kept in `%LOCALAPPDATA%\linkcord\sounds`. `internal/dcsound` names their files; should Discord change them, it finds them again in the web app's script.

In a voice channel, the log says every minute how many voice packets were sent and how they were encrypted (`voice stats`). When nothing anyone says can be decrypted for five seconds, as when the keys of the call changed without Linkcord following, Linkcord joins the channel again. If the window stops answering for 20 seconds, what every part of Linkcord was doing is written to `freeze.txt` beside the log.

## Test without an account

| Environment variable | Purpose |
|---|---|
| `LINKCORD_PORTABLE=1` | Runs the copy started where it is, instead of the installed one. |
| `LINKCORD_DEMO=1` | Displays made-up servers, users, and messages without connecting to Discord. Fixtures are in `internal/gui/demo.go`. |
| `GIO_LEGACY_MOUSE=1` | Uses Windows 7 mouse messages to test that path on Windows 10/11. |
| `GIO_D3D11_WARP=1` | Uses software rendering instead of a graphics card. |
| `LINKCORD_TOKEN=...` | Overrides the locally saved Discord token. |
| `LINKCORD_RENDER=...` | Output folder for screenless rendering tests. |
| `LINKCORD_LIVE_TENOR=...` | A Tenor media URL for the optional live animation smoke test. |
| `LINKCORD_LIVE_APNG=...` | An APNG URL for the optional live animation smoke test. |
| `LINKCORD_DEMO_VIDEO=...` | A video URL the demo shows in a message, to test playback. `LINKCORD_DEMO_VIDEO_SIZE=360x640` gives it another shape and makes it the newest message. |
| `LINKCORD_DEMO_CALL=1` | The demo rings with a call from a made-up friend, who is in it. |

`scripts/test-window.ps1` sends clicks, wheel events, and keys directly to the application's window without moving the real mouse, then captures its content even when another window covers it. See the script's header for usage.

```powershell
.\scripts\test-window.ps1 -Demo -Script { Start-Sleep 4; Snap "start"; MouseClick 500 632; TypeText "@an"; Snap "mentions" }
```

For screenless media renders, set `LINKCORD_RENDER` and run `go test ./internal/gui -run '^TestRenderMedia$' -count=1`.

Local application data:

- Log: `%LOCALAPPDATA%\linkcord\logs.txt`; crashes are written to `crash.txt` beside it. The demo writes `demo-logs.txt` instead.
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
| `internal/gui/voice.go` | Voice panel, controls, speaking indicators, and saved per-user volumes and devices, backed by `internal/voice`. |
| `internal/gui/voicecall.go`, `voicemenu.go` | Call view, soundboard, device menu, and the right-click menu of voice members with its moderation actions. |
| `internal/gui/golive.go` | Stream picker, the user's own stream on its card, and who watches it. |
| `internal/golive/` | Go Live: screen and window capture, H264 encoding with OpenH264, sound capture, and the stream's voice connection. |
| `internal/gui/menu.go` | Context menus opened at the pointer. |
| `internal/gui/theme.go`, `widgets.go` | Colors, fonts, and shared interface components. |
| `internal/gui/demo.go` | Account-free demo fixtures. |
| `internal/gui/render_test.go`, `media_render_test.go` | Screenless interface rendering tests. |
| `internal/http/` | Discord request headers, client properties, and cached web-build refresh. |
| `internal/update/` | GitHub release checks, verified downloads, Windows replacement, and rollback. |
| `internal/gui/settings.go` | The settings screen: voice and video, keybinds, and Performance Mode. |
| `internal/hotkey/` | Keybinds that work in any window, and recording the keys pressed. |
| `internal/gui/appearance.go`, `appearance_page.go`, `colorpicker.go` | Presets and colors of the interface, and their page in the settings. |
| `internal/gui/poll.go` | Polls in messages. |
| `internal/rpc/`, `internal/gui/richpresence.go` | Discord's local RPC, and the activities it shows. |
| `internal/hub/`, `internal/gui/community.go`, `server/linkcord-api/` | Linkcord's server: who uses Linkcord, and error reports. |
| `internal/install/` | The installed copy that every copy started opens, and its desktop shortcut. |
| `internal/version/` | Embedded version shared by both entry points. |
| `third_party/patches/` | Patches to the vendored Gio libraries. |

The UI state belongs to the window goroutine. Background work must enter through `u.post(func() { ... })`; `u.win.Invalidate()` requests a new frame.

When merging upstream discordo changes, replace `github.com/ayn2op/discordo` module imports with `github.com/ezchr/linkcord`. Linkcord's graphical interface is separate from the original terminal interface.

## Vendored library patches

`go.mod` uses local copies of Gio and its extensions, Arikawa and dave-go. Patch files are retained in `third_party/patches/` so the changes can be reapplied to newer versions.

- **Gio v0.10.3:** checks for Windows APIs unavailable on Windows 7 and falls back to classic mouse messages. Direct3D hardware failures fall back to WARP.
- **gioui.org/x v0.10.3:** draws image glyphs in rich text so color emoji remain visible; also supports the project's rich-text span callback, and reports where every span and its letters are laid out, for selecting text.
- **Arikawa (ayn2op fork, 2026-10-12):** users carry their avatar decoration, nameplate and server tag (`avatar_decoration_data`, `collectibles`, `primary_guild`), which Discord sends with every user and Arikawa dropped. A voice session tells every voice server it is moved to that it speaks, as it told the first; otherwise, after Discord moves a call to another server, nobody hears the user.
- **Arikawa, polls:** messages carry their poll (`poll`), which Arikawa dropped.
- **RNNoise 0.1.1:** vendored unchanged in `third_party/rnnoise`, with its trained model and an empty `config.h`, for noise suppression.
- **dave-go v0.5.1:** adds end-to-end encryption of H264 frames laid out as libdave does, taking a fresh nonce for every retry, for screen sharing. Upstream encrypts Opus only.

## Fonts and color emoji

Emoji in messages, reactions and the picker are Twemoji pictures, as Discord draws them, from jsDelivr, kept in the image cache. Noto Color Emoji, embedded in the executable, draws emoji elsewhere, such as in names, and those Twemoji lacks. Segoe UI Emoji's COLR layers are unsupported by the current renderer.

`narrowSpace` in `theme.go` reduces the emoji font's unusually wide space; `theme_test.go` covers it. The fallback fonts are listed in `theme.go` and loaded as `fontneeds.go` finds text that needs them; see Resource usage.

Do not set a fixed `th.Face`: bold text must retain fallback-font lookup.

The interface's text goes through `tr` in `i18n.go`, which `label` and the text fields call; `i18n_es.go` holds the Spanish, by the English text. Text that starts or ends with a space also translates the beginning or end of longer text, such as "Replying to " and a name.

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
- Camera in voice channels and calls.
- Notification previews, threads and forums, and Lottie stickers.
- Text built from several parts, such as "MEMBERS — 3", in Spanish.

## Licenses

Linkcord is **GPL-3.0**, inherited from discordo; see `LICENSE`. Distributing the executable requires making its corresponding source available under that license.

Gio uses MIT/Unlicense terms. RNNoise, in `third_party/rnnoise`, is BSD-3-Clause; see the `COPYING` beside it. The OpenH264 headers in `third_party/openh264` are BSD-2-Clause; see the `LICENSE` beside them. Noto Color Emoji uses the SIL Open Font License 1.1; see `internal/gui/fonts/OFL.txt`.