package gui

import (
	"cmp"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/json/option"
	"github.com/ayn2op/arikawa/v3/utils/sendpart"
	"github.com/ncruces/zenity"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconAdd   = mustIcon(icons.ContentAddCircle)
	iconEmoji = mustIcon(icons.SocialMood)
	iconFile  = mustIcon(icons.EditorInsertDriveFile)
)

// maxUpload is the largest file Discord takes from accounts without Nitro.
const maxUpload = 10 << 20

// pendingFile is a file chosen to be sent with the next message.
type pendingFile struct {
	path, name string
	size       int64
	remove     widget.Clickable
}

// mentionItem is a person or role offered when typing @.
type mentionItem struct {
	name, sub string
	color     colorT
	user      *discord.User
	role      bool
	// markup is what is sent in place of @name: <@id>, <@&id> or @everyone.
	markup string
}

// composerState is the box to write messages, with its attachments, its
// emoji picker and its list of mentions.
type composerState struct {
	files []*pendingFile
	// voice is the voice message being recorded.
	voice     voiceRecording
	attachBtn widget.Clickable
	emojiBtn  widget.Clickable
	// stickerBtn and gifBtn open the picker on its tab.
	stickerBtn, gifBtn widget.Clickable
	sending            bool
	// emojiBackdrop closes the emoji picker when clicked.
	emojiBackdrop widget.Clickable

	// The list of mentions while typing @: the start of the @ in runes, what
	// follows it, the items that match, and the one picked with the arrows.
	mentionOn     bool
	mentionStart  int
	mentionQuery  string
	mentionItems  []mentionItem
	mentionSel    int
	mentionClicks []widget.Clickable
	mentionList   widget.List
	searched      string
	// dismissed is the @ that Escape closed, which stays closed until the
	// text before the caret changes.
	dismissed string
	// mentions maps the @name put in the box to what is sent for it.
	mentions map[string]string

	emoji *emojiPicker

	// The place of the box, for the popups drawn above it.
	paneX, paneW, boxH int
}

func (c *chatView) attachFiles() {
	owner := zenity.Attach(c.u.hwnd)
	go func() {
		paths, err := zenity.SelectFileMultiple(zenity.Title("Attach files"), owner)
		if err != nil {
			if err != zenity.ErrCanceled {
				slog.Error("failed to pick files", "err", err)
			}
			return
		}
		c.u.post(func() { c.attachPaths(paths) })
	}()
}

// attachPaths adds files to the message being written, leaving out folders
// and those too big for Discord.
func (c *chatView) attachPaths(paths []string) {
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > maxUpload {
			c.sendErr = fmt.Sprintf("\"%s\" is %s; Discord only accepts files up to 10 MB without Nitro.", filepath.Base(p), formatSize(uint64(info.Size())))
			continue
		}
		c.comp.files = append(c.comp.files, &pendingFile{path: p, name: filepath.Base(p), size: info.Size()})
	}
	gtxFocus(c)
}

// pasteDir holds the pictures pasted into the box until they are sent.
var pasteDir = filepath.Join(os.TempDir(), "linkcord-paste")

// paste handles Ctrl+V in the box: files copied in Explorer and pictures,
// such as screenshots, become attachments; anything else is text, which the
// box pastes as usual.
func (c *chatView) paste(gtx layout.Context) {
	clip, err := readClipboard()
	if err != nil {
		slog.Warn("failed to read the clipboard", "err", err)
	}
	switch {
	case len(clip.files) > 0:
		c.attachPaths(clip.files)
	case len(clip.png) > 0:
		c.attachPicture(clip.png)
	default:
		gtx.Execute(clipboard.ReadCmd{Tag: &c.composer})
	}
}

