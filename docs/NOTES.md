# Things learned the hard way

## Inbox: what counts as a mention, and where the entries come from

**What Discord does.** The official inbox's Mentions tab calls
`GET /users/@me/mentions?limit=25&roles=true&everyone=true`. The unofficial
docs say it answers a plain JSON array of message objects, the newest first,
with no wrapper object (unlike the search, which answers
`{total_results, messages: [[...]]}`); no one has called it from Linkcord
yet. Messages without `guild_id` get the server of their channel, if known.
Direct messages are not in it. A reply whose ping was turned off is not a
mention for Discord: the user is not in `mentions`, so the endpoint never
lists it. Only the gateway shows those: a `MESSAGE_CREATE` of type 19
(`InlinedReplyMessage`) whose `referenced_message.author` is the user.

**What the code does.** ningen's `State.MessageMentions` does not check role
mentions (it has a `TODO: roles`), and the red mention counts come from the
read states (`MentionCount`), worked out by Discord, not by the client. The
message highlight (`chatView.mentionsMe` in `internal/gui/actions.go`) is the
client's own test, but it reads the server of the channel open. The inbox has
its own pure version, `classifyInbox` in `internal/gui/inbox.go`, which takes
the server's and channel's settings as `inboxRules`:

- the user in `mentions`, or `<@id>` / `<@!id>` in the text: always a mention;
- `@everyone`/`@here`: unless the server suppresses them, or the channel (or
  the server, when the channel follows it) notifies of nothing;
- a role: unless the server suppresses role mentions, and only the user's
  roles. For messages from the mentions endpoint any role counts, as Discord
  already chose them and the user's member may not be known here;
- a reply to the user's message is a Reply, ping or not. Forwards and a
  thread's first message also carry `referenced_message` but are not replies.

**Unseen.** An entry is new until it is opened, "Mark All as Read" is
pressed, or its channel's read state is at or past it. Entries from the
endpoint that were read before they arrived start as read, so the first open
does not light 25 old mentions.

**Where it is kept.** `inbox.json` beside `logs.txt` (the user cache folder,
`%LOCALAPPDATA%\linkcord`), with the account's ID: another account's file is
ignored. The last 100 entries, written through a `.tmp` file and a rename.

**How to test.** `go test ./internal/gui -run Inbox` covers the classifier,
the capped newest-first list without duplicates, and the file round trip.
`LINKCORD_DEMO=1` fills the inbox from the demo's messages: two mentions and
a reply without a ping in a thread. Tests that build a `chatView` without a
state must not reach the inbox: `noteInbox` does nothing without `c.st`, so
that no test reads or writes the real `inbox.json`.

## Background picture and colors from it

- **One picture, made once per window size.** `paint.NewImageOp` uploads a
  texture the first time the op is drawn and reuses it while the same op is
  drawn again; a new op each frame uploads again. `background.go` keeps one
  `paint.ImageOp` and makes it again only when the window has kept a new size
  for 300 ms (`resizeSettle`), or the picture, blur or low power mode change.
  Meanwhile the old picture is stretched to the new size with `op.Affine`,
  which costs nothing.
- **Blur is a small picture.** The GPU stretches an `ImageOp` with linear
  filtering, so a picture kept 1+blur times smaller than the window, box
  blurred once on the CPU (`boxBlur`), looks like a strong blur and takes less
  memory than a sharp one. Low power mode keeps it half size again.
- **Gio mixes colors in linear light.** A column drawn at alpha over the
  dimmed picture has a luminance of `a*L(column) + (1-a)*(1-dim)*L(picture)`,
  as luminance adds up in linear light. `minPictureDim` solves that for the
  least dim at which text keeps 4.5:1 over the brightest twentieth of the
  picture, so a white picture is darkened more than the slider says.
