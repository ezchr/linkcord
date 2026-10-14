package gui

import (
	"cmp"
	"fmt"
	"image"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconSearch     = mustIcon(icons.ActionSearch)
	iconFilterUser = mustIcon(icons.SocialPerson)
	iconFilterHas  = mustIcon(icons.EditorAttachFile)
	iconFilterAt   = mustIcon(icons.CommunicationContactMail)
)

// searchFilters are the filters the search offers, as Discord's.
var searchFilters = []struct {
	key, title, hint string
	ic               **widget.Icon
}{
	{"from:", "From a specific user", "from: user", &iconFilterUser},
	{"in:", "Sent in a specific channel", "in: channel", nil},
	{"has:", "Includes a specific type of data", "has: link, embed, file, image, video or sound", &iconFilterHas},
	{"mentions:", "Mentions a specific user", "mentions: user", &iconFilterAt},
}

// searchSuggestion is a user, channel or kind that a filter being typed
// could take.
type searchSuggestion struct {
	value      string // what goes after the filter's colon
	title, sub string
	user       *discord.User
	channel    bool
}

// typedFilter is the filter the last word typed starts with, and what
// follows its colon, or "" when the last word is not a filter.
func typedFilter(text string) (key, partial string) {
	if text == "" || strings.HasSuffix(text, " ") {
		return "", ""
	}
	words := strings.Fields(text)
	last := words[len(words)-1]
	k, v, ok := strings.Cut(last, ":")
	if !ok {
		return "", ""
	}
	switch strings.ToLower(k) {
	case "from", "de":
		return "from", v
	case "mentions", "menciones":
		return "mentions", v
	case "in", "en":
		return "in", v
	case "has", "tiene":
		return "has", v
	}
	return "", ""
}