// attachPicture attaches pasted picture data as image.png, as Discord names
// pasted pictures. Each paste gets its own folder so the names do not clash.
func (c *chatView) attachPicture(data []byte) {
	cleanPastes()
	if err := os.MkdirAll(pasteDir, 0o755); err != nil {
		c.sendErr = "Could not keep the pasted picture: " + err.Error()
		return
	}
	dir, err := os.MkdirTemp(pasteDir, "p")
	if err == nil {
		path := filepath.Join(dir, "image.png")
		if err = os.WriteFile(path, data, 0o644); err == nil {
			c.attachPaths([]string{path})
			return
		}
	}
	c.sendErr = "Could not keep the pasted picture: " + err.Error()
}

// cleanPastes removes pasted pictures older than a day, long since sent.
func cleanPastes() {
	entries, err := os.ReadDir(pasteDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			os.RemoveAll(filepath.Join(pasteDir, e.Name()))
		}
	}
}

// gtxFocus asks for the box to get the keyboard focus on the next frame.
func gtxFocus(c *chatView) {
	c.focusComposer = true
	c.u.win.Invalidate()
}

func (c *chatView) updateComposer(gtx layout.Context) {
	cs := &c.comp
	c.updateReply(gtx)
	if c.focusComposer {
		gtx.Execute(key.FocusCmd{Tag: &c.composer})
		c.focusComposer = false
	}
	c.updateVoiceRecording(gtx)
	if cs.attachBtn.Clicked(gtx) {
		c.attachFiles()
	}
	for tab, btn := range map[int]*widget.Clickable{tabEmoji: &cs.emojiBtn, tabStickers: &cs.stickerBtn, tabGIFs: &cs.gifBtn} {
		if !btn.Clicked(gtx) {
			continue
		}
		switch {
		case cs.emoji == nil:
			cs.emoji = newEmojiPicker()
			cs.emoji.tab = tab
			cs.mentionOn = false
		case cs.emoji.tab != tab:
			cs.emoji.tab = tab
		default:
			cs.emoji = nil
		}
	}
	for i := 0; i < len(cs.files); i++ {
		if cs.files[i].remove.Clicked(gtx) {
			cs.files = slices.Delete(cs.files, i, i+1)
			i--
		}
	}

	// While the list of mentions shows, the arrows, Enter, Tab and Escape
	// work it instead of the box.
	if cs.mentionOn && len(cs.mentionItems) > 0 {
		for {
			ev, ok := gtx.Event(
				key.Filter{Focus: &c.composer, Name: key.NameUpArrow},
				key.Filter{Focus: &c.composer, Name: key.NameDownArrow},
				key.Filter{Focus: &c.composer, Name: key.NameReturn},
				key.Filter{Focus: &c.composer, Name: key.NameEnter},
				key.Filter{Focus: &c.composer, Name: key.NameTab},
				key.Filter{Focus: &c.composer, Name: key.NameEscape},
			)
			if !ok {
				break
			}
			e, ok := ev.(key.Event)
			if !ok || e.State != key.Press {
				continue
			}
			switch e.Name {
			case key.NameUpArrow:
				cs.mentionSel = (cs.mentionSel - 1 + len(cs.mentionItems)) % len(cs.mentionItems)
				cs.mentionList.ScrollTo(cs.mentionSel)
			case key.NameDownArrow:
				cs.mentionSel = (cs.mentionSel + 1) % len(cs.mentionItems)
				cs.mentionList.ScrollTo(cs.mentionSel)
			case key.NameReturn, key.NameEnter, key.NameTab:
				c.pickMention(cs.mentionItems[cs.mentionSel])
			case key.NameEscape:
				cs.mentionOn = false
				cs.dismissed = fmt.Sprint(cs.mentionStart, cs.mentionQuery)
			}
		}
		for i := range cs.mentionClicks {
			if i < len(cs.mentionItems) && cs.mentionClicks[i].Clicked(gtx) {
				c.pickMention(cs.mentionItems[i])
				break
			}
		}
	}

	// Escape stops replying, and the up arrow in an empty box edits the
	// newest message of the user, as in Discord. Both are asked for before
	// the box reads the keys.
	if !cs.mentionOn || len(cs.mentionItems) == 0 {
		if c.act.replyTo != nil {
			for {
				ev, ok := gtx.Event(key.Filter{Focus: &c.composer, Name: key.NameEscape})
				if !ok {
					break
				}
				if e, ok := ev.(key.Event); ok && e.State == key.Press {
					c.act.replyTo = nil
				}
			}
		}
		if c.composer.Len() == 0 {
			for {
				ev, ok := gtx.Event(key.Filter{Focus: &c.composer, Name: key.NameUpArrow})
				if !ok {
					break
				}
				if e, ok := ev.(key.Event); ok && e.State == key.Press {
					c.editLast()
				}
			}
		}
	}

	// Ctrl+V is asked for before the box reads the keys, so that files and
	// pictures can be attached; text is handed back to the box.
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &c.composer, Name: "V", Required: key.ModShortcut})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			c.paste(gtx)
		}
	}

	for {
		ev, ok := c.composer.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.SubmitEvent:
			c.send()
		case widget.ChangeEvent:
			c.sendTyping()
		}
	}
	c.updateMentions()
}

