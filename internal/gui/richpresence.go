package gui

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ezchr/linkcord/internal/rpc"
)

// presenceCommand sets the user's presence with activities as Discord takes
// them, which Arikawa's activities lack fields of.
type presenceCommand struct {
	Since      int64             `json:"since"`
	Activities []json.RawMessage `json:"activities"`
	Status     discord.Status    `json:"status"`
	AFK        bool              `json:"afk"`
}

func (*presenceCommand) Op() ws.OpCode           { return 3 }
func (*presenceCommand) EventType() ws.EventType { return "" }

// rpcActivity is what a program connected to the RPC shows the user doing.
type rpcActivity struct {
	clientID string
	activity json.RawMessage
}

// startRPC serves Discord's RPC, once, for games and programs to show what
// the user does in them on their profile, as Rich Presence.
func (u *ui) startRPC() {
	if u.rpcStarted {
		return
	}
	u.rpcStarted = true
	name, err := rpc.Start(rpc.Handler{
		User: func() any {
			if me := u.rpcMe.Load(); me != nil {
				return map[string]any{
					"id": me.ID.String(), "username": me.Username, "discriminator": me.Discriminator,
					"global_name": me.DisplayName, "avatar": me.Avatar, "bot": false, "flags": 0, "premium_type": 0,
				}
			}
			return map[string]any{"id": "0", "username": "linkcord", "discriminator": "0"}
		},
		Activity: func(conn, clientID string, activity json.RawMessage) {
			u.post(func() {
				if c := u.chat; c != nil && !c.demo {
					c.setRPCActivity(conn, clientID, activity)
				}
			})
		},
	})
	if err != nil {
		slog.Warn("could not serve Discord's RPC", "err", err)
		return
	}
	slog.Info("serving Discord's RPC", "pipe", name)
}

// setRPCActivity keeps what the program connection conn shows, nil once it
// stops, and shows it on the user's profile.
func (c *chatView) setRPCActivity(conn, clientID string, activity json.RawMessage) {
	if c.rpcActivities == nil {
		c.rpcActivities = make(map[string]rpcActivity)
	}
	if activity == nil {
		if _, ok := c.rpcActivities[conn]; !ok {
			return
		}
		delete(c.rpcActivities, conn)
	} else {
		// One activity per program: one that reconnected without closing
		// left its old connection, which kept the song before.
		for k, a := range c.rpcActivities {
			if k != conn && a.clientID == clientID {
				delete(c.rpcActivities, k)
			}
		}
		c.rpcActivities[conn] = rpcActivity{clientID, activity}
	}
	c.pushPresence()
}

// pushPresence shows on the user's profile what programs show over the RPC
// and the games detected, with the status and custom status they have.
func (c *chatView) pushPresence() {
	acts := make([]rpcActivity, 0, len(c.rpcActivities))
	for _, a := range c.rpcActivities {
		acts = append(acts, a)
	}
	status := discord.OnlineStatus
	var custom []json.RawMessage
	if p, _ := c.st.PresenceStore.Presence(0, c.me.ID); p != nil {
		// The status the user chose stays with the activity.
		if p.Status != "" {
			status = p.Status
		}
		// What the user chose here, over what the sessions say.
		if c.myStatus != "" {
			status = c.myStatus
		}
		// The custom status stays.
		for _, a := range p.Activities {
			if a.Type == discord.CustomActivity {
				if data, err := json.Marshal(a); err == nil {
					custom = append(custom, data)
				}
			}
		}
	}
	games := c.gameActivities()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		all := custom
		for _, a := range acts {
			if data, err := c.gatewayActivity(a); err == nil {
				all = append(all, data)
			} else {
				slog.Warn("could not show a program's activity", "client_id", a.clientID, "err", err)
			}
		}
		all = append(all, games...)
		if all == nil {
			all = []json.RawMessage{}
		}
		if err := c.st.SendGateway(ctx, &presenceCommand{Activities: all, Status: status}); err != nil {
			slog.Warn("could not show the activity", "err", err)
		}
	}()
}