// searchSuggestions are what the filter being typed could take, the
// closest first, a few at most.
func (c *chatView) searchSuggestions(key, partial string) []searchSuggestion {
	q := strings.ToLower(partial)
	const most = 8
	var out []searchSuggestion
	switch key {
	case "from", "mentions":
		var users []discord.User
		nicks := map[discord.UserID]string{}
		if c.selGuild.IsValid() {
			members, _ := c.st.Cabinet.Members(c.selGuild)
			for _, m := range members {
				users = append(users, m.User)
				nicks[m.User.ID] = m.Nick
			}
		} else if c.selChannel != nil {
			users = append(slices.Clone(c.selChannel.DMRecipients), c.me)
		}
		// Those whose name starts with what was typed go first.
		var first, rest []searchSuggestion
		for _, u := range users {
			name := cmpOr(nicks[u.ID], u.DisplayOrUsername())
			names := strings.ToLower(u.Username + " " + u.DisplayName + " " + nicks[u.ID])
			if q != "" && !strings.Contains(names, q) {
				continue
			}
			u := u
			sg := searchSuggestion{value: u.Username, title: name, sub: u.Username, user: &u}
			if strings.HasPrefix(strings.ToLower(u.Username), q) || strings.HasPrefix(strings.ToLower(name), q) {
				first = append(first, sg)
			} else {
				rest = append(rest, sg)
			}
		}
		// By name, then username, then ID, for those named alike to keep
		// their place from frame to frame: the members come in no order.
		byName := func(a, b searchSuggestion) int {
			if n := strings.Compare(strings.ToLower(a.title), strings.ToLower(b.title)); n != 0 {
				return n
			}
			if n := strings.Compare(a.sub, b.sub); n != 0 {
				return n
			}
			return cmp.Compare(a.user.ID, b.user.ID)
		}
		slices.SortFunc(first, byName)
		slices.SortFunc(rest, byName)
		out = append(first, rest...)
	case "in":
		channels, _ := c.st.Cabinet.Channels(c.selGuild)
		slices.SortFunc(channels, func(a, b discord.Channel) int { return cmp.Or(a.Position-b.Position, cmp.Compare(a.ID, b.ID)) })
		for _, ch := range channels {
			if !isTextual(ch.Type) || ch.Type == discord.GuildPublicThread || ch.Type == discord.GuildPrivateThread || ch.Type == discord.GuildAnnouncementThread {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(ch.Name), q) || !c.can(ch.ID, discord.PermissionViewChannel) {
				continue
			}
			sub := ""
			if parent, err := c.st.Cabinet.Channel(ch.ParentID); err == nil {
				sub = parent.Name
			}
			out = append(out, searchSuggestion{value: ch.Name, title: ch.Name, sub: sub, channel: true})
		}
	case "has":
		for _, h := range []string{"link", "embed", "file", "image", "video", "sound", "sticker", "poll"} {
			if strings.HasPrefix(h, q) {
				out = append(out, searchSuggestion{value: h, title: h})
			}
		}
	}
	return out[:min(len(out), most)]
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// searchHas are the words has: takes, in English and Spanish, as Discord's
// search names them.
var searchHas = map[string]string{
	"link": "link", "enlace": "link", "embed": "embed", "integrado": "embed",
	"file": "file", "archivo": "file", "image": "image", "imagen": "image",
	"video": "video", "sound": "sound", "sonido": "sound", "sticker": "sticker", "poll": "poll", "encuesta": "poll",
}

// searchView is the search of the messages of a server or conversation.
type searchView struct {
	ed      widget.Editor
	focused bool
	// dismiss takes the presses outside the list while it shows.
	dismiss bool
	// suggestions are what the filter being typed could be, as last shown,
	// and the words each puts in its place.
	suggestions []searchSuggestion
	suggestBtns []widget.Clickable
	// open reports whether the results show in place of the members.
	open    bool
	scope   discord.Snowflake // the server, or the conversation, searched
	inGuild bool
	query   string
	results []discord.Message
	total   int
	loading bool
	err     string
	epoch   int
	list    widget.List

	filterBtns          [4]widget.Clickable
	historyBtns         []widget.Clickable
	clearHistory, close widget.Clickable
	more                widget.Clickable
	clicks              map[discord.MessageID]*widget.Clickable

	// jump is the result to show once its channel has loaded.
	jump     discord.MessageID
	jumpChan discord.ChannelID
}

// searchResponse is what Discord's search answers; code 110000 says the
// messages are still being indexed, to ask again after retry_after.
type searchResponse struct {
	TotalResults int                 `json:"total_results"`
	Messages     [][]discord.Message `json:"messages"`
	Code         int                 `json:"code"`
	RetryAfter   float64             `json:"retry_after"`
}

// layoutSearchBox is the search field of the header.
func (c *chatView) layoutSearchBox(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	s := &c.search
	s.ed.SingleLine, s.ed.Submit = true, true
	for {
		e, ok := s.ed.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			c.runSearch(strings.TrimSpace(s.ed.Text()), 0)
			gtx.Execute(key.FocusCmd{})
		}
	}
	for {
		e, ok := gtx.Event(key.Filter{Focus: &s.ed, Name: key.NameEscape})
		if !ok {
			break
		}
		if ev, ok := e.(key.Event); ok && ev.State == key.Press {
			gtx.Execute(key.FocusCmd{})
		}
	}
	s.focused = gtx.Focused(&s.ed)
	hint := "Search"
	if c.selGuild.IsValid() {
		if g, err := c.st.Cabinet.Guild(c.selGuild); err == nil {
			hint = "Search " + g.Name
		}
	}
	w := gtx.Dp(220)
	if s.focused {
		w = gtx.Dp(300)
	}
	// What is left beside the title, and the members' button after it.
	w = max(min(w, gtx.Constraints.Max.X-gtx.Dp(44)), 0)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
	return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return rounded(gtx, colorServers, 6, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(6), Top: unit.Dp(5), Bottom: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						e := material.Editor(th, &s.ed, tr(hint))
						e.TextSize, e.Color, e.HintColor = 14, colorText, colorMuted
						return e.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconSearch, 18, colorMuted) }),
				)
			})
		})
	})
}