// updateMentions shows the list of mentions while the word before the caret
// starts with @, with the people and roles that match what follows it.
func (c *chatView) updateMentions() {
	cs := &c.comp
	text := []rune(c.composer.Text())
	_, caret := c.composer.Selection()
	caret = min(caret, len(text))
	i := caret - 1
	for i >= 0 && !unicode.IsSpace(text[i]) && text[i] != '@' {
		i--
	}
	if i < 0 || text[i] != '@' || (i > 0 && !unicode.IsSpace(text[i-1])) || c.selChannel == nil {
		cs.mentionOn = false
		return
	}
	query := string(text[i+1 : caret])
	if cs.mentionOn && cs.mentionStart == i && cs.mentionQuery == query {
		return
	}
	if cs.dismissed == fmt.Sprint(i, query) {
		return
	}
	cs.dismissed = ""
	cs.mentionOn, cs.mentionStart, cs.mentionQuery, cs.mentionSel = true, i, query, 0
	c.rebuildMentions()
	// Big servers have members that are not loaded yet: ask Discord, whose
	// answer rebuilds the list when it arrives.
	if guildID := c.selChannel.GuildID; guildID.IsValid() && query != "" && query != cs.searched && !c.demo {
		cs.searched = query
		go c.st.MemberState.SearchMember(guildID, query)
	}
}