// rpcApps keeps what programs' applications are called, and their pictures'
// IDs by name, asked for once.
var rpcApps sync.Map // client ID → *rpcApp

type rpcApp struct {
	once   sync.Once
	name   string
	assets map[string]string
}

// app returns the application clientID, asking Discord the first time.
func (c *chatView) app(clientID string) *rpcApp {
	v, _ := rpcApps.LoadOrStore(clientID, new(rpcApp))
	app := v.(*rpcApp)
	app.once.Do(func() {
		app.assets = make(map[string]string)
		info, err := c.st.Client.RequestJSON[struct {
			Name string `json:"name"`
		}]("GET", api.Endpoint+"applications/"+clientID+"/rpc")
		if err == nil {
			app.name = info.Name
		}
		assets, err := c.st.Client.RequestJSON[[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}]("GET", api.Endpoint+"oauth2/applications/"+clientID+"/assets")
		if err == nil {
			for _, a := range assets {
				app.assets[strings.ToLower(a.Name)] = a.ID
			}
		}
	})
	return app
}

// asset is a picture of an activity as the gateway takes it: the ID of one
// of the application's, by its name, or a picture from the web, proxied by
// Discord.
func (c *chatView) asset(clientID string, app *rpcApp, key string) string {
	switch {
	case key == "" || strings.HasPrefix(key, "mp:"):
		return key
	case strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://"):
		res, err := c.st.Client.RequestJSON[[]struct {
			Path string `json:"external_asset_path"`
		}]("POST", api.Endpoint+"applications/"+clientID+"/external-assets",
			httputil.WithJSONBody(map[string]any{"urls": []string{key}}))
		if err == nil && len(res) > 0 && res[0].Path != "" {
			return "mp:" + res[0].Path
		}
		return key
	}
	if id, ok := app.assets[strings.ToLower(key)]; ok {
		return id
	}
	return key
}

// gatewayActivity makes what a program set over the RPC into an activity as
// the gateway takes it, as Discord's app does: with the application's name,
// its pictures by ID, times in milliseconds, and buttons split into labels
// and links.
func (c *chatView) gatewayActivity(a rpcActivity) (json.RawMessage, error) {
	var in map[string]any
	if err := json.Unmarshal(a.activity, &in); err != nil {
		return nil, err
	}
	app := c.app(a.clientID)
	out := map[string]any{"application_id": a.clientID, "type": 0, "name": app.name}
	if out["name"] == "" {
		out["name"] = "a game"
	}
	for _, k := range []string{"type", "details", "state", "party", "instance", "flags", "status_display_type", "details_url", "state_url", "name"} {
		if v, ok := in[k]; ok && v != nil && v != "" {
			out[k] = v
		}
	}
	if ts, ok := in["timestamps"].(map[string]any); ok {
		ms := map[string]any{}
		for _, k := range []string{"start", "end"} {
			if f, ok := ts[k].(float64); ok && f > 0 {
				// Programs give seconds, the gateway takes milliseconds.
				if f < 1e12 {
					f *= 1000
				}
				ms[k] = int64(f)
			}
		}
		out["timestamps"] = ms
	}
	if as, ok := in["assets"].(map[string]any); ok {
		assets := map[string]any{}
		for k, v := range as {
			s, _ := v.(string)
			if k == "large_image" || k == "small_image" {
				s = c.asset(a.clientID, app, s)
			}
			if s != "" {
				assets[k] = s
			}
		}
		out["assets"] = assets
	}
	if bs, ok := in["buttons"].([]any); ok {
		var labels, urls []string
		for _, b := range bs {
			if m, ok := b.(map[string]any); ok {
				l, _ := m["label"].(string)
				u, _ := m["url"].(string)
				if l != "" && u != "" {
					labels, urls = append(labels, l), append(urls, u)
				}
			}
		}
		if len(labels) > 0 {
			out["buttons"] = labels
			out["metadata"] = map[string]any{"button_urls": urls}
		}
	}
	return json.Marshal(out)
}
