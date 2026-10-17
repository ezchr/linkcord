package gui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

// Following someone in voice, joining again when disconnected, and how long
// the user spends in voice and with whom.

// voiceExtras follows what a voice state update means for those features.
func (c *chatView) voiceExtras(ev *gateway.VoiceStateUpdateEvent) {
	u := c.u
	if ev.UserID == c.me.ID {
		c.noteVoiceChannel(ev.VoiceState)
		if ev.ChannelID.IsValid() {
			c.lastVoice = ev.VoiceState
			return
		}
		// Disconnected: by someone else, unless the user left a moment ago.
		last := c.lastVoice
		c.lastVoice = discord.VoiceState{}
		if !u.app.VoiceRejoin || !last.ChannelID.IsValid() || time.Since(u.voice.leftAt) < 5*time.Second {
			return
		}
		ch, err := c.st.Cabinet.Channel(last.ChannelID)
		if err != nil {
			return
		}
		u.voiceInfo(fmt.Sprintf(tr("Disconnected from %s, joining again…"), ch.Name))
		time.AfterFunc(time.Second, func() {
			u.post(func() {
				if !c.closed && !u.voiceStatus().Joined {
					c.joinVoice(*ch)
				}
			})
		})
		return
	}
	// The one followed goes to another voice channel: so does the user.
	if c.following.IsValid() && ev.UserID == c.following && ev.ChannelID.IsValid() {
		c.followGuild = ev.GuildID
		c.followTo(ev.ChannelID)
	}
}

// followTo joins chID, where the one followed is, unless the user is there
// or tried a moment ago; why it cannot, the voice panel says.
func (c *chatView) followTo(chID discord.ChannelID) {
	u := c.u
	if chID == u.voiceChannel() {
		return
	}
	// A join takes a moment: the same one is not asked for again at once.
	if chID == c.followTried && time.Since(c.followAt) < 6*time.Second {
		return
	}
	ch, err := c.st.Cabinet.Channel(chID)
	if err != nil {
		return
	}
	c.followTried, c.followAt = chID, time.Now()
	if err := c.joinable(*ch); err != nil {
		u.voiceInfo(fmt.Sprintf(tr("Could not follow into %s: "), ch.Name) + tr(err.Error()))
		return
	}
	c.joinVoice(*ch)
}

// watchFollow looks every two seconds where the one followed is, and joins
// them if the user is not with them: a join missed, as one asked while the
// voice connection was busy leaving the last channel, is tried again.
func (c *chatView) watchFollow() {
	if c.followWatched {
		return
	}
	c.followWatched = true
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-tick.C:
			}
			c.u.post(func() {
				if !c.following.IsValid() || !c.followGuild.IsValid() {
					return
				}
				if vs, err := c.st.Cabinet.VoiceState(c.followGuild, c.following); err == nil && vs.ChannelID.IsValid() {
					c.followTo(vs.ChannelID)
				}
			})
		}
	}()
}

// toggleFollow follows id from voice channel to voice channel, or stops.
func (c *chatView) toggleFollow(guildID discord.GuildID, id discord.UserID) {
	if c.following == id {
		c.following = 0
		c.u.voiceInfo(tr("No longer following anyone in voice"))
		return
	}
	c.following, c.followGuild = id, guildID
	c.followTried = 0
	c.u.voiceInfo(fmt.Sprintf(tr("Following %s in voice"), c.memberName(guildID, id, nil)))
	c.watchFollow()
	// Where they are now, the user goes too.
	if vs, err := c.st.Cabinet.VoiceState(guildID, id); err == nil && vs.ChannelID.IsValid() {
		c.followTo(vs.ChannelID)
	}
}

// voiceStats is how long the user spent in voice, by channel and with each
// person, kept in voice-stats.json.
type voiceStats struct {
	Since    time.Time                `json:"since"`
	Total    time.Duration            `json:"total"`
	Channels map[string]*voiceStatRow `json:"channels"`
	People   map[string]*voiceStatRow `json:"people"`
}

