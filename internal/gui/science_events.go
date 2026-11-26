package gui

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/ningen/v3"
)

// The analytics events Linkcord sends, where the web client sends them,
// with the properties it gives them (client study, docs 05, 1.5d and 1.7).

// guildProps are trackWithMetadata's guild properties.
func (c *chatView) guildProps(guildID discord.GuildID, p map[string]any) {
	if !guildID.IsValid() {
		return
	}
	p["guild_id"] = guildID.String()
	g, gerr := c.st.Cabinet.Guild(guildID)
	if gerr == nil {
		p["guild_size_total"] = g.ApproximateMembers
		p["guild_num_roles"] = len(g.Roles)
		vip := false
		for _, f := range g.Features {
			if f == discord.VIPRegions {
				vip = true
			}
		}
		p["guild_is_vip"] = vip
	}
	if chs, err := c.st.Cabinet.Channels(guildID); err == nil {
		text, voice := 0, 0
		for _, ch := range chs {
			switch ch.Type {
			case discord.GuildText, discord.GuildAnnouncement, discord.GuildForum:
				text++
			case discord.GuildVoice, discord.GuildStageVoice:
				voice++
			}
		}
		p["guild_num_channels"] = len(chs)
		p["guild_num_text_channels"] = text
		p["guild_num_voice_channels"] = voice
	}
	m, merr := c.st.Cabinet.Member(guildID, c.me.ID)
	p["is_member"] = merr == nil
	if merr == nil {
		p["guild_member_num_roles"] = len(m.RoleIDs)
		if gerr == nil {
			if perms := c.guildPerms(guildID); perms != 0 {
				p["guild_member_perms"] = strconv.FormatUint(uint64(perms), 10)
			}
		}
	}
}

// channelProps are trackWithMetadata's channel properties.
func (c *chatView) channelProps(ch discord.Channel, p map[string]any) {
	p["channel_id"] = ch.ID.String()
	p["channel_type"] = int(ch.Type)
	if perms, ok := c.cachedPerms(ch.ID); ok {
		p["channel_member_perms"] = strconv.FormatUint(uint64(perms), 10)
		p["channel_hidden"] = !perms.Has(discord.PermissionViewChannel)
	}
	if !ch.GuildID.IsValid() {
		p["channel_size_total"] = len(ch.DMRecipients) + 1
	}
	c.guildProps(ch.GuildID, p)
}

func (c *chatView) trackChannelOpened(ch discord.Channel) {
	if c.sci == nil {
		return
	}
	p := map[string]any{}
	c.channelProps(ch, p)
	c.sci.track("channel_opened", p, "channel_opened:"+ch.ID.String(), 15*time.Minute)
	if ch.GuildID.IsValid() {
		g := map[string]any{"is_pending": false, "preview_enabled": false}
		c.guildProps(ch.GuildID, g)
		c.sci.track("guild_viewed", g, "guild_viewed:"+ch.GuildID.String(), 15*time.Minute)
	}
}

func (c *chatView) trackAck(chID discord.ChannelID) {
	if c.sci == nil {
		return
	}
	p := map[string]any{"channel_id": chID.String(), "location": "Channel"}
	if ch, err := c.st.Cabinet.Channel(chID); err == nil && ch.GuildID.IsValid() {
		p["guild_id"] = ch.GuildID.String()
	}
	p["guild_unread_statuses"] = c.guildUnreadStatuses()
	c.sci.track("ack_messages", p, "ack_messages:"+chID.String(), 15*time.Minute)
}

func (c *chatView) trackMemberList() {
	if c.sci == nil || c.selChannel == nil {
		return
	}
	ch := c.selChannel
	p := map[string]any{"channel_id": ch.ID.String(), "channel_type": int(ch.Type)}
	if ch.GuildID.IsValid() {
		p["guild_id"] = ch.GuildID.String()
	}
	c.sci.track("member_list_viewed", p, "member_list_viewed:"+ch.ID.String(), 15*time.Minute)
}

func (c *chatView) trackSettingsPane(dest string) {
	if c.sci != nil {
		c.sci.track("settings_pane_viewed", map[string]any{"settings_type": "user", "destination_pane": dest}, "", 0)
	}
}

