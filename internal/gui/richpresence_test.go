package gui

import (
	"encoding/json"
	"testing"
)

// TestGatewayActivity makes what a game sets over the RPC into what the
// gateway takes, as Discord's app does.
func TestGatewayActivity(t *testing.T) {
	app := new(rpcApp)
	app.once.Do(func() { app.name, app.assets = "Some Game", map[string]string{"logo": "999"} })
	rpcApps.Store("123", app)
	c := new(chatView)
	out, err := c.gatewayActivity(rpcActivity{clientID: "123", activity: json.RawMessage(`{
		"details": "In a match", "state": "Ranked",
		"timestamps": {"start": 1700000000},
		"assets": {"large_image": "Logo", "large_text": "Map"},
		"buttons": [{"label": "Join", "url": "https://example.com"}]
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Name          string `json:"name"`
		ApplicationID string `json:"application_id"`
		Details       string `json:"details"`
		Timestamps    struct {
			Start int64 `json:"start"`
		} `json:"timestamps"`
		Assets struct {
			LargeImage string `json:"large_image"`
			LargeText  string `json:"large_text"`
		} `json:"assets"`
		Buttons  []string `json:"buttons"`
		Metadata struct {
			ButtonURLs []string `json:"button_urls"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Some Game" || got.ApplicationID != "123" || got.Details != "In a match" {
		t.Errorf("%s", out)
	}
	if got.Timestamps.Start != 1700000000000 {
		t.Errorf("start %d, not in milliseconds", got.Timestamps.Start)
	}
	if got.Assets.LargeImage != "999" || got.Assets.LargeText != "Map" {
		t.Errorf("assets %+v", got.Assets)
	}
	if len(got.Buttons) != 1 || got.Buttons[0] != "Join" || len(got.Metadata.ButtonURLs) != 1 {
		t.Errorf("buttons %v %v", got.Buttons, got.Metadata.ButtonURLs)
	}
}