type voiceStatRow struct {
	Name string        `json:"name"`
	Time time.Duration `json:"time"`
}

var voiceStatsMu sync.Mutex

func voiceStatsPath() string { return filepath.Join(filepath.Dir(logPath()), "voice-stats.json") }

func loadVoiceStats() *voiceStats {
	s := &voiceStats{Since: time.Now(), Channels: map[string]*voiceStatRow{}, People: map[string]*voiceStatRow{}}
	if data, err := os.ReadFile(voiceStatsPath()); err == nil {
		json.Unmarshal(data, s)
	}
	if s.Channels == nil {
		s.Channels = map[string]*voiceStatRow{}
	}
	if s.People == nil {
		s.People = map[string]*voiceStatRow{}
	}
	return s
}

func (s *voiceStats) save() {
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	go func() {
		voiceStatsMu.Lock()
		defer voiceStatsMu.Unlock()
		if err := os.WriteFile(voiceStatsPath(), data, 0o600); err != nil {
			slog.Warn("could not keep the voice statistics", "err", err)
		}
	}()
}

// statTick is how often the time in voice is counted.
const statTick = 30 * time.Second

// watchVoiceStats counts, every half minute, the time in the voice channel
// the user is in, and with whom, until done.
func (c *chatView) watchVoiceStats() {
	c.stats = loadVoiceStats()
	go func() {
		tick := time.NewTicker(statTick)
		defer tick.Stop()
		n := 0
		for {
			select {
			case <-c.done:
				return
			case <-tick.C:
			}
			n++
			save := n%4 == 0
			c.u.post(func() { c.countVoice(save) })
		}
	}()
}

// countVoice adds a tick to the channel the user is in and to those in it.
func (c *chatView) countVoice(save bool) {
	st := c.u.voiceStatus()
	if c.u.app.NoVoiceStats || !st.Joined || c.stats == nil {
		return
	}
	s := c.stats
	s.Total += statTick
	ch, err := c.st.Cabinet.Channel(st.ChannelID)
	if err != nil {
		return
	}
	add := func(m map[string]*voiceStatRow, id, name string) {
		r := m[id]
		if r == nil {
			r = new(voiceStatRow)
			m[id] = r
		}
		r.Name = cmp.Or(name, r.Name)
		r.Time += statTick
	}
	add(s.Channels, ch.ID.String(), ch.Name)
	if ch.GuildID.IsValid() {
		states, _ := c.st.Cabinet.VoiceStates(ch.GuildID)
		for _, vs := range states {
			if vs.ChannelID == ch.ID && vs.UserID != c.me.ID {
				add(s.People, vs.UserID.String(), c.memberName(ch.GuildID, vs.UserID, vs.Member))
			}
		}
	}
	if save {
		s.save()
	}
}

// topStats are the rows of m with the most time, at most n.
func topStats(m map[string]*voiceStatRow, n int) []voiceStatRow {
	var out []voiceStatRow
	for _, r := range m {
		out = append(out, *r)
	}
	// Those with the same time by name, so that they keep their place: the
	// map gives them in no order.
	slices.SortFunc(out, func(a, b voiceStatRow) int {
		return cmp.Or(cmp.Compare(b.Time, a.Time), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Name, b.Name))
	})
	return out[:min(n, len(out))]
}

// narrate says aloud who joined or left the voice channel the user is in,
// as they chose.
func (c *chatView) narrate(ev *gateway.VoiceStateUpdateEvent, joined bool) {
	if !c.u.app.VoiceNarrator {
		return
	}
	name := c.memberName(ev.GuildID, ev.UserID, ev.Member)
	text := fmt.Sprintf(tr("%s left"), name)
	if joined {
		text = fmt.Sprintf(tr("%s joined"), name)
	}
	go speak(text)
}

// formatHours is d as hours and minutes.
func formatHours(d time.Duration) string {
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h == 0 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}
