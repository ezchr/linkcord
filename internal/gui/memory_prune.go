package gui

import (
	"context"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/ningen/v3"
)

// Members and presences of a server come from its member list while it is
// shown, and from the authors of its messages. ningen subscribes to the
// member list of each server opened and never lets it go, so after visiting
// many servers the state kept members of all of them, and Discord kept
// sending their list updates. Discord's own client only follows the lists it
// shows. The pruner below does the same: a server not shown for
// memberIdleAfter loses its members and presences (other than the user's),
// and its member list subscription is cut down to no channels. Shown again,
// the subscription is sent again; members the chat needs are asked for again
// with MemberState.RequestMember, as for any member not known.
const (
	memberIdleAfter = 30 * time.Minute
	// keepRecentGuilds servers viewed last keep their members however long
	// ago, so going back to them shows names and colors at once.
	keepRecentGuilds = 5
	pruneEvery       = time.Minute
	// freeOSAfter is how many members and presences let go at once make it
	// worth giving the freed memory back to Windows right away.
	freeOSAfter = 5000
)

// guildTracker remembers which servers a store holds something of, as
// arikawa's stores cannot list them.
type guildTracker struct {
	mu     sync.Mutex
	guilds map[discord.GuildID]time.Time // first set since the last prune
}

func (t *guildTracker) note(g discord.GuildID) {
	if !g.IsValid() {
		return // friends' presences have no server
	}
	t.mu.Lock()
	if _, ok := t.guilds[g]; !ok {
		t.guilds[g] = time.Now()
	}
	t.mu.Unlock()
}

func (t *guildTracker) snapshot() map[discord.GuildID]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := make(map[discord.GuildID]time.Time, len(t.guilds))
	for g, at := range t.guilds {
		m[g] = at
	}
	return m
}

func (t *guildTracker) forget(g discord.GuildID) {
	t.mu.Lock()
	delete(t.guilds, g)
	t.mu.Unlock()
}

// prunedMembers is arikawa's member store, noting the servers it holds.
type prunedMembers struct {
	store.MemberStore
	tracker *guildTracker
}

func (s *prunedMembers) MemberSet(g discord.GuildID, m *discord.Member, update bool) error {
	s.tracker.note(g)
	return s.MemberStore.MemberSet(g, m, update)
}

// prunedPresences is arikawa's presence store, noting the servers it holds.
type prunedPresences struct {
	store.PresenceStore
	tracker *guildTracker
}

func (s *prunedPresences) PresenceSet(g discord.GuildID, p *discord.Presence, update bool) error {
	s.tracker.note(g)
	return s.PresenceStore.PresenceSet(g, p, update)
}

// memberListUnsubscribe is the op 37 subscription of one server with an
// explicit empty set of channels, which arikawa's GuildSubscription leaves
// out when empty.
type memberListUnsubscribe struct {
	Subscriptions map[discord.GuildID]memberListSubscription `json:"subscriptions"`
}

type memberListSubscription struct {
	Typing     bool                           `json:"typing"`
	Threads    bool                           `json:"threads"`
	Activities bool                           `json:"activities"`
	Channels   map[discord.ChannelID][][2]int `json:"channels"`
}

func (*memberListUnsubscribe) Op() ws.OpCode           { return 37 }
func (*memberListUnsubscribe) EventType() ws.EventType { return "" }

func subscriptionOf(g discord.GuildID, channels map[discord.ChannelID][][2]int) *memberListUnsubscribe {
	return &memberListUnsubscribe{Subscriptions: map[discord.GuildID]memberListSubscription{
		g: {Typing: true, Threads: true, Activities: true, Channels: channels},
	}}
}

// memoryPruner lets go of the state of servers and channels not shown.
type memoryPruner struct {
	st       weak.Pointer[ningen.State]
	messages *recentStore
	tracker  *guildTracker

	page         string
	viewed       map[discord.GuildID]time.Time
	unsubscribed map[discord.GuildID]bool
	lastPrune    time.Time
}

// watchMemory runs the pruner for st until st is gone.
func watchMemory(st *ningen.State, messages *recentStore, tracker *guildTracker) {
	p := &memoryPruner{
		st:           weak.Make(st),
		messages:     messages,
		tracker:      tracker,
		viewed:       make(map[discord.GuildID]time.Time),
		unsubscribed: make(map[discord.GuildID]bool),
		lastPrune:    time.Now(),
	}
	go func() {
		// The page is checked every second, so a server shown again gets its
		// subscription back before its member list is looked at.
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for range tick.C {
			st := p.st.Value()
			if st == nil {
				return
			}
			p.step(st, time.Now())
		}
	}()
}

