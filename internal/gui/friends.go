package gui

import (
	"cmp"
	"errors"
	"fmt"
	"gioui.org/op"
	"image"
	"log/slog"
	"slices"
	"strings"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

var (
	iconCheck   = mustIcon(icons.NavigationCheck)
	iconMessage = mustIcon(icons.CommunicationChat)
	iconMail    = mustIcon(icons.CommunicationEmail)
)

// homePage is what the middle of the direct messages shows while no
// conversation is open.
type homePage int

const (
	homeNone homePage = iota
	homeFriends
	homeRequests
)

// The rows of the friends and the message requests at the top of the direct
// messages stand in for channels with IDs that no channel has.
const (
	friendsRowID  = discord.ChannelID(1)
	requestsRowID = discord.ChannelID(2)
)

type friendTab int

const (
	tabOnline friendTab = iota
	tabAll
	tabPending
	tabBlocked
	tabAddFriend
	friendTabCount
)

var friendTabNames = [friendTabCount]string{"Online", "All", "Pending", "Blocked", "Add Friend"}

// friendAction is what a button on a friend row does.
type friendAction int

const (
	actMessage friendAction = iota
	actAccept
	actRemove
)

type friendKey struct {
	user discord.UserID
	act  friendAction
}

// messageRequest is a direct message from someone who is not a friend,
// which waits to be accepted before it joins the direct messages. Discord
// sends these fields only with the list of direct messages.
type messageRequest struct {
	ID               discord.ChannelID   `json:"id"`
	Type             discord.ChannelType `json:"type"`
	IsMessageRequest bool                `json:"is_message_request"`
	IsSpam           bool                `json:"is_spam"`
	Since            discord.Timestamp   `json:"is_message_request_timestamp,omitzero"`
	LastMessageID    discord.MessageID   `json:"last_message_id"`
	Recipients       []discord.User      `json:"recipients"`
}

type requestKey struct {
	ch     discord.ChannelID
	accept bool
}

// friendsState is the friends page and the message requests.
type friendsState struct {
	page homePage
	tab  friendTab
	tabs [friendTabCount]widget.Clickable
	list widget.List

	rows    map[discord.UserID]*widget.Clickable
	actions map[friendKey]*widget.Clickable
	// busy holds the users with a change on its way to Discord.
	busy map[discord.UserID]bool
	err  string

	add     widget.Editor
	addBtn  widget.Clickable
	adding  bool
	addMsg  string
	addGood bool

	requests        []messageRequest
	requestIDs      map[discord.ChannelID]bool
	requestsLoading bool
	requestRows     map[discord.ChannelID]*widget.Clickable
	requestActs     map[requestKey]*widget.Clickable
	requestList     widget.List

	// The demo keeps its friends here instead of in the gateway state.
	demoRels []discord.Relationship
}

func (f *friendsState) init() {
	f.list.Axis = layout.Vertical
	f.requestList.Axis = layout.Vertical
	f.rows = make(map[discord.UserID]*widget.Clickable)
	f.actions = make(map[friendKey]*widget.Clickable)
	f.busy = make(map[discord.UserID]bool)
	f.requestIDs = make(map[discord.ChannelID]bool)
	f.requestRows = make(map[discord.ChannelID]*widget.Clickable)
	f.requestActs = make(map[requestKey]*widget.Clickable)
	f.add.SingleLine = true
	f.add.Submit = true
}

func clickOf[K comparable](m map[K]*widget.Clickable, k K) *widget.Clickable {
	cl, ok := m[k]
	if !ok {
		cl = new(widget.Clickable)
		m[k] = cl
	}
	return cl
}

// openHome shows a page of the direct messages in place of a conversation.
func (c *chatView) openHome(p homePage) {
	c.closeChannel()
	c.friends.page = p
	switch p {
	case homeFriends:
		c.setTitle("Friends - " + appName)
	case homeRequests:
		c.setTitle("Message Requests - " + appName)
		c.loadMessageRequests()
	}
}

// relationships returns the friends, requests and blocked users.
func (c *chatView) relationships() []discord.Relationship {
	if c.demo {
		return c.friends.demoRels
	}
	// Read at most every second: each frame read them all several times.
	if c.rels != nil && time.Since(c.relsAt) < time.Second {
		return c.rels
	}
	out := []discord.Relationship{}
	c.st.RelationshipState.Each(func(id discord.UserID, _ discord.RelationshipType) bool {
		if rel, ok := c.st.RelationshipState.FullRelationship(id); ok {
			out = append(out, rel)
		}
		return false
	})
	c.rels, c.relsAt = out, time.Now()
	return out
}

// incomingRequests counts the friend requests waiting for an answer.
func (c *chatView) incomingRequests() int {
	n := 0
	for _, rel := range c.relationships() {
		if rel.Type == discord.IncomingFriendRequest {
			n++
		}
	}
	return n
}

// relationshipName is how a friend is called: the nickname given to them,
// their display name, or their username.
func relationshipName(rel discord.Relationship) string {
	if rel.Nickname != nil && *rel.Nickname != "" {
		return *rel.Nickname
	}
	return rel.User.DisplayOrUsername()
}

// friendRows returns the relationships the tab lists, sorted by name.
func (c *chatView) friendRows(tab friendTab) []discord.Relationship {
	var out []discord.Relationship
	for _, rel := range c.relationships() {
		switch tab {
		case tabOnline:
			if rel.Type != discord.FriendRelationship {
				continue
			}
			if s := c.status(discord.NullGuildID, rel.UserID); s == discord.OfflineStatus || s == discord.InvisibleStatus {
				continue
			}
		case tabAll:
			if rel.Type != discord.FriendRelationship {
				continue
			}
		case tabPending:
			if rel.Type != discord.IncomingFriendRequest && rel.Type != discord.SentFriendRequest {
				continue
			}
		case tabBlocked:
			if rel.Type != discord.BlockedRelationship {
				continue
			}
		}
		out = append(out, rel)
	}
	slices.SortFunc(out, func(a, b discord.Relationship) int {
		// Incoming requests go before outgoing ones.
		incoming := func(r discord.Relationship) int {
			if r.Type == discord.IncomingFriendRequest {
				return 0
			}
			return 1
		}
		return cmp.Or(
			cmp.Compare(incoming(a), incoming(b)),
			strings.Compare(strings.ToLower(relationshipName(a)), strings.ToLower(relationshipName(b))),
		)
	})
	return out
}

// friendError turns what Discord answers to a friend change into a message.
func friendError(err error) string {
	var he *httputil.HTTPError
	if !errors.As(err, &he) {
		var v httputil.HTTPError
		if errors.As(err, &v) {
			he = &v
		}
	}
	if he != nil {
		switch he.Code {
		case 80004:
			return "Hm, didn't work. Double check that the username is correct."
		case 80007:
			return "You're already friends with that user!"
		case 80001:
			return "That user is not accepting friend requests."
		case 80000:
			return "Discord refused the friend request. Try again later."
		}
		if strings.Contains(string(he.Body), "captcha") {
			return "Discord asked for a captcha, which Linkcord cannot show. Send the request from the Discord app or website."
		}
		if he.Message != "" {
			return he.Message
		}
	}
	return err.Error()
}

// changeRelationship accepts a friend request when accept is true, or
// otherwise removes the friend, the request or the block.
func (c *chatView) changeRelationship(rel discord.Relationship, accept bool) {
	f := &c.friends
	if f.busy[rel.UserID] {
		return
	}
	f.err = ""
	if c.demo {
		for i := range f.demoRels {
			if f.demoRels[i].UserID != rel.UserID {
				continue
			}
			if accept {
				f.demoRels[i].Type = discord.FriendRelationship
			} else {
				f.demoRels = slices.Delete(f.demoRels, i, i+1)
			}
			break
		}
		return
	}
	f.busy[rel.UserID] = true
	go func() {
		var err error
		if accept {
			err = c.st.Client.SetRelationship(rel.UserID, discord.FriendRelationship)
		} else {
			err = c.st.Client.DeleteRelationship(rel.UserID)
		}
		c.u.post(func() {
			delete(f.busy, rel.UserID)
			if err != nil {
				slog.Error("failed to change relationship", "err", err, "user_id", rel.UserID)
				f.err = friendError(err)
			}
			// The gateway sends the change, which updates the lists.
		})
	}()
}

// sendFriendRequest asks the user with the username typed to be a friend.
func (c *chatView) sendFriendRequest() {
	f := &c.friends
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(f.add.Text()), "@"))
	if name == "" || f.adding {
		return
	}
	f.addMsg, f.addGood = "", false
	if c.demo {
		f.demoRels = append(f.demoRels, discord.Relationship{
			UserID: discord.UserID(discord.NewSnowflake(time.Now())),
			User:   discord.User{Username: name, Discriminator: "0"},
			Type:   discord.SentFriendRequest,
		})
		f.demoRels[len(f.demoRels)-1].User.ID = f.demoRels[len(f.demoRels)-1].UserID
		f.addMsg, f.addGood = fmt.Sprintf("Success! Your friend request to %s was sent.", name), true
		f.add.SetText("")
		return
	}
	f.adding = true
	go func() {
		body := struct {
			Username      string  `json:"username"`
			Discriminator *string `json:"discriminator"`
		}{Username: name}
		err := c.st.Client.FastRequest("POST", api.EndpointMe+"/relationships", httputil.WithJSONBody(body))
		c.u.post(func() {
			f.adding = false
			if err != nil {
				slog.Error("failed to send friend request", "err", err)
				f.addMsg = friendError(err)
				return
			}
			f.addMsg, f.addGood = fmt.Sprintf("Success! Your friend request to %s was sent.", name), true
			f.add.SetText("")
		})
	}()
}