// runSearch searches the open server, or conversation, for query, from
// offset of its results.
func (c *chatView) runSearch(query string, offset int) {
	s := &c.search
	if query == "" || c.selChannel == nil {
		return
	}
	if offset == 0 {
		c.addSearchHistory(query)
		s.results, s.total = nil, 0
		s.list.Position = layout.Position{}
	}
	s.open, s.query, s.err, s.loading = true, query, "", true
	s.epoch++
	epoch := s.epoch
	s.inGuild = c.selGuild.IsValid()
	s.scope = discord.Snowflake(c.selChannel.ID)
	if s.inGuild {
		s.scope = discord.Snowflake(c.selGuild)
	}
	if c.demo {
		s.loading = false
		s.results = c.demoSearch(query)
		s.total = len(s.results)
		return
	}
	params := c.searchParams(query)
	params.Set("offset", strconv.Itoa(offset))
	endpoint := api.EndpointChannels + s.scope.String() + "/messages/search?"
	if s.inGuild {
		endpoint = api.EndpointGuilds + s.scope.String() + "/messages/search?"
	}
	endpoint += params.Encode()
	go func() {
		var resp searchResponse
		var err error
		// While Discord indexes the messages, it asks to wait.
		for try := 0; try < 4; try++ {
			resp, err = c.st.Client.RequestJSON[searchResponse]("GET", endpoint)
			if err != nil || resp.Code != 110000 {
				break
			}
			time.Sleep(time.Duration(max(resp.RetryAfter, 1)*1000) * time.Millisecond)
		}
		c.u.post(func() {
			if s.epoch != epoch {
				return
			}
			s.loading = false
			if err != nil {
				s.err = "Discord did not search: " + err.Error()
				slog.Warn("search failed", "err", err)
				return
			}
			s.total = resp.TotalResults
			for _, group := range resp.Messages {
				// Each result comes with the messages around it; the one
				// found is marked as hit, or is the only one.
				if len(group) > 0 {
					s.results = append(s.results, group[len(group)/2])
				}
			}
			c.requestAuthors(s.results)
		})
	}()
}

// searchParams turns the words typed into the search's parameters: from:,
// in:, has: and mentions: take a user, a channel or a kind, and the rest is
// the text to find.
func (c *chatView) searchParams(query string) url.Values {
	v := url.Values{}
	var text []string
	words := strings.Fields(query)
	for i := 0; i < len(words); i++ {
		w := words[i]
		key, value, ok := strings.Cut(w, ":")
		if !ok || !slices.Contains([]string{"from", "in", "has", "mentions", "de", "en", "tiene", "menciones"}, strings.ToLower(key)) {
			text = append(text, w)
			continue
		}
		// "from: name" as well as "from:name".
		if value == "" && i+1 < len(words) {
			i++
			value = words[i]
		}
		switch strings.ToLower(key) {
		case "from", "de":
			if id := c.findUser(value); id.IsValid() {
				v.Add("author_id", id.String())
			}
		case "mentions", "menciones":
			if id := c.findUser(value); id.IsValid() {
				v.Add("mentions", id.String())
			}
		case "in", "en":
			if id := c.findChannel(value); id.IsValid() {
				v.Add("channel_id", id.String())
			}
		case "has", "tiene":
			if h, ok := searchHas[strings.ToLower(strings.Trim(value, ","))]; ok {
				v.Add("has", h)
			}
		}
	}
	if len(text) > 0 {
		v.Set("content", strings.Join(text, " "))
	}
	return v
}

// findUser is the member, or person of the conversation, called name.
func (c *chatView) findUser(name string) discord.UserID {
	name = strings.ToLower(strings.TrimPrefix(name, "@"))
	match := func(u discord.User, nick string) bool {
		return strings.ToLower(u.Username) == name || strings.ToLower(u.DisplayName) == name || strings.ToLower(nick) == name
	}
	if id, err := strconv.ParseUint(name, 10, 64); err == nil {
		return discord.UserID(id)
	}
	if c.selGuild.IsValid() {
		members, _ := c.st.Cabinet.Members(c.selGuild)
		for _, m := range members {
			if match(m.User, m.Nick) {
				return m.User.ID
			}
		}
	} else if c.selChannel != nil {
		for _, u := range append(c.selChannel.DMRecipients, c.me) {
			if match(u, "") {
				return u.ID
			}
		}
	}
	return 0
}