// parsePage reads the server and channel of a page address of pageURL.
func parsePage(page string) (discord.GuildID, discord.ChannelID) {
	rest, ok := strings.CutPrefix(page, "https://discord.com/channels/")
	if !ok {
		return 0, 0
	}
	gs, cs, _ := strings.Cut(rest, "/")
	var g discord.GuildID
	var c discord.ChannelID
	if s, err := discord.ParseSnowflake(gs); err == nil {
		g = discord.GuildID(s)
	}
	if s, err := discord.ParseSnowflake(cs); err == nil {
		c = discord.ChannelID(s)
	}
	return g, c
}

func (p *memoryPruner) step(st *ningen.State, now time.Time) {
	page := currentPage()
	g, ch := parsePage(page)
	if g.IsValid() {
		p.viewed[g] = now
	}
	if page != p.page {
		p.page = page
		if ch.IsValid() {
			p.messages.MarkOpened(ch)
		}
		if g.IsValid() && p.unsubscribed[g] {
			delete(p.unsubscribed, g)
			p.resubscribe(st, g, ch)
		}
	}
	if now.Sub(p.lastPrune) >= pruneEvery {
		p.lastPrune = now
		p.prune(st, g, now)
	}
}

// resubscribe follows the member list of ch again, which ningen does not
// send by itself when it was the last channel it subscribed in g.
func (p *memoryPruner) resubscribe(st *ningen.State, g discord.GuildID, ch discord.ChannelID) {
	channels := map[discord.ChannelID][][2]int{}
	if c, err := st.Cabinet.Channel(ch); err == nil {
		switch c.Type {
		case discord.GuildText, discord.GuildAnnouncement:
			channels[ch] = [][2]int{{0, 99}}
		}
	}
	if err := st.SendGateway(context.Background(), subscriptionOf(g, channels)); err != nil {
		slog.Warn("could not subscribe to member list again", "guild", g, "err", err)
	}
}

// prune lets go of the members and presences of servers not shown for a
// while.
func (p *memoryPruner) prune(st *ningen.State, shown discord.GuildID, now time.Time) {
	me, _ := st.Cabinet.Me()
	var meID discord.UserID
	if me != nil {
		meID = me.ID
	}
	freed := 0
	recent := recentGuilds(p.viewed, keepRecentGuilds)
	for g, first := range p.tracker.snapshot() {
		if g == shown || recent[g] {
			continue
		}
		last, wasShown := p.viewed[g]
		if !wasShown {
			last = first
		}
		if now.Sub(last) < memberIdleAfter {
			continue
		}
		// A server the user is in a voice channel of shows its members in
		// the call.
		if meID.IsValid() {
			if vs, err := st.Cabinet.VoiceState(g, meID); err == nil && vs.ChannelID.IsValid() {
				continue
			}
		}
		freed += dropGuildMembers(st.Cabinet, g, meID)
		p.tracker.forget(g)
		if wasShown && !p.unsubscribed[g] {
			if err := st.SendGateway(context.Background(), subscriptionOf(g, map[discord.ChannelID][][2]int{})); err != nil {
				slog.Warn("could not unsubscribe from member list", "guild", g, "err", err)
			} else {
				p.unsubscribed[g] = true
			}
		}
	}
	if freed > 0 {
		slog.Debug("let go of members of servers not shown", "count", freed)
	}
	if freed >= freeOSAfter {
		debug.FreeOSMemory()
	}
}

// dropGuildMembers removes the members and presences of g but the user's,
// and returns how many went.
func dropGuildMembers(cab *store.Cabinet, g discord.GuildID, me discord.UserID) int {
	n := 0
	if members, err := cab.Members(g); err == nil {
		for _, m := range members {
			if m.User.ID != me {
				_ = cab.MemberRemove(g, m.User.ID)
				n++
			}
		}
	}
	if presences, err := cab.Presences(g); err == nil {
		for _, pr := range presences {
			if pr.User.ID != me {
				_ = cab.PresenceRemove(g, pr.User.ID)
				n++
			}
		}
	}
	return n
}

// recentGuilds are the n servers viewed last.
func recentGuilds(viewed map[discord.GuildID]time.Time, n int) map[discord.GuildID]bool {
	ids := make([]discord.GuildID, 0, len(viewed))
	for g := range viewed {
		ids = append(ids, g)
	}
	slices.SortFunc(ids, func(a, b discord.GuildID) int { return viewed[b].Compare(viewed[a]) })
	out := make(map[discord.GuildID]bool, n)
	for _, g := range ids[:min(n, len(ids))] {
		out[g] = true
	}
	return out
}