// loadMessageRequests fetches the direct messages to find the message
// requests among them, which the gateway does not mark.
func (c *chatView) loadMessageRequests() {
	f := &c.friends
	if c.demo || f.requestsLoading {
		return
	}
	f.requestsLoading = true
	go func() {
		chs, err := c.st.Client.RequestJSON[[]messageRequest]("GET", api.EndpointMe+"/channels")
		c.u.post(func() {
			f.requestsLoading = false
			if err != nil {
				slog.Error("failed to load message requests", "err", err)
				return
			}
			f.setRequests(chs)
			c.rebuildRows()
		})
	}()
}

// setRequests keeps the message requests among chs.
func (f *friendsState) setRequests(chs []messageRequest) {
	f.requests = f.requests[:0]
	clear(f.requestIDs)
	for _, ch := range chs {
		if ch.IsMessageRequest {
			f.requests = append(f.requests, ch)
			f.requestIDs[ch.ID] = true
		}
	}
	slices.SortFunc(f.requests, func(a, b messageRequest) int {
		return cmp.Compare(b.LastMessageID, a.LastMessageID)
	})
}

// answerRequest accepts a message request, which moves the conversation to
// the direct messages, or rejects and deletes it.
func (c *chatView) answerRequest(req messageRequest, accept bool) {
	f := &c.friends
	done := func() {
		f.requests = slices.DeleteFunc(f.requests, func(r messageRequest) bool { return r.ID == req.ID })
		delete(f.requestIDs, req.ID)
		c.rebuildRows()
	}
	if c.demo {
		done()
		return
	}
	go func() {
		var err error
		path := api.EndpointChannels + req.ID.String() + "/recipients/@me"
		if accept {
			body := struct {
				ConsentStatus int `json:"consent_status"`
			}{2}
			err = c.st.Client.FastRequest("PUT", path, httputil.WithJSONBody(body))
		} else {
			err = c.st.Client.FastRequest("DELETE", path)
		}
		c.u.post(func() {
			if err != nil {
				slog.Error("failed to answer message request", "err", err, "channel_id", req.ID)
				f.err = "Could not answer the message request: " + friendError(err)
				return
			}
			done()
		})
	}()
}