// findChannel is the server's channel called name.
func (c *chatView) findChannel(name string) discord.ChannelID {
	name = strings.ToLower(strings.TrimPrefix(name, "#"))
	channels, _ := c.st.Cabinet.Channels(c.selGuild)
	for _, ch := range channels {
		if strings.ToLower(ch.Name) == name {
			return ch.ID
		}
	}
	for _, ch := range channels {
		if strings.Contains(strings.ToLower(ch.Name), name) {
			return ch.ID
		}
	}
	return 0
}

// demoSearch finds the words among the open channel's messages.
func (c *chatView) demoSearch(query string) []discord.Message {
	content := c.searchParams(query).Get("content")
	var out []discord.Message
	for i := len(c.messages) - 1; i >= 0; i-- {
		m := c.messages[i]
		if strings.Contains(strings.ToLower(m.Content), strings.ToLower(content)) {
			m.ChannelID = c.selChannel.ID
			out = append(out, m)
		}
	}
	return out
}

// searchMarkup is the markdown left out of a result's text.
var searchMarkup = strings.NewReplacer("**", "", "__", "", "~~", "", "||", "", "`", "")

func (c *chatView) addSearchHistory(query string) {
	a := &c.u.app
	a.SearchHistory = slices.DeleteFunc(a.SearchHistory, func(q string) bool { return q == query })
	a.SearchHistory = append([]string{query}, a.SearchHistory...)
	a.SearchHistory = a.SearchHistory[:min(len(a.SearchHistory), 8)]
	c.u.saveAppSettings()
}

// updateSearch handles the clicks of the filters, the history and the
// results, and shows a result once its channel has loaded.
func (c *chatView) updateSearch(gtx layout.Context) {
	s := &c.search
	// The results are of the server, or conversation, they were found in.
	if s.open && c.selChannel != nil {
		if s.inGuild && s.scope != discord.Snowflake(c.selGuild) || !s.inGuild && !c.selGuild.IsValid() && s.scope != discord.Snowflake(c.selChannel.ID) {
			s.open = false
			s.epoch++
		}
	}
	for i := range s.filterBtns {
		if s.filterBtns[i].Clicked(gtx) {
			text := strings.TrimSpace(s.ed.Text())
			if text != "" {
				text += " "
			}
			s.ed.SetText(text + searchFilters[i].key)
			s.ed.SetCaret(s.ed.Len(), s.ed.Len())
			gtx.Execute(key.FocusCmd{Tag: &s.ed})
		}
	}
	for i := range s.suggestBtns {
		if i < len(s.suggestions) && s.suggestBtns[i].Clicked(gtx) {
			text := s.ed.Text()
			k, partial := typedFilter(text)
			if k == "" {
				continue
			}
			text = strings.TrimSuffix(text, partial) + s.suggestions[i].value + " "
			s.ed.SetText(text)
			s.ed.SetCaret(s.ed.Len(), s.ed.Len())
			gtx.Execute(key.FocusCmd{Tag: &s.ed})
		}
	}
	for i := range s.historyBtns {
		if i < len(c.u.app.SearchHistory) && s.historyBtns[i].Clicked(gtx) {
			q := c.u.app.SearchHistory[i]
			s.ed.SetText(q)
			c.runSearch(q, 0)
			gtx.Execute(key.FocusCmd{})
		}
	}
	if s.clearHistory.Clicked(gtx) {
		c.u.app.SearchHistory = nil
		c.u.saveAppSettings()
	}
	if s.close.Clicked(gtx) {
		s.open = false
		s.epoch++
	}
	if s.more.Clicked(gtx) && !s.loading {
		c.runSearch(s.query, len(s.results))
	}
	for id, click := range s.clicks {
		if !click.Clicked(gtx) {
			continue
		}
		for _, m := range s.results {
			if m.ID == id {
				c.openSearchResult(m)
			}
		}
	}
	if s.jump.IsValid() && c.selChannel != nil && c.selChannel.ID == s.jumpChan && !c.loading {
		id := s.jump
		s.jump = 0
		c.jumpToMessage(id)
	}
}

// openSearchResult shows the message found in its channel.
func (c *chatView) openSearchResult(m discord.Message) {
	if c.selChannel != nil && c.selChannel.ID == m.ChannelID {
		c.jumpToMessage(m.ID)
		return
	}
	ch, err := c.st.Cabinet.Channel(m.ChannelID)
	if err != nil {
		return
	}
	c.selectChannel(*ch)
	c.search.jump, c.search.jumpChan = m.ID, m.ChannelID
}