func (c *chatView) rebuildMentions() {
	cs := &c.comp
	if !cs.mentionOn {
		return
	}
	q := strings.ToLower(cs.mentionQuery)
	matches := func(s ...string) int {
		best := -1
		for _, x := range s {
			x = strings.ToLower(x)
			switch {
			case x == "":
			case strings.HasPrefix(x, q):
				return 0
			case strings.Contains(x, q):
				best = 1
			}
		}
		return best
	}

	type ranked struct {
		item mentionItem
		rank int
	}
	var people, roles []ranked
	ch := c.selChannel
	guildID := ch.GuildID
	addUser := func(u discord.User, member *discord.Member) {
		name := u.DisplayOrUsername()
		if member != nil && member.Nick != "" {
			name = member.Nick
		}
		r := matches(name, u.Username, u.DisplayName)
		if r < 0 || u.ID == c.me.ID && q == "" {
			return
		}
		col := colorHeader
		if guildID.IsValid() {
			if mc, ok := c.memberColor(guildID, u.ID); ok && mc != 0 && mc != discord.NullColor {
				col = rgb(uint32(mc))
			}
		}
		user := u
		people = append(people, ranked{mentionItem{name: name, sub: u.Username, color: col, user: &user, markup: "<@" + u.ID.String() + ">"}, r})
	}
	if guildID.IsValid() {
		members, _ := c.st.Cabinet.Members(guildID)
		for i := range members {
			addUser(members[i].User, &members[i])
		}
		canEveryone := c.can(ch.ID, discord.PermissionMentionEveryone)
		guildRoles, _ := c.st.Cabinet.Roles(guildID)
		for _, r := range guildRoles {
			if discord.Snowflake(r.ID) == discord.Snowflake(guildID) || !r.Mentionable && !canEveryone {
				continue
			}
			if rank := matches(r.Name); rank >= 0 {
				col := colorMention
				if r.Color != 0 {
					col = rgb(uint32(r.Color))
				}
				roles = append(roles, ranked{mentionItem{name: r.Name, color: col, role: true, markup: "<@&" + r.ID.String() + ">"}, rank})
			}
		}
		if canEveryone {
			for _, s := range []struct{ name, sub string }{{"everyone", "Notifies everyone who can access this channel."}, {"here", "Notifies everyone online who can access this channel."}} {
				if rank := matches(s.name); rank >= 0 {
					roles = append(roles, ranked{mentionItem{name: s.name, sub: s.sub, color: colorMention, role: true, markup: "@" + s.name}, rank})
				}
			}
		}
	} else {
		for _, u := range ch.DMRecipients {
			addUser(u, nil)
		}
	}
	sortRanked := func(rs []ranked) {
		slices.SortFunc(rs, func(a, b ranked) int {
			return cmp.Or(cmp.Compare(a.rank, b.rank), strings.Compare(strings.ToLower(a.item.name), strings.ToLower(b.item.name)))
		})
	}
	sortRanked(people)
	sortRanked(roles)
	cs.mentionItems = cs.mentionItems[:0]
	for i, r := range people {
		if i == 8 {
			break
		}
		cs.mentionItems = append(cs.mentionItems, r.item)
	}
	for i, r := range roles {
		if i == 6 {
			break
		}
		cs.mentionItems = append(cs.mentionItems, r.item)
	}
	cs.mentionSel = min(cs.mentionSel, max(len(cs.mentionItems)-1, 0))
	if len(cs.mentionClicks) < len(cs.mentionItems) {
		cs.mentionClicks = make([]widget.Clickable, len(cs.mentionItems))
	}
}

// pickMention puts the mention in place of the @ being typed.
func (c *chatView) pickMention(it mentionItem) {
	cs := &c.comp
	text := []rune(c.composer.Text())
	_, caret := c.composer.Selection()
	caret = min(caret, len(text))
	shown := "@" + it.name
	if cs.mentions == nil {
		cs.mentions = make(map[string]string)
	}
	cs.mentions[shown] = it.markup
	out := string(text[:cs.mentionStart]) + shown + " " + string(text[caret:])
	pos := cs.mentionStart + utf8.RuneCountInString(shown) + 1
	c.composer.SetText(out)
	c.composer.SetCaret(pos, pos)
	cs.mentionOn = false
	gtxFocus(c)
}

// insertText puts s at the caret of the box, as the emoji picker does.
func (c *chatView) insertText(s string) {
	c.composer.Insert(s)
	gtxFocus(c)
}