func (c *chatView) trackJoinVoice(ch discord.Channel) {
	if c.sci == nil {
		return
	}
	p := map[string]any{"channel_bitrate": ch.VoiceBitrate, "was_moved": false}
	c.channelProps(ch, p)
	c.sci.track("join_voice_channel", p, "", 0)
}

func (c *chatView) trackVoiceDisconnect(chID discord.ChannelID) {
	if c.sci == nil || !chID.IsValid() {
		return
	}
	c.sci.track("voice_disconnect", map[string]any{"channel_id": chID.String()}, "", 0)
}

func (c *chatView) trackReady(guilds, channels int) {
	if c.sci != nil {
		c.sci.track("ready_payload_received", map[string]any{"num_guilds": guilds, "num_guild_channels": channels}, "", 0)
		c.trackSessionStart()
	}
}

// qosCommand is op 40, QOS_HEARTBEAT, which the web client sends with each
// session heartbeat.
type qosCommand struct {
	Seq int `json:"seq"`
	QoS struct {
		Active  bool     `json:"active"`
		Ver     int      `json:"ver"`
		Reasons []string `json:"reasons"`
	} `json:"qos"`
}

func (*qosCommand) Op() ws.OpCode           { return 40 }
func (*qosCommand) EventType() ws.EventType { return "" }

// watchHeartbeat sends client_heartbeat and QOS_HEARTBEAT every 15 minutes
// while the client is active, foregrounded or in a call, as the web
// client's session heartbeat scheduler does.
func (c *chatView) watchHeartbeat() func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		seq := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			c.u.post(func() {
				if c.closed || c.sci == nil {
					return
				}
				var reasons []string
				if c.u.focused {
					reasons = append(reasons, "foregrounded")
				}
				if c.u.voiceChannel().IsValid() {
					reasons = append(reasons, "rtc_connected")
				}
				if len(reasons) == 0 {
					return
				}
				c.sci.track("client_heartbeat", map[string]any{
					"client_heartbeat_version": 31,
					"is_idle":                  c.idle,
					"idle_duration_ms":         systemIdle().Milliseconds(),
					"is_afk":                   c.idle,
					"is_system_suspended":      false,
					"is_system_locked":         false,
					"uses_client_mods":         false,
				}, "", 0)
				if c.u.app.NoTelemetry {
					return
				}
				seq++
				q := &qosCommand{Seq: seq}
				q.QoS.Active, q.QoS.Ver, q.QoS.Reasons = true, 31, reasons
				st := c.st
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					st.SendGateway(ctx, q)
				}()
			})
		}
	}()
	return func() { close(stop) }
}

// sciStart is when Linkcord started, for app_ui_viewed's time since the app
// opened.
var sciStart = time.Now()

// sciOnce keeps what the events below send once per launch or compare with
// what they sent before. All of it is used on the UI goroutine.
var sciOnce struct {
	uiViewed bool
	// loginMethod is how the user just signed in, sent once the session
	// is ready.
	loginMethod string
	// speakChannel is the voice channel the speaking state is of, and
	// meSpeaking and othersSpeaking whether the user and anyone else were
	// speaking there.
	speakChannel               discord.ChannelID
	meSpeaking, othersSpeaking bool
	// activities are the last activity_updated sent by application, and
	// activityAt when.
	activities map[string]string
	activityAt map[string]time.Time
	// games are the games running, by ID, with their session.
	games map[string]*gameSession
}

// gameSession is a game running, for running_game_heartbeat.
type gameSession struct {
	id   string
	p    playing
	last time.Time
}