// layoutSearchMenu is the list of filters and past searches under the
// search field while it is being typed in.
func (c *chatView) layoutSearchMenu(gtx layout.Context) {
	th := c.u.th
	s := &c.search
	if !s.focused || c.selChannel == nil {
		return
	}
	// A press anywhere else, below the header where the field is, lets go
	// of the field and closes the list, as Discord's.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &s.dismiss, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Position.Y > float32(gtx.Dp(48)) {
			gtx.Execute(key.FocusCmd{})
			s.focused = false
			return
		}
	}
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, &s.dismiss)
	pass.Pop()
	area.Pop()
	w := gtx.Dp(320)
	x := c.comp.paneX + c.comp.paneW - w - gtx.Dp(16)
	history := c.u.app.SearchHistory
	if len(s.historyBtns) < len(history) {
		s.historyBtns = make([]widget.Clickable, len(history))
	}
	row := func(click *widget.Clickable, lead layout.Widget, title, sub string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rowItem(gtx, click, func(gtx layout.Context, hovered bool) layout.Dimensions {
				return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Dp(24)
							return lead(gtx)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							if sub == "" {
								return oneLine(label(th, 14, colorHeader, title)).Layout(gtx)
							}
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(oneLine(bold(label(th, 14, colorHeader, title))).Layout),
								layout.Rigid(oneLine(label(th, 12, colorMuted, sub)).Layout),
							)
						}),
					)
				})
			}, false)
		})
	}
	var children []layout.FlexChild
	if k, partial := typedFilter(s.ed.Text()); k != "" {
		s.suggestions = c.searchSuggestions(k, partial)
		if len(s.suggestBtns) < len(s.suggestions) {
			s.suggestBtns = make([]widget.Clickable, len(s.suggestions))
		}
		heading := map[string]string{"from": "From User", "mentions": "Mentions User", "in": "In Channel", "has": "Has"}[k]
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6), Bottom: unit.Dp(4)}.Layout(gtx, bold(label(th, 12, colorMuted, heading)).Layout)
		}))
		if len(s.suggestions) == 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(6)).Layout(gtx, label(th, 14, colorMuted, "No matches").Layout)
			}))
		}
		for i, sg := range s.suggestions {
			sg := sg
			lead := func(gtx layout.Context) layout.Dimensions {
				switch {
				case sg.user != nil:
					return c.u.picture(gtx, c.authorAvatarURL(c.selGuild, *sg.user), 24, colorFor(uint64(sg.user.ID)), sg.title, -1)
				case sg.channel:
					return label(th, 20, colorText, "#").Layout(gtx)
				}
				return icon(gtx, iconFilterHas, 20, colorText)
			}
			children = append(children, row(&s.suggestBtns[i], lead, sg.title, sg.sub))
		}
		s.drawList(gtx, x, w, children)
		return
	}
	s.suggestions = nil
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(6), Bottom: unit.Dp(4)}.Layout(gtx, bold(label(th, 12, colorMuted, "Filters")).Layout)
	}))
	for i, f := range searchFilters {
		if f.key == "in:" && !c.selGuild.IsValid() {
			continue
		}
		f := f
		lead := func(gtx layout.Context) layout.Dimensions {
			if f.ic == nil {
				return label(th, 20, colorText, "#").Layout(gtx)
			}
			return icon(gtx, *f.ic, 20, colorText)
		}
		children = append(children, row(&s.filterBtns[i], lead, f.title, f.hint))
	}
	if len(history) > 0 {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return settingsDivider(gtx, 6) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, bold(label(th, 12, colorMuted, "History")).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return iconButton(gtx, &s.clearHistory, iconDelete, colorMuted, colorRed)
						}),
					)
				})
			}),
		)
		for i, q := range history {
			children = append(children, row(&s.historyBtns[i], func(gtx layout.Context) layout.Dimensions {
				return icon(gtx, iconSearch, 18, colorMuted)
			}, q, ""))
		}
	}
	s.drawList(gtx, x, w, children)
}