func (c *chatView) updateFriends(gtx layout.Context) {
	f := &c.friends
	for i := range f.tabs {
		if f.tabs[i].Clicked(gtx) {
			f.tab, f.err, f.addMsg = friendTab(i), "", ""
			f.list.Position = layout.Position{}
		}
	}
	for {
		ev, ok := f.add.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			c.sendFriendRequest()
		}
		// Typing again clears the last answer; emptying the box after a
		// request went out keeps it.
		if _, ok := ev.(widget.ChangeEvent); ok && f.add.Text() != "" {
			f.addMsg = ""
		}
	}
	if f.addBtn.Clicked(gtx) {
		c.sendFriendRequest()
	}
	// The buttons sit on their row, so a click on one reaches the row too;
	// the row only acts when no button did.
	for _, rel := range c.relationships() {
		pressed := false
		if clickOf(f.actions, friendKey{rel.UserID, actMessage}).Clicked(gtx) {
			c.openDM(rel.UserID)
			pressed = true
		}
		if clickOf(f.actions, friendKey{rel.UserID, actAccept}).Clicked(gtx) {
			c.changeRelationship(rel, true)
			pressed = true
		}
		if clickOf(f.actions, friendKey{rel.UserID, actRemove}).Clicked(gtx) {
			c.changeRelationship(rel, false)
			pressed = true
		}
		if clickOf(f.rows, rel.UserID).Clicked(gtx) && !pressed {
			c.openProfile(rel.User, discord.NullGuildID)
		}
	}
	for _, req := range f.requests {
		pressed := false
		if clickOf(f.requestActs, requestKey{req.ID, true}).Clicked(gtx) {
			c.answerRequest(req, true)
			pressed = true
		}
		if clickOf(f.requestActs, requestKey{req.ID, false}).Clicked(gtx) {
			c.answerRequest(req, false)
			pressed = true
		}
		if clickOf(f.requestRows, req.ID).Clicked(gtx) && !pressed {
			if ch, err := c.st.Cabinet.Channel(req.ID); err == nil {
				c.selectChannel(*ch)
			}
		}
	}
}