// withMarkup turns the @names that came from the list of mentions into the
// markup that Discord expects.
func (c *chatView) withMarkup(text string) string {
	names := make([]string, 0, len(c.comp.mentions))
	for name := range c.comp.mentions {
		names = append(names, name)
	}
	// Longer names first, so that @Ana Maria is not taken as @Ana.
	slices.SortFunc(names, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	for _, name := range names {
		text = strings.ReplaceAll(text, name, c.comp.mentions[name])
	}
	return text
}

func (c *chatView) layoutComposer(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	cs := &c.comp
	canSend := !c.selChannel.GuildID.IsValid() || c.can(c.selChannel.ID, discord.PermissionSendMessages)
	dims := layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !canSend {
					return layout.Dimensions{}
				}
				return c.layoutReplyBar(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !canSend {
					return rounded(gtx, colorInput, 8, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.UniformInset(unit.Dp(14)).Layout(gtx,
							label(th, 15, colorMuted, "You do not have permission to send messages in this channel.").Layout)
					})
				}
				return rounded(gtx, colorInput, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(c.layoutPendingFiles),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return iconButton(gtx, &cs.attachBtn, iconAdd, colorMuted, colorHeader)
									}),
									layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
									layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
										// A voice message records in place of the text.
										if cs.voice.rec != nil || cs.voice.sending {
											return c.layoutVoiceRecording(gtx)
										}
										return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
											// Long messages grow the box up to a limit, then scroll.
											gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(200))
											gtx.Constraints.Min.X = gtx.Constraints.Max.X
											hint := "Message #" + c.selChannel.Name
											if !c.selChannel.GuildID.IsValid() {
												hint = "Message @" + c.dmName(*c.selChannel)
											}
											e := material.Editor(th, &c.composer, hint)
											e.HintColor = colorMuted
											e.Color = colorText
											e.TextSize = 15
											e.SelectionColor = withAlpha(colorAccent, 0x60)
											// The mentions picked show in their box, as once sent,
											// drawn under the text.
											macro := op.Record(gtx.Ops)
											d := e.Layout(gtx)
											call := macro.Stop()
											c.paintComposerMentions(gtx, d.Size)
											call.Add(gtx.Ops)
											return d
										})
									}),
									// With nothing written, a voice message can be
									// recorded, as on Discord's phones.
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										if cs.voice.rec != nil || cs.voice.sending || c.composer.Len() > 0 || len(cs.files) > 0 {
											return layout.Dimensions{}
										}
										return iconButton(gtx, &cs.voice.mic, iconMic, colorMuted, colorHeader)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return pickerButton(gtx, &cs.gifBtn, cs.emoji != nil && cs.emoji.tab == tabGIFs, c.gifGlyph)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return pickerButton(gtx, &cs.stickerBtn, cs.emoji != nil && cs.emoji.tab == tabStickers, stickerGlyph)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										return pickerButton(gtx, &cs.emojiBtn, cs.emoji != nil && cs.emoji.tab == tabEmoji, emojiGlyph)
									}),
								)
							})
						}),
					)
				})
			}),
			// The line below the box says who is typing, or why sending failed.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				txt, col := c.typingText(), colorText
				if c.act.notice != "" && time.Now().Before(c.act.noticeUntil) {
					txt, col = c.act.notice, colorGreen
				}
				if cs.sending {
					txt, col = "Uploading files…", colorMuted
				}
				if c.sendErr != "" {
					txt, col = c.sendErr, colorRed
				}
				h := gtx.Dp(24)
				gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
				return vcenter(gtx, oneLine(label(th, 12, col, txt)).Layout)
			}),
		)
	})
	cs.boxH = dims.Size.Y
	return dims
}

// layoutPendingFiles shows the files to be sent as cards above the text.
func (c *chatView) layoutPendingFiles(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	cs := &c.comp
	if len(cs.files) == 0 {
		return layout.Dimensions{}
	}
	var cards []layout.FlexChild
	for _, f := range cs.files {
		cards = append(cards, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = gtx.Dp(220)
				return rounded(gtx, colorSidebar, 6, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconFile, 28, colorAccent) }),
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(oneLine(label(th, 13, colorHeader, f.name)).Layout),
									layout.Rigid(label(th, 11, colorMuted, formatSize(uint64(f.size))).Layout),
								)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return iconButton(gtx, &f.remove, iconClose, colorMuted, colorRed)
							}),
						)
					})
				})
			})
		}))
	}
	return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(12), Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := wrapLayout(gtx, cards)
		dims.Size.X = gtx.Constraints.Max.X
		// A line separates the files from the text.
		paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Min: image.Pt(0, dims.Size.Y-1), Max: dims.Size}.Op())
		return dims
	})
}