// trackSpeaking sends start_speaking when the user starts speaking and
// start_listening when someone else does, as the web client's speaking
// tracker, throttled by server for 15 minutes.
func (c *chatView) trackSpeaking() {
	if c.sci == nil {
		return
	}
	u := c.u
	chID := u.voiceChannel()
	if chID != sciOnce.speakChannel {
		sciOnce.speakChannel, sciOnce.meSpeaking, sciOnce.othersSpeaking = chID, false, false
	}
	if !chID.IsValid() || !u.voiceStatus().Joined {
		return
	}
	ch, err := c.st.Cabinet.Channel(chID)
	if err != nil {
		return
	}
	var users []discord.UserID
	if ch.GuildID.IsValid() {
		if states, err := c.st.Cabinet.VoiceStates(ch.GuildID); err == nil {
			for _, vs := range states {
				if vs.ChannelID == chID {
					users = append(users, vs.UserID)
				}
			}
		}
	} else {
		for _, r := range ch.DMRecipients {
			users = append(users, r.ID)
		}
	}
	me := u.voiceSpeaking(c.me.ID)
	others := false
	for _, id := range users {
		if id != c.me.ID && u.voiceSpeaking(id) {
			others = true
		}
	}
	server := ""
	var guild any
	if ch.GuildID.IsValid() {
		server = ch.GuildID.String()
		guild = server
	}
	base := func() map[string]any {
		p := map[string]any{"channel": chID.String(), "server": guild, "channel_id": chID.String(), "guild_id": guild}
		if ch.GuildID.IsValid() {
			p["voice_state_count"] = len(users)
		}
		var name, id any
		if len(c.games) > 0 {
			g := c.games[0].game
			name = g.Name
			if !strings.HasPrefix(g.ID, customPrefix) {
				id = g.ID
			}
		}
		p["game_name"], p["game_id"] = name, id
		return p
	}
	if me != sciOnce.meSpeaking {
		sciOnce.meSpeaking = me
		if me {
			p := base()
			p["mode"] = "VOICE_ACTIVITY"
			if u.voiceSettings().PushToTalk {
				p["mode"] = "PUSH_TO_TALK"
			}
			p["priority"] = false
			c.sci.track("start_speaking", p, "start_speaking:"+server, 15*time.Minute)
		}
	}
	if others != sciOnce.othersSpeaking {
		sciOnce.othersSpeaking = others
		if others && !u.voice.deafened {
			p := base()
			p["mute"] = u.voice.muted
			p["anyone_priority"] = false
			c.sci.track("start_listening", p, "start_listening:"+server, 15*time.Minute)
		}
	}
}

// trackStreamEnded sends video_stream_ended when the user stops their Go
// Live stream or watching one, with the reason the web client gives a
// connection it closes itself. The per-stream statistics it adds are the
// media engine's and are not known here.
func (c *chatView) trackStreamEnded() {
	if c.sci != nil {
		c.sci.track("video_stream_ended", map[string]any{"reason": "Force Close"}, "", 0)
	}
}

// friendTabIDs are the web client's names of the friends tabs.
var friendTabIDs = [friendTabCount]string{"ONLINE", "ALL", "PENDING", "BLOCKED", "ADD_FRIEND"}

// trackFriendsViewed sends friends_list_viewed as the friends page opens,
// throttled by tab for 15 minutes.
func (c *chatView) trackFriendsViewed() {
	if c.sci == nil || c.demo {
		return
	}
	tab := ""
	if c.friends.tab >= 0 && c.friends.tab < friendTabCount {
		tab = friendTabIDs[c.friends.tab]
	}
	friends, in, out := 0, 0, 0
	c.st.RelationshipState.Each(func(_ discord.UserID, t discord.RelationshipType) bool {
		switch t {
		case discord.FriendRelationship:
			friends++
		case discord.IncomingFriendRequest:
			in++
		case discord.SentFriendRequest:
			out++
		}
		return false
	})
	c.sci.track("friends_list_viewed", map[string]any{
		"tab_opened": tab, "num_friends": friends,
		"num_incoming_requests": in, "num_outgoing_requests": out,
	}, "friends_list_viewed:"+tab, 15*time.Minute)
}

// trackPickerOpened sends expression_picker_opened as the emoji, sticker
// or GIF picker opens; widthDp is its width in CSS pixels. The reaction
// picker is a different event on the web client, not sent here.
func (c *chatView) trackPickerOpened(p *emojiPicker, widthDp int) {
	if c.sci == nil || p.reacting() {
		return
	}
	tab := map[int]string{tabEmoji: "emoji", tabGIFs: "gif", tabStickers: "sticker"}[p.tab]
	props := map[string]any{"width": widthDp, "tab": tab, "badged": false}
	if c.selChannel != nil {
		c.channelProps(*c.selChannel, props)
	}
	c.sci.track("expression_picker_opened", props, "", 0)
}