// layoutHomeRow is the row of the friends or the message requests at the
// top of the direct messages, with the count of what waits for an answer.
func (c *chatView) layoutHomeRow(gtx layout.Context, r channelRow) layout.Dimensions {
	th := c.u.th
	page, ic, count := homeFriends, iconMembers, c.incomingRequests()
	if r.ch.ID == requestsRowID {
		page, ic, count = homeRequests, iconMail, len(c.friends.requests)
	}
	selected := c.selChannel == nil && c.friends.page == page
	return rowItem(gtx, c.click(discord.Snowflake(r.ch.ID)), func(gtx layout.Context, hovered bool) layout.Dimensions {
		col := colorMuted
		if selected || hovered {
			col = colorHeader
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icon(gtx, ic, 24, col)
				})
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Flexed(1, oneLine(bold(label(th, 15, col, r.label))).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return countBadge(gtx, c.u, count)
			}),
		)
	}, selected)
}

// countBadge is the red mark with a number that waits: a circle for one
// digit and a pill for more, as on Discord.
func countBadge(gtx layout.Context, u *ui, n int) layout.Dimensions {
	if n <= 0 {
		return layout.Dimensions{}
	}
	txt := fmt.Sprint(n)
	if n > 99 {
		txt = "99+"
	}
	// The number is measured first, so that the mark is exactly as tall
	// as wide for one digit.
	h := gtx.Dp(16)
	lgtx := gtx
	lgtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	l := bold(label(u.th, 12, colorHeader, txt))
	l.MaxLines = 1
	ld := l.Layout(lgtx)
	call := macro.Stop()
	w := max(h, ld.Size.X+gtx.Dp(8))
	size := image.Pt(w, h)
	paint.FillShape(gtx.Ops, colorRed, clip.UniformRRect(image.Rectangle{Max: size}, h/2).Op(gtx.Ops))
	t := op.Offset(image.Pt((w-ld.Size.X)/2, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// layoutFriends is the friends page: tabs for the friends online, all of
// them, the pending requests and the blocked users, and one to add friends.
func (c *chatView) layoutFriends(gtx layout.Context) layout.Dimensions {
	c.updateFriends(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(c.layoutFriendsHeader),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if c.friends.tab == tabAddFriend {
				return c.layoutAddFriend(gtx)
			}
			return c.layoutFriendList(gtx)
		}),
	)
}

func (c *chatView) layoutFriendsHeader(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	f := &c.friends
	height := gtx.Dp(48)
	size := image.Pt(gtx.Constraints.Max.X, height)
	gtx.Constraints = layout.Exact(size)
	paint.FillShape(gtx.Ops, colorServers, clip.Rect{Min: image.Pt(0, height-1), Max: size}.Op())
	tabs := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconMembers, 22, colorMuted) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(bold(label(th, 16, colorHeader, "Friends")).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// A short line separates the title from the tabs.
			return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				h := gtx.Dp(24)
				paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: image.Pt(gtx.Dp(1), h)}.Op())
				return layout.Dimensions{Size: image.Pt(gtx.Dp(1), h)}
			})
		}),
	}
	for i := range friendTabCount {
		tab := friendTab(i)
		tabs = append(tabs, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			cl := &f.tabs[i]
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					active := f.tab == tab
					bg, col := colorT{}, colorMuted
					switch {
					case tab == tabAddFriend && active:
						bg, col = colorT{}, colorGreen
					case tab == tabAddFriend:
						bg, col = colorGreenButton, colorHeader
					case active:
						bg, col = colorSelected, colorHeader
					case cl.Hovered():
						bg, col = colorHover, colorText
					}
					return rounded(gtx, bg, 4, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							children := []layout.FlexChild{layout.Rigid(bold(label(th, 14, col, friendTabNames[i])).Layout)}
							if tab == tabPending {
								if n := c.incomingRequests(); n > 0 {
									children = append(children,
										layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions { return countBadge(gtx, c.u, n) }),
									)
								}
							}
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
						})
					})
				})
			})
		}))
	}
	layout.Inset{Left: unit.Dp(16), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return vcenter(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, tabs...)
		})
	})
	return layout.Dimensions{Size: size}
}