// layoutMentions is the list of people and roles that match the @ being
// typed, drawn above the box.
func (c *chatView) layoutMentions(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	cs := &c.comp
	cs.mentionList.Axis = layout.Vertical
	// As tall as its items, up to eight of them.
	rowH := gtx.Dp(36)
	gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, rowH*min(len(cs.mentionItems), 8)+gtx.Dp(44))
	return rounded(gtx, colorPanel, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			title := "MEMBERS"
			if len(cs.mentionItems) > 0 && cs.mentionItems[0].role {
				title = "ROLES"
			}
			if cs.mentionQuery != "" {
				title += " MATCHING @" + strings.ToUpper(cs.mentionQuery)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8), Bottom: unit.Dp(6), Top: unit.Dp(2)}.Layout(gtx, oneLine(bold(label(th, 11, colorMuted, title))).Layout)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return listLayout(gtx, th, &cs.mentionList, len(cs.mentionItems), func(gtx layout.Context, i int) layout.Dimensions {
						it := cs.mentionItems[i]
						return rowItem(gtx, &cs.mentionClicks[i], func(gtx layout.Context, hovered bool) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if it.user != nil {
										return c.u.picture(gtx, c.authorAvatarURL(c.selChannel.GuildID, *it.user), 24, colorFor(uint64(it.user.ID)), it.name, -1)
									}
									gtx.Constraints.Min.X = gtx.Dp(24)
									return label(th, 18, it.color, "@").Layout(gtx)
								}),
								layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
								layout.Rigid(oneLine(label(th, 15, it.color, it.name)).Layout),
								layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
								layout.Flexed(1, oneLine(label(th, 13, colorMuted, it.sub)).Layout),
							)
						}, i == cs.mentionSel)
					})
				}),
			)
		})
	})
}

func (c *chatView) send() {
	if c.selChannel == nil {
		return
	}
	cs := &c.comp
	text := strings.TrimSpace(c.composer.Text())
	if text == "" && len(cs.files) == 0 {
		return
	}
	if n := utf8.RuneCountInString(text); n > 2000 {
		c.sendErr = fmt.Sprintf("The message is too long (%d of 2000 characters).", n)
		return
	}
	content := c.withMarkup(text)
	files := cs.files
	reply, ping := c.act.replyTo, c.act.replyPing
	c.act.replyTo = nil
	c.composer.SetText("")
	cs.files, cs.mentions, cs.mentionOn = nil, nil, false
	c.sendErr = ""
	c.typingUntil = time.Time{}
	// Show the newest messages, where this one lands.
	c.messageList.Position = layout.Position{}
	c.jumpToPresent()

	id := c.selChannel.ID
	if c.demo {
		c.messages = append(c.messages, discord.Message{
			ID: discord.MessageID(discord.NewSnowflake(time.Now())), ChannelID: id, GuildID: c.selChannel.GuildID,
			Author: c.me, Content: content, Timestamp: discord.NewTimestamp(time.Now()),
			ReferencedMessage: reply,
		})
		return
	}
	cs.sending = len(files) > 0
	go func() {
		data := api.SendMessageData{
			Content: content,
			Nonce:   strconv.FormatInt(time.Now().UnixNano(), 10),
		}
		if reply != nil {
			data.Reference = &discord.MessageReference{MessageID: reply.ID, ChannelID: id, GuildID: reply.GuildID}
			if !ping {
				// Everything else in the message still mentions as usual.
				data.AllowedMentions = &api.AllowedMentions{
					Parse:       []api.AllowedMentionType{api.AllowUserMention, api.AllowRoleMention, api.AllowEveryoneMention},
					RepliedUser: option.Some(false),
				}
			}
		}
		var opened []*os.File
		defer func() {
			for _, f := range opened {
				f.Close()
			}
		}()
		for _, f := range files {
			file, err := os.Open(f.path)
			if err != nil {
				c.u.post(func() { c.sendErr = "Could not open \"" + f.name + "\": " + err.Error() })
				return
			}
			opened = append(opened, file)
			data.Files = append(data.Files, sendpart.File{Name: f.name, Reader: file})
		}
		_, err := c.st.SendMessageComplex(id, data)
		c.u.post(func() {
			cs.sending = false
			if err == nil || c.selChannel == nil || c.selChannel.ID != id {
				return
			}
			c.sendErr = "Could not send the message: " + err.Error()
			if c.composer.Text() == "" {
				c.composer.SetText(text)
			}
			if len(cs.files) == 0 {
				cs.files = files
			}
			if c.act.replyTo == nil {
				c.act.replyTo = reply
			}
		})
	}()
}