// trackOpenModal sends open_modal with the web client's modal type.
func (c *chatView) trackOpenModal(props map[string]any) {
	if c.sci != nil {
		c.sci.track("open_modal", props, "", 0)
	}
}

// trackMediaViewer is open_modal for the media viewer, in the open chat.
func (c *chatView) trackMediaViewer() {
	p := map[string]any{"type": "Media Viewer"}
	if ch := c.selChannel; ch != nil {
		p["channel_id"], p["channel_type"] = ch.ID.String(), int(ch.Type)
		if ch.GuildID.IsValid() {
			p["guild_id"] = ch.GuildID.String()
		}
	}
	c.trackOpenModal(p)
}

// trackStreamPreview sends open_popout for the card of a stream shown on
// hovering its streamer in the channel list.
func (c *chatView) trackStreamPreview(user discord.UserID) {
	if c.sci != nil {
		c.sci.track("open_popout", map[string]any{"type": "Stream Preview", "other_user_id": user.String(), "is_streaming": true}, "", 0)
	}
}

// noteLogin keeps how the user signed in, for login_successful once the
// session is ready. The web client sends it for QR code sign-ins only.
func noteLogin(method string) { sciOnce.loginMethod = method }

// trackSessionStart sends, once the session is ready, login_successful
// after a QR code sign-in and app_ui_viewed once per launch as the chat
// shows.
func (c *chatView) trackSessionStart() {
	if c.sci == nil {
		return
	}
	if sciOnce.loginMethod == "remote_auth" {
		c.sci.track("login_successful", map[string]any{
			"source": "QR Code Login", "gift_code_sku_id": nil, "is_new_user": false, "login_method": "remote_auth",
		}, "", 0)
	}
	sciOnce.loginMethod = ""
	if sciOnce.uiViewed {
		return
	}
	sciOnce.uiViewed = true
	// Posted, it runs after the ready event opened the last chat.
	c.u.post(func() {
		if c.sci == nil {
			return
		}
		screen := "friends"
		if c.selChannel != nil {
			screen = "guild_channel"
		}
		c.sci.track("app_ui_viewed", map[string]any{
			"load_id": newUUID(), "screen_name": screen,
			"duration_ms_since_app_opened": time.Since(sciStart).Milliseconds(),
		}, "", 0)
	})
}

// trackResumed sends connection_resumed as the gateway resumes, for half
// of the resumes as the web client samples it for users not staff. The
// resume timings are the gateway's own and are not known here.
func (c *chatView) trackResumed() {
	if c.sci != nil && rand.IntN(2) == 0 {
		c.sci.track("connection_resumed", map[string]any{}, "", 0)
	}
}

