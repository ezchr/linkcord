package gateway

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/json"
	"github.com/ayn2op/arikawa/v3/utils/json/option"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

func TestRequestGuildMembersCommand(t *testing.T) {
	assert := func(cmd Event, data map[string]any) {
		cmdBytes, err := json.Marshal(cmd)
		if err != nil {
			t.Fatal("failed to marshal command:", err)
		}

		var cmdMap map[string]any
		if err := json.Unmarshal(cmdBytes, &cmdMap); err != nil {
			t.Fatal("failed to unmarshal command:", err)
		}

		if !reflect.DeepEqual(cmdMap, data) {
			t.Fatalf("mismatched command, got %#v", cmdMap)
		}
	}

	t.Run("userIDs", func(t *testing.T) {
		cmd := RequestGuildMembersCommand{
			GuildIDs: []discord.GuildID{123},
			UserIDs:  []discord.UserID{456},
		}

		assert(&cmd, map[string]any{
			"guild_id":  []any{"123"},
			"user_ids":  []any{"456"},
			"presences": false,
		})
	})

	t.Run("query_empty", func(t *testing.T) {
		cmd := RequestGuildMembersCommand{
			GuildIDs: []discord.GuildID{123},
			Query:    option.Some(""),
		}

		assert(&cmd, map[string]any{
			"guild_id":  []any{"123"},
			"query":     "",
			"limit":     float64(0),
			"presences": false,
		})
	})

	t.Run("query_nonempty", func(t *testing.T) {
		cmd := RequestGuildMembersCommand{
			GuildIDs: []discord.GuildID{123},
			Query:    option.Some("abc"),
		}

		assert(&cmd, map[string]any{
			"guild_id":  []any{"123"},
			"query":     "abc",
			"limit":     float64(0),
			"presences": false,
		})
	})

	t.Run("both", func(t *testing.T) {
		cmd := RequestGuildMembersCommand{
			GuildIDs: []discord.GuildID{123},
			UserIDs:  []discord.UserID{456},
			Query:    option.Some("abc"),
		}

		// Gateway should never be touched when Marshal fails, so we can just
		// create a zero-value.
		var gateway ws.Gateway

		err := gateway.Send(context.Background(), &cmd)
		if err == nil || !strings.Contains(err.Error(), "neither UserIDs nor Query can be filled") {
			t.Fatal("unexpected error:", err)
		}
	})
}

func TestReadyEventCapabilities(t *testing.T) {
	capabilities := UserSettingsProto | DedupeUserObjects | AuthTokenRefresh
	unmarshalers := NewOpUnmarshalers(capabilities)
	ready := unmarshalers.Lookup(0, "READY")().(*ReadyEvent)
	if err := json.Unmarshal([]byte(`{"auth_token":"new"}`), ready); err != nil {
		t.Fatal("failed to unmarshal READY:", err)
	}
	if ready.Capabilities != capabilities {
		t.Fatalf("unexpected capabilities: %d", ready.Capabilities)
	}
	if ready.AuthToken != "new" {
		t.Fatalf("unexpected auth token: %q", ready.AuthToken)
	}
}

func TestReadyEventVersionedFields(t *testing.T) {
	tests := []struct {
		name         string
		capabilities Capabilities
		payload      string
	}{
		{
			name: "normal",
			payload: `{
				"user": {"id": "1"},
				"read_state": [{"id": "2"}],
				"user_guild_settings": [{"guild_id": "3"}]
			}`,
		},
		{
			name:         "versioned",
			capabilities: VersionedReadStates | VersionedUserGuildSetttings,
			payload: `{
				"user": {"id": "1"},
				"read_state": {"entries": [{"id": "2"}], "partial": false, "version": 4},
				"user_guild_settings": {"entries": [{"guild_id": "3"}], "partial": true, "version": 5}
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unmarshalers := NewOpUnmarshalers(test.capabilities)
			ready := unmarshalers.Lookup(0, "READY")().(*ReadyEvent)
			if err := json.Unmarshal([]byte(test.payload), ready); err != nil {
				t.Fatal("failed to unmarshal READY:", err)
			}
			if len(ready.ReadStates) != 1 || ready.ReadStates[0].ChannelID != 2 {
				t.Fatalf("unexpected read states: %#v", ready.ReadStates)
			}
			if len(ready.UserGuildSettings) != 1 || ready.UserGuildSettings[0].GuildID != 3 {
				t.Fatalf("unexpected user guild settings: %#v", ready.UserGuildSettings)
			}
		})
	}
}

func TestReadyEventPrivateChannelRecipients(t *testing.T) {
	ready := NewOpUnmarshalers(DedupeUserObjects).Lookup(0, "READY")().(*ReadyEvent)
	payload := []byte(`{
		"user": {"id": "1"},
		"users": [{"id": "3", "username": "friend"}],
		"private_channels": [{"id": "2", "recipient_ids": ["3"]}]
	}`)
	if err := json.Unmarshal(payload, ready); err != nil {
		t.Fatal("failed to unmarshal READY:", err)
	}

	recipients := ready.PrivateChannels[0].DMRecipients
	if len(recipients) != 1 || recipients[0].ID != 3 || recipients[0].Username != "friend" {
		t.Fatalf("unexpected recipients: %#v", recipients)
	}
}