func (c *chatView) layoutFriendList(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	f := &c.friends
	rows := c.friendRows(f.tab)
	title := map[friendTab]string{tabOnline: "ONLINE", tabAll: "ALL FRIENDS", tabPending: "PENDING", tabBlocked: "BLOCKED"}[f.tab]
	return layout.Inset{Left: unit.Dp(20), Right: unit.Dp(20), Top: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if f.err == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, label(th, 14, colorRed, f.err).Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, bold(label(th, 12, colorMuted, fmt.Sprintf("%s — %d", title, len(rows)))).Layout)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if len(rows) == 0 {
					return layout.Center.Layout(gtx, label(th, 15, colorMuted, emptyFriendsText(f.tab)).Layout)
				}
				return listLayout(gtx, th, &f.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
					return c.layoutFriendRow(gtx, rows[i])
				})
			}),
		)
	})
}

func emptyFriendsText(tab friendTab) string {
	switch tab {
	case tabOnline:
		return "No one is online right now."
	case tabPending:
		return "There are no pending friend requests."
	case tabBlocked:
		return "You haven't blocked anyone."
	}
	return "You have no friends yet. Add one with their username."
}

// layoutFriendRow is a friend, request or blocked user, with the buttons
// that apply to it.
func (c *chatView) layoutFriendRow(gtx layout.Context, rel discord.Relationship) layout.Dimensions {
	th := c.u.th
	f := &c.friends
	presence := c.presence(discord.NullGuildID, rel.UserID)
	status := c.status(discord.NullGuildID, rel.UserID)
	var sub string
	switch rel.Type {
	case discord.IncomingFriendRequest:
		sub = "Incoming Friend Request"
	case discord.SentFriendRequest:
		sub = "Outgoing Friend Request"
	case discord.BlockedRelationship:
		sub = "Blocked"
	default:
		sub = statusName(status)
		if line := activityLine(presence); line != "" && status != discord.OfflineStatus {
			sub = line
		}
	}
	type action struct {
		act       friendAction
		ic        *widget.Icon
		hover     colorT
		tooltipOK bool
	}
	var actions []action
	switch rel.Type {
	case discord.FriendRelationship:
		actions = []action{{act: actMessage, ic: iconMessage, hover: colorHeader}}
	case discord.IncomingFriendRequest:
		actions = []action{{act: actAccept, ic: iconCheck, hover: colorGreen}, {act: actRemove, ic: iconClose, hover: colorRed}}
	case discord.SentFriendRequest, discord.BlockedRelationship:
		actions = []action{{act: actRemove, ic: iconClose, hover: colorRed}}
	}
	busy := f.busy[rel.UserID]

	// A line separates the rows, as in Discord.
	paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))}.Op())
	return rowItem(gtx, clickOf(f.rows, rel.UserID), func(gtx layout.Context, hovered bool) layout.Dimensions {
		ring := colorChat
		if hovered {
			ring = colorHover
		}
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.avatarStatus(gtx, rel.User, discord.NullGuildID, 32, ring)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
								layout.Rigid(oneLine(bold(label(th, 15, colorHeader, relationshipName(rel)))).Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if !hovered {
										return layout.Dimensions{}
									}
									return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, oneLine(label(th, 13, colorMuted, rel.User.Username)).Layout)
								}),
							)
						}),
						layout.Rigid(oneLine(label(th, 13, colorMuted, sub)).Layout),
					)
				}),
			}
			for _, a := range actions {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						col := colorText
						if busy {
							col = colorMuted
						}
						return roundButton(gtx, clickOf(f.actions, friendKey{rel.UserID, a.act}), a.ic, col, a.hover)
					})
				}))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	}, false)
}