// trackActivityUpdated sends activity_updated as a program shows its Rich
// Presence, at most every minute per application and not again unchanged.
func (c *chatView) trackActivityUpdated(clientID string, activity json.RawMessage) {
	if c.sci == nil {
		return
	}
	var a struct {
		Type              *int    `json:"type"`
		Name              string  `json:"name"`
		Details           string  `json:"details"`
		State             string  `json:"state"`
		StatusDisplayType *int    `json:"status_display_type"`
		DetailsURL        *string `json:"details_url"`
		StateURL          *string `json:"state_url"`
		Instance          *bool   `json:"instance"`
		Timestamps        *struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"timestamps"`
		Assets *struct {
			LargeImage *string `json:"large_image"`
			SmallImage *string `json:"small_image"`
			LargeText  *string `json:"large_text"`
			SmallText  *string `json:"small_text"`
			LargeURL   *string `json:"large_url"`
			SmallURL   *string `json:"small_url"`
		} `json:"assets"`
		Party *struct {
			ID   *string `json:"id"`
			Size []int   `json:"size"`
		} `json:"party"`
		Secrets *struct {
			Join  *string `json:"join"`
			Match *string `json:"match"`
		} `json:"secrets"`
		Buttons []struct {
			Label string `json:"label"`
		} `json:"buttons"`
	}
	if json.Unmarshal(activity, &a) != nil {
		return
	}
	typ := 0
	if a.Type != nil {
		typ = *a.Type
	}
	name := a.Name
	if app := c.app(clientID); app != nil && app.name != "" {
		name = app.name
	}
	p := map[string]any{"application_id": clientID, "type": typ, "name": name, "details": a.Details, "state": a.State}
	if a.StatusDisplayType != nil {
		p["status_display_type"] = *a.StatusDisplayType
	}
	if a.Instance != nil {
		p["instance"] = *a.Instance
	}
	urls := a.StateURL != nil || a.DetailsURL != nil
	if as := a.Assets; as != nil {
		if as.LargeText != nil {
			p["large_text"] = *as.LargeText
		}
		if as.SmallText != nil {
			p["small_text"] = *as.SmallText
		}
		urls = urls || as.LargeURL != nil || as.SmallURL != nil
		p["has_images"] = as.LargeImage != nil || as.SmallImage != nil
	}
	p["has_urls"] = urls
	for i, b := range a.Buttons {
		if i < 2 {
			p["button_"+strconv.Itoa(i+1)+"_label"] = b.Label
		}
	}
	if ts := a.Timestamps; ts != nil {
		for k, v := range map[string]float64{"start": ts.Start, "end": ts.End} {
			if v > 0 {
				// Programs give seconds or milliseconds.
				if v < 1e12 {
					v *= 1000
				}
				p[k] = time.UnixMilli(int64(v)).UTC().Format("2006-01-02T15:04:05.000Z")
			}
		}
	}
	if s := a.Secrets; s != nil {
		p["has_match_secret"], p["has_join_secret"] = s.Match != nil, s.Join != nil
	}
	if pt := a.Party; pt != nil {
		if len(pt.Size) == 2 && pt.Size[1] > 0 {
			p["party_max"] = pt.Size[1]
		}
		if pt.ID != nil {
			p["party_id"] = *pt.ID
		}
	}
	// The web client throttles it by application for a minute and drops
	// one the same as the last one sent.
	enc, _ := json.Marshal(p)
	if sciOnce.activities == nil {
		sciOnce.activities, sciOnce.activityAt = make(map[string]string), make(map[string]time.Time)
	}
	if time.Since(sciOnce.activityAt[clientID]) < time.Minute || sciOnce.activities[clientID] == string(enc) {
		return
	}
	sciOnce.activities[clientID], sciOnce.activityAt[clientID] = string(enc), time.Now()
	c.sci.track("activity_updated", p, "", 0)
}

// gameHeartbeatEvery is how often the web client sends
// running_game_heartbeat while a game runs.
const gameHeartbeatEvery = 5 * time.Minute

// trackGames sends running_game_heartbeat as games start and stop, the
// first and last heartbeats of their sessions; watchGameHeartbeats sends
// the ones between.
func (c *chatView) trackGames(now []playing) {
	if sciOnce.games == nil {
		sciOnce.games = make(map[string]*gameSession)
	}
	running := make(map[string]bool, len(now))
	for _, p := range now {
		running[p.game.ID] = true
		if _, ok := sciOnce.games[p.game.ID]; !ok {
			s := &gameSession{id: newUUID(), p: p, last: time.Now()}
			sciOnce.games[p.game.ID] = s
			c.gameHeartbeat(s, true, false)
		}
	}
	for id, s := range sciOnce.games {
		if !running[id] {
			c.gameHeartbeat(s, false, true)
			delete(sciOnce.games, id)
		}
	}
}

// watchGameHeartbeats sends running_game_heartbeat every 5 minutes for
// each game running, until the chat closes.
func (c *chatView) watchGameHeartbeats() {
	stop := c.done
	go func() {
		t := time.NewTicker(gameHeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			c.u.post(func() {
				if c.closed {
					return
				}
				for _, s := range sciOnce.games {
					c.gameHeartbeat(s, false, false)
				}
			})
		}
	}()
}

func (c *chatView) gameHeartbeat(s *gameSession, first, final bool) {
	t := time.Now()
	d := t.Sub(s.last).Milliseconds()
	s.last = t
	if first {
		d = 0
	}
	if c.sci == nil {
		return
	}
	var id any
	if !strings.HasPrefix(s.p.game.ID, customPrefix) {
		id = s.p.game.ID
	}
	state := "hidden"
	if c.u.focused {
		state = "focused"
	} else if !c.u.minimized {
		state = "visible"
	}
	c.sci.track("running_game_heartbeat", map[string]any{
		"game_id": id, "game_name": s.p.game.Name, "game_detection_enabled": true,
		"initial_heartbeat": first, "final_heartbeat": final,
		"game_session_id": s.id, "duration_tracked_ms": d,
		"discord_window_state": state,
	}, "", 0)
}

// trackDisabledSounds sends local_settings_updated with the sounds turned
// off, as the web client does when one is switched.
func (c *chatView) trackDisabledSounds() {
	if c.sci == nil {
		return
	}
	s := c.u.voiceSettings()
	off := []string{}
	for _, sc := range soundChoices {
		if s.NoSounds || s.SoundOff[sc.name] {
			off = append(off, sc.name)
		}
	}
	c.sci.track("local_settings_updated", map[string]any{"disabled_sounds": off}, "", 0)
}

// guildNotifyProps are the web client's notification settings properties
// of a server.
func guildNotifyProps(s gateway.UserGuildSetting) map[string]any {
	var until any
	if s.MuteConfig != nil && s.MuteConfig.EndTime.IsValid() {
		until = s.MuteConfig.EndTime.Time().UnixMilli()
	}
	var level any
	switch s.Notifications {
	case gateway.AllNotifications:
		level = "All"
	case gateway.OnlyMentions:
		level = "Mentions"
	case gateway.NoNotifications:
		level = "Nothing"
	}
	return map[string]any{
		"guild_suppress_everyone": s.SuppressEveryone, "guild_suppress_roles": s.SuppressRoles,
		"guild_is_muted": s.Muted, "guild_muted_until": until,
		"guild_receive_mobile_push": s.MobilePush, "guild_message_notification_settings": level,
	}
}

// trackGuildNotify sends notification_settings_updated for a change of the
// server's notification settings from old to now, with the old values of
// those changed and the web client's label of the change.
func (c *chatView) trackGuildNotify(guildID discord.GuildID, old, now gateway.UserGuildSetting) {
	if c.sci == nil {
		return
	}
	p := guildNotifyProps(now)
	was := guildNotifyProps(old)
	for k, v := range was {
		if v != p[k] {
			p[k+"_old"] = v
		}
	}
	label := ""
	switch {
	case old.Muted != now.Muted:
		label = map[bool]string{true: "muted", false: "unmuted"}[now.Muted]
	case old.SuppressEveryone != now.SuppressEveryone:
		label = map[bool]string{true: "enabled suppress everyone", false: "disabled suppress everyone"}[now.SuppressEveryone]
	case old.SuppressRoles != now.SuppressRoles:
		label = map[bool]string{true: "enabled suppress roles", false: "disabled suppress roles"}[now.SuppressRoles]
	case old.Notifications != now.Notifications:
		label = map[gateway.UserNotification]string{
			gateway.AllNotifications: "notifications set to all messages",
			gateway.OnlyMentions:     "notifications set to mentions",
			gateway.NoNotifications:  "notifications set to nothing",
		}[now.Notifications]
		if label == "" {
			label = "notifications set to the default"
		}
	}
	p["update_type"], p["label"] = "guild", label
	c.guildProps(guildID, p)
	c.sci.track("notification_settings_updated", p, "", 0)
}

// guildUnreadStatuses is ack_messages' guild_unread_statuses: for each
// server, "<id>,<unread>,<mentions>,<muted>,<unread setting>", the setting
// being 1 for all messages and 2 for mentions as the server's default
// notifications resolve it.
func (c *chatView) guildUnreadStatuses() []string {
	guilds, err := c.st.Cabinet.Guilds()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(guilds))
	for _, g := range guilds {
		muted := c.st.MutedState != nil && c.st.MutedState.Guild(g.ID, false)
		setting := 2
		if g.Notification == discord.AllMessages {
			setting = 1
		}
		out = append(out, g.ID.String()+","+strconv.FormatBool(c.guildUnread(g.ID) != ningen.ChannelRead)+","+
			strconv.Itoa(c.guildMentions(g.ID))+","+strconv.FormatBool(muted)+","+strconv.Itoa(setting))
	}
	return out
}
