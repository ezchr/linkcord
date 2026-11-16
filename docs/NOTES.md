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
