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