// sendTyping tells the others that the user is typing, at most every 8
// seconds as Discord expects.
func (c *chatView) sendTyping() {
	if c.selChannel == nil || c.composer.Text() == "" || c.u.app.GhostTyping {
		return
	}
	now := time.Now()
	if now.Before(c.typingUntil) {
		return
	}
	c.typingUntil = now.Add(8 * time.Second)
	if c.demo {
		return
	}
	id := c.selChannel.ID
	go c.st.Typing(id)
}

func (c *chatView) typingText() string {
	now := time.Now()
	var names []string
	for id, until := range c.typers {
		if now.After(until) {
			delete(c.typers, id)
			continue
		}
		names = append(names, c.memberName(c.selChannel.GuildID, id, nil))
	}
	slices.Sort(names)
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0] + " is typing…"
	case 2:
		return names[0] + " and " + names[1] + " are typing…"
	case 3:
		return names[0] + ", " + names[1] + " and " + names[2] + " are typing…"
	default:
		return "Several people are typing…"
	}
}

// layoutComposerPopups draws the list of mentions and the emoji picker above
// the box, over the messages.
func (c *chatView) layoutComposerPopups(gtx layout.Context) {
	cs := &c.comp
	if c.selChannel == nil {
		return
	}
	size := gtx.Constraints.Max
	bottom := size.Y - cs.boxH + gtx.Dp(4)
	if cs.mentionOn && len(cs.mentionItems) > 0 {
		pgtx := gtx
		w := cs.paneW - gtx.Dp(32)
		pgtx.Constraints = layout.Constraints{Max: image.Pt(w, bottom-gtx.Dp(60))}
		drawAbove(gtx, pgtx, image.Pt(cs.paneX+gtx.Dp(16), bottom-gtx.Dp(8)), &cs.mentionList, c.layoutMentions)
	}
	if cs.emoji != nil {
		// Clicking anywhere else closes the picker.
		if cs.emojiBackdrop.Clicked(gtx) {
			cs.emoji = nil
			return
		}
		cs.emojiBackdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: size}
		})
		w, h := min(gtx.Dp(460), cs.paneW-gtx.Dp(32)), min(gtx.Dp(480), bottom-gtx.Dp(60))
		pgtx := gtx
		pgtx.Constraints = layout.Exact(image.Pt(w, h))
		drawAbove(gtx, pgtx, image.Pt(cs.paneX+cs.paneW-gtx.Dp(16)-w, bottom-gtx.Dp(8)), cs.emoji, c.layoutEmojiPicker)
	}
}

// drawAbove lays out w with the constraints of wgtx so that its bottom left
// corner is at p, taking the clicks on it so that they do not reach what is
// below.
func drawAbove(gtx, wgtx layout.Context, p image.Point, tag event.Tag, w layout.Widget) {
	macro := op.Record(gtx.Ops)
	dims := w(wgtx)
	call := macro.Stop()
	t := op.Offset(image.Pt(p.X, p.Y-dims.Size.Y)).Push(gtx.Ops)
	solid(gtx, tag, dims)
	call.Add(gtx.Ops)
	t.Pop()
}