// roundButton is a round dark button showing ic, as Discord has on the rows
// of friends.
func roundButton(gtx layout.Context, click *widget.Clickable, ic *widget.Icon, c, hover colorT) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		size := gtx.Dp(36)
		r := image.Rectangle{Max: image.Pt(size, size)}
		paint.FillShape(gtx.Ops, colorSidebar, clip.Ellipse(r).Op(gtx.Ops))
		if click.Hovered() {
			c = hover
		}
		gtx.Constraints = layout.Exact(r.Max)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return icon(gtx, ic, 20, c)
		})
	})
}

// layoutAddFriend is the tab to send a friend request by username.
func (c *chatView) layoutAddFriend(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	f := &c.friends
	return layout.Inset{Left: unit.Dp(20), Right: unit.Dp(20), Top: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(bold(label(th, 16, colorHeader, "ADD FRIEND")).Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(label(th, 14, colorMuted, "You can add friends with their Discord username.").Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				border := colorServers
				switch {
				case f.addMsg != "" && f.addGood:
					border = colorGreen
				case f.addMsg != "":
					border = colorRed
				}
				return rounded(gtx, border, 8, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(unit.Dp(1)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return rounded(gtx, colorServers, 7, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return layout.Inset{Left: unit.Dp(14), Right: unit.Dp(8), Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
										gtx.Constraints.Min.X = gtx.Constraints.Max.X
										e := materialEditor(th, &f.add, "Enter a username")
										return e.Layout(gtx)
									}),
									layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										txt, bg := "Send Friend Request", colorAccent
										if strings.TrimSpace(f.add.Text()) == "" {
											bg = withAlpha(colorAccent, 0x80)
										}
										if f.adding {
											txt = "Sending…"
										}
										return button(gtx, th, &f.addBtn, bg, txt)
									}),
								)
							})
						})
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if f.addMsg == "" {
					return layout.Dimensions{}
				}
				col := colorRed
				if f.addGood {
					col = colorGreen
				}
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, label(th, 14, col, f.addMsg).Layout)
			}),
		)
	})
}

// layoutMessageRequests lists the direct messages from people who are not
// friends, to accept or ignore them.
func (c *chatView) layoutMessageRequests(gtx layout.Context) layout.Dimensions {
	c.updateFriends(gtx)
	th := c.u.th
	f := &c.friends
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return header(gtx, th, "Message Requests") }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(20), Right: unit.Dp(20), Top: unit.Dp(16)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if f.err == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, label(th, 14, colorRed, f.err).Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, bold(label(th, 12, colorMuted, fmt.Sprintf("MESSAGE REQUESTS — %d", len(f.requests)))).Layout)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						if len(f.requests) == 0 {
							txt := "There are no message requests."
							if f.requestsLoading {
								txt = "Loading message requests…"
							}
							return layout.Center.Layout(gtx, label(th, 15, colorMuted, txt).Layout)
						}
						return listLayout(gtx, th, &f.requestList, len(f.requests), func(gtx layout.Context, i int) layout.Dimensions {
							return c.layoutRequestRow(gtx, f.requests[i])
						})
					}),
				)
			})
		}),
	)
}

func (c *chatView) layoutRequestRow(gtx layout.Context, req messageRequest) layout.Dimensions {
	th := c.u.th
	f := &c.friends
	var user discord.User
	if len(req.Recipients) > 0 {
		user = req.Recipients[0]
	}
	sub := "Sent you a message request"
	if req.Since.IsValid() {
		sub += " · " + formatTime(req.Since.Time())
	}
	if req.IsSpam {
		sub = "Marked as possible spam · " + sub
	}
	paint.FillShape(gtx.Ops, colorSelected, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))}.Op())
	return rowItem(gtx, clickOf(f.requestRows, req.ID), func(gtx layout.Context, hovered bool) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.u.picture(gtx, cdnSize(user.AvatarURLWithType(discord.PNGImage)), 32, colorFor(uint64(user.ID)), user.DisplayOrUsername(), -1)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(oneLine(bold(label(th, 15, colorHeader, user.DisplayOrUsername()))).Layout),
						layout.Rigid(oneLine(label(th, 13, colorMuted, sub)).Layout),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, clickOf(f.requestActs, requestKey{req.ID, false}), colorSelected, "Ignore")
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return button(gtx, th, clickOf(f.requestActs, requestKey{req.ID, true}), colorAccent, "Accept")
				}),
			)
		})
	}, false)
}