// drawList draws the list under the search field at x, w wide.
func (s *searchView) drawList(gtx layout.Context, x, w int, children []layout.FlexChild) {
	mgtx := gtx
	mgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y-gtx.Dp(60))}
	macro := op.Record(gtx.Ops)
	dims := rounded(mgtx, colorMenu, 8, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
	})
	call := macro.Stop()
	t := op.Offset(image.Pt(max(x, 0), gtx.Dp(46))).Push(gtx.Ops)
	solid(gtx, &s.historyBtns, dims)
	call.Add(gtx.Ops)
	t.Pop()
}

// layoutSearchResults is the column of what the search found.
func (c *chatView) layoutSearchResults(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	s := &c.search
	s.list.Axis = layout.Vertical
	if s.clicks == nil {
		s.clicks = map[discord.MessageID]*widget.Clickable{}
	}
	title := "Searching…"
	if !s.loading || len(s.results) > 0 {
		title = fmt.Sprintf("%d Result%s", s.total, plural(s.total))
	}
	head := func(gtx layout.Context) layout.Dimensions {
		height := gtx.Dp(48)
		size := image.Pt(gtx.Constraints.Max.X, height)
		gtx.Constraints = layout.Exact(size)
		paint.FillShape(gtx.Ops, colorServers, clip.Rect{Min: image.Pt(0, height-1), Max: size}.Op())
		layout.Inset{Left: unit.Dp(16), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, oneLine(bold(label(th, 16, colorHeader, title))).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return iconButton(gtx, &s.close, iconClose, colorMuted, colorHeader)
					}),
				)
			})
		})
		return layout.Dimensions{Size: size}
	}
	n := len(s.results)
	extra := 0
	if s.err != "" || s.loading || len(s.results) < s.total || n == 0 {
		extra = 1
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(head),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return listLayout(gtx, th, &s.list, n+extra, func(gtx layout.Context, i int) layout.Dimensions {
				if i == n {
					return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						switch {
						case s.err != "":
							return label(th, 14, colorRed, s.err).Layout(gtx)
						case s.loading:
							return label(th, 14, colorMuted, "Searching…").Layout(gtx)
						case n == 0:
							return label(th, 14, colorMuted, "No results. Try other words or filters.").Layout(gtx)
						}
						return button(gtx, th, &s.more, colorSecondaryButton, "Load More")
					})
				}
				return c.layoutSearchResult(gtx, s.results[i])
			})
		}),
	)
}

// layoutSearchResult is one message found: its channel, author, time and
// text, which a click shows in its channel.
func (c *chatView) layoutSearchResult(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	s := &c.search
	click := s.clicks[m.ID]
	if click == nil {
		click = new(widget.Clickable)
		s.clicks[m.ID] = click
	}
	guildID := c.selGuild
	chName := ""
	if ch, err := c.st.Cabinet.Channel(m.ChannelID); err == nil && ch.Name != "" {
		chName = "# " + ch.Name
	}
	name := c.memberName(guildID, m.Author.ID, nil)
	if name == "" {
		name = m.Author.DisplayOrUsername()
	}
	text := searchMarkup.Replace(c.renderContent(m))
	if text == "" && len(m.Attachments) > 0 {
		text = fmt.Sprintf("%d attachment%s", len(m.Attachments), plural(len(m.Attachments)))
	}
	return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			bg := colorChat
			if click.Hovered() {
				bg = colorHover
			}
			return rounded(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if chName == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, oneLine(bold(label(th, 12, colorMuted, chName))).Layout)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Start}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return c.u.picture(gtx, c.authorAvatarURL(guildID, m.Author), 32, colorFor(uint64(m.Author.ID)), name, -1)
								}),
								layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
												layout.Rigid(oneLine(bold(label(th, 15, colorHeader, name))).Layout),
												layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
												layout.Rigid(oneLine(label(th, 11, colorMuted, formatTime(m.Timestamp.Time()))).Layout),
											)
										}),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											l := label(th, 14, colorText, text)
											l.MaxLines, l.Truncator = 6, "…"
											return l.Layout(gtx)
										}),
									)
								}),
							)
						}),
					)
				})
			})
		})
	})
}