- **White text on an accent.** With the default theme's brightest text
  (#f2f3f5), Discord's blurple is about 4.2:1. A picture's accent is darkened
  until the header color reaches 4.5:1 on it, so orange or yellow pictures
  give a brownish accent; the link color is the same hue made light instead.
- **Message text keeps its colors.** `markdown.go` caches each message's
  spans, colors included, keyed by its content and the minute. Changing a
  color left links and mentions as they were for up to a minute;
  `themeGen`, counted by `themeChanged`, is now part of the key.
- **Where it is:** `palette.go` (`derivePalette`, contrast), `background.go`
  (loading, drawing, dim), `customize_page.go` (settings). The colors from
  the picture are saved in `appearance.json` under `picture.colors` and used
  as the preset `picture`, so single colors still change over them and Reset
  goes back to them. Tests: `go test ./internal/gui -run 'Palette|MinPictureDim|CoverImage|BoxBlur|AppearanceFiles'`.

## Moving the bars

- **The columns are placed by hand.** `layoutColumns` (`panelayout.go`)
  computes a rectangle for each column with `placePanes` and records them in
  `c.cols`; everything that opens beside a column reads them: the composer
  popups (`c.comp.paneX/paneW/paneBottom`), the voice history and the user's
  own profile card (by the user panel), the server menu (under the channel
  header), the stream preview (beside the channels). A popup placed from a
  fixed `gtx.Dp(72)` breaks once the server list moves.
- **Resize edges measure from the outer edge.** `paneResize` sets the width to
  `direction*(pointer.X - origin)`: for a column on the left, the origin is its
  left edge and the direction 1; on the right, its right edge and -1.
- **A horizontal list does not scroll with the wheel.** On Windows the wheel
  gives `Scroll.Y`; Gio gives `Scroll.X` only for a tilted wheel or with
  Shift. Scroll that a handler does not take passes on to the handlers under
  it (`io/input/pointer.go`, `deliverEvent` and `clampScroll`), so a handler
  with a `ScrollY` range registered before the list catches the rest and moves
  the list (`updateGuildWheel`).
- **The server strip reuses the column's drag code.** Entries along the strip
  are as long as rows down the column (56 dp, 10 more after the direct
  messages), so `entryAt` and `itemTop` work on `c.along(p)` and the drop
  marks are the column's rectangles turned by `c.across`.
- **Where it is saved:** the places are in `appearance.json` under `layout`
  (only places other than the default), the widths stay in `panes.json`. The
  demo reads `appearance.json` but not `panes.json`.

## Seeing the interface without a screen

- Screenshots of the demo window by `scripts/test-window.ps1` came out white
  in a session whose screen was locked; `TestRenderDemo` (`render_test.go`)
  draws with Gio's headless GPU instead. `LINKCORD_RENDER` is the folder for
  the pictures, `LINKCORD_RENDER_STEPS` what to do (`click:x,y;wait:ms;snap:name`)
  and `LINKCORD_RENDER_APPEARANCE` an `appearance.json` to draw with.
- Linkcord's files are in `%LOCALAPPDATA%\linkcord` (`os.UserCacheDir`) and
  its config in `%APPDATA%`. Setting both to a test folder, with
  `LINKCORD_PORTABLE=1` and `LINKCORD_DEMO=1`, runs a test build without
  touching the installed one; the demo also skips the single instance lock.
- The checkout has CRLF line endings (`core.autocrlf=true`), so `gofmt -l`
  lists every file; check the files changed with `gofmt -d`.

## Forwarding messages

- **The request.** A forward is a message create in the place it goes to
  (`POST /channels/{id}/messages`) whose `message_reference` has `type: 1`
  and the message's `message_id`, `channel_id` and, in a server,
  `guild_id`. It has no content of its own; Discord fills
  `message_snapshots` in what it creates. `forwardData` (`forward.go`)
  builds it and it goes through `SendMessageComplex`, as the box's
  messages do, so the nonce, `tts`, `flags` and the headers are the same.
  The text written in the form is a second, plain message after it.
- **Arikawa refused it.** `SendMessageComplex` answers `ErrEmptyMessage`
  for a message without content, embeds, files or components, before any
  request. It now lets a reference of type forward through
  (`third_party/patches/arikawa-forward.patch`). `MessageReference`
  already had `type`.
- **What cannot be forwarded** (`forwardable`): system messages, messages
  only the user sees, polls, messages deleted and kept here, and messages
  of a channel whose history the user may not read. Voice messages are
  offered; whether Discord takes them was not tried.
- **Where to.** `dmDests` and `guildDests` list the direct messages, the
  most recent first, then each server's channels in the order of its
  channel list with the threads joined under their channel. They are
  worked out once when the form opens, as the permissions of every
  channel of every server are too much for each frame.
- **Several places.** One request at a time, 0.5 to 1.5 s apart
  (`paceDest`). A place that refuses a message gets no more of them and
  says why on its line; the others go on. "Send Again" sends only what a
  place lacks (`forwardPlan`).
- **Arikawa hides 429s.** Its client waits what `Retry-After` says and
  sends the request again, up to 5 times, so the caller never sees most
  of them. `noteRateLimit`, an `OnResponse` hook set in `newState`,
  counts them in `rateLimitHits`. `st.OnResponse` is `api.Client`'s
  method, not the list of hooks: that is `st.Client.Client.OnResponse`.
- **Not tried against Discord.** Nobody sent a forward with this build:
  the body is what the web app is known to send, less its empty
  `content` and `mobile_network_type`, which Linkcord's other messages
  leave out too. Slow mode answers 429 with code 20016, which Arikawa
  may wait out like a rate limit before the form hears of it.
- **How to test.** `go test ./internal/gui -run 'Forward|Dests|SendErrorText'`.
  In the demo the form sends nothing and adds the forward to the place
  chosen. `TestRenderDemo` has `rclick:x,y` for the message's menu.

## Selecting several messages

- **Holding the button.** Gio has no long press. `updateSelectPointer`
  (`multiselect.go`) keeps the message pressed, when and where, asks for a
  frame 500 ms later with `op.InvalidateCmd{At: …}`, and starts selecting
  if the button is still down and the pointer moved less than 6 dp.
  Dragging to select text moves further, so it does not start it. A press
  whose end never comes, as of a message scrolled out of the list, is
  dropped after a second more.
- **Clicks that must do nothing.** While selecting, each message is
  covered by an area that takes the pointer (`selectArea`, without
  `PassOp`), so links, pictures, names and reactions under it get
  nothing. This also takes care of the release that ends the hold: Gio
  works out again what is under the pointer after every frame
  (`pointerQueue.Frame`), the link under the new area gets a `Leave`, and
  `gesture.Click` reports a release after a `Leave` as cancelled, not as
  a click. Handlers laid out over the message with `PassOp` (the double
  click that replies, the right click) still get the press and check
  `c.msel.on` themselves.
- **An area over a message blocks what is beside it, not the list.** The
  wheel still scrolls the messages: the list's handler is an ancestor of
  the area, and only handlers under it in the same branch are covered.
- **The gap between messages is not part of them.** A click exactly
  between two rows ticks nothing.
- **Many messages, one request each.** A user's account has no request
  that deletes or reacts to several messages (bulk delete is for bots),
  so `bulkReact` and `bulkDelete` send what one message's button sends
  (`requestReact`, `requestDelete`), and forwarding those selected goes
  through the form's own run. `runSteps` (`bulkpace.go`) sends one at a
  time, in the order shown, and never two at once.
- **The waits.** `bulkpace.go` has every number: 1.5 to 4 s between
  deletes, 0.8 to 2.5 s between reactions, 2 to 5 s between messages
  forwarded, most of them in the lower half of the range, with a pause
  of 5 to 12 s after every 8 to 15 messages. A 429 that Arikawa waited
  out makes the rest 1.5 to 2 times slower; the second one of a run
  stops it and says how many were done. A run takes at most 50 messages.
  This is the pace of doing it by hand, so that a run is not a burst of
  requests; it lowers the risk of Discord limiting or flagging the
  account, and nothing can promise how Discord judges one.
- **Stopping.** Stop ends the run after the request on its way; what was
  deleted, sent or reacted to stays so. Changing channel stops it too.
- **The demo** sends nothing and waits a tenth of the time (`demoPace`).
- **How to test.** `go test ./internal/gui -run 'RangeIDs|MultiSelect|Selectable|SplitDeletable|CopyLines|Pace|Pacer|RunSteps|RetryAfter|Bulk'`.
  `TestRenderDemo` holds the button with `press:x,y;wait:700;release:x,y`.
## Echo cancellation

**The report.** With the sound on a monitor's speakers and the microphone on
the desk, the others heard themselves. Discord's client does not do this,
whatever its noise suppression is set to (Standard, Krisp or None), so it is
not the noise filter: it is WebRTC's echo canceller, and a voice activity
threshold that rises while something is played.

**What the audio path does** (`internal/voice`). Every device is opened by
`startDeviceOn` at 48 kHz, stereo, 16 bits, whatever the hardware runs at:
miniaudio converts. So what is played and what is captured always have the
same rate, and a mono microphone arrives as two equal channels.

- Played: the playback device calls `Session.mix` every 20 ms, which adds
  every speaker's decoded voice, already at its volume, into the buffer.
  Streams watched and videos (`StartMediaAudio`), Discord's sounds
  (`Effects`), voice messages (`Player`) and the microphone test each open a
  playback device of their own.
- Captured: the capture device calls `Session.captured` every 10 ms, which
  queues the bytes; `Session.send` cuts them into frames of 20 ms, runs the
  `processor` (input volume, RNNoise, gain control, limiter), tells whether a
  sample reaches the sensitivity, and encodes. There is no automatic
  sensitivity: the threshold is the one dragged in the settings, -40 dB
  unless changed, and it is compared after the gain control, which raises a
  quiet signal up to 6 times. That is why a faint echo was sent: left alone
  for a second, the gain control brought it over the threshold.

**What was added** (`internal/voice/echo.go`). `Session.mix` gives what it
mixed to `echo.played`, and `Session.send` runs `echo.capture` on each frame
before the processor, so the canceller sees the microphone as captured.

1. *The canceller*, `third_party/aec3`: WebRTC's AEC3. What is played goes in
   mono, in blocks of 10 ms; what is captured in one channel while both are
   equal and in two from the first frame in which they differ. It finds the
   delay between the two by itself (76 ms found for 80 ms, 244 for 250) and
   follows it.
2. *The gate*, `echoGate`, after it. It keeps the loudest block played over
   the last 400 ms and learns the reach of the echo: the mean square of what
   is captured, and of what the canceller left, over that of what was played,
   in windows of 20 frames in which the user did not speak, the largest of
   the last three. While something is played, a frame is the user's only if
   it is 6 dB louder than the reach times the loudest played. With nothing
   played it changes nothing. `send` takes a frame as loud enough only if
   the gate also says it is the user's; push to talk is not gated.

Streams, videos and Discord's sounds reach the canceller through `tap`s, added
to what `mix` played. Voice messages and the microphone test do not.

**Why AEC3, and how it is built.** It is what Discord and Chromium use, and
it handles what a simpler canceller does not: an unknown delay, a room that
goes on longer than its filter, two people talking at once. It was thought
too heavy to vendor; it is not, once the rest of the audio processing module
is left out:

- 81 source files of webrtc-audio-processing 2.1 and their headers, and 34
  headers of Abseil, none of its sources. Found by compiling
  `echo_canceller3.cc` with what it includes, linking, and adding the file of
  each undefined symbol until none was left; `g++ -MM` then listed the
  headers used.
- cgo compiles only the files in the package's folder. The `webrtc_*.cc`
  files there `#include` the sources from the subfolders, a few at a time, so
  no build system is needed. Go does not look into the subfolders: after
  changing a file there, `go clean -cache` or touch a `webrtc_*.cc`.
- `RTC_DISABLE_LOGGING` leaves out WebRTC's logging, which is what needed
  Abseil's sources and the time and thread code.
- `multi_channel_content_detector.h` does not include `<cstdint>` and GCC 16
  no longer lets that pass; the `webrtc_*.cc` files include it first.
- The AVX2 files are not built. They need `-mavx2` for the whole file, cgo
  gives the same flags to every file, and a function of the standard library
  compiled with AVX2 could be the copy the linker keeps for everything, which
  crashes on an old processor. Without `WEBRTC_ENABLE_AVX2` AEC3 never calls
  them and uses SSE2; `aec3_noavx2.cc` only gives the linker their names.
- `#cgo windows LDFLAGS: -static` links libstdc++ and winpthreads in. The
  executable imports the same DLLs as before, and no function that Windows 7
  lacks (compared with `objdump -p`), and grows from 41.8 to 42.3 MB.

SpeexDSP was not needed. The echo canceller that comes with Windows was not
tried: it would have to replace miniaudio's capture, and takes out every
program's sound, not Linkcord's.

**Measured** (synthetic speech, a room of 41 reflections over 100 ms, the
echo 10 dB below what is played; `go test ./internal/voice -run Echo -v`):

- Echo alone: lowered by 26 dB in the first second and by 62 to 72 dB after
  5, with the echo 80 or 250 ms late or as loud as played. A real room, with
  speakers that distort, gives far less than these: AEC3 is usually quoted at
  20 to 40 dB. Nothing of what was left was sent.
- Clocks apart: 47 dB with the microphone's clock 100 parts in a million
  fast, 21 dB with 500. The canceller loses the room for a moment each time
  the delay has moved by a block, and the gate stops about half of that.
- The user over the others, 8 dB louder than the echo: comes out 1.2 dB
  quieter and 0.92 alike to what was said; 93% of the frames spoken are sent.
- Nothing played: 0.5 dB quieter, 0.96 alike (AEC3 always removes what is
  below speech), 5 to 9 ms late. That is all the delay it adds.
- The gate alone, as before the canceller has learned the room: no frame of
  echo sent; a user 23 dB louder than the echo has 96% of the frames spoken
  sent. A user only 17 dB louder has under half: the gate compares with the
  loudest played of late, and what it does not take for the user it learns
  as echo. With the canceller working this does not matter.
- Cost (`-bench Echo`): 0.17 ms for a frame of 20 ms on a Ryzen 5 8640HS,
  0.9% of one core, with a mono microphone; about 1.4 times that with a
  stereo one. Nothing is done while muted for more than half a second.

**Traps in the tests.**

- AEC3 fills what it removes with comfort noise as loud as the noise it
  thinks the room has. A test signal that never pauses, as noise with a slow
  tremolo, is taken for the room's noise, and the output settles 16 dB below
  the echo however well it is cancelled. Speech with pauses is needed.
- Counting the frames the gate takes for the user counts every pause of the
  others too, in which the gate is rightly open. Count what `send` would
  send: frames loud enough, and the hangover after them.
- A gate that lowers its estimate at once on one low measure leaks: when a
  loud syllable starts, its echo has not arrived yet and the measure is far
  too low. Hence the largest of three.

**What only a call can tell.** How much is left in a real room; whether the
delay between `mix` and the microphone stays within what AEC3 follows when
the processor is busy; whether the sound of a stream, which comes through
another device than the voices and so a few milliseconds apart, is cancelled
as well as the voices; whether the gate cuts the user while the others talk
loudly on speakers close to the microphone.
