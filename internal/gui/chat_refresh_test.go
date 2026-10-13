package gui

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestChatCatchupPaginatesPastOneHundred(t *testing.T) {
	all := make([]discord.Message, 350)
	for i := range all {
		all[i] = discord.Message{ID: discord.MessageID(i + 1)}
	}
	calls := 0
	fetch := func(before discord.MessageID) ([]discord.Message, error) {
		calls++
		end := len(all)
		if before != 0 {
			end = int(before) - 1
		}
		start := max(end-100, 0)
		return all[start:end], nil
	}
	fetched, complete, err := fetchChatCatchup(fetch, 100)
	if err != nil || complete || calls != 3 {
		t.Fatalf("catchup: calls=%d, complete=%v, error=%v", calls, complete, err)
	}
	got := reconcileChatHistory(all[:100], fetched, nil, complete)
	if !reflect.DeepEqual(got, all) {
		t.Fatal("missed messages or duplicated the overlapping page")
	}
}

func TestChatCatchupFailureKeepsHistory(t *testing.T) {
	wantErr := errors.New("network unavailable")
	calls := 0
	fetched, _, err := fetchChatCatchup(func(discord.MessageID) ([]discord.Message, error) {
		calls++
		if calls == 2 {
			return nil, wantErr
		}
		page := make([]discord.Message, 100)
		for i := range page {
			page[i].ID = discord.MessageID(101 + i)
		}
		return page, nil
	}, 1)
	if !errors.Is(err, wantErr) || fetched != nil {
		t.Fatal("partial refresh would replace existing history")
	}
}

func TestChatRefreshEditsDeletesAndLiveChanges(t *testing.T) {
	current := []discord.Message{
		{ID: 1, Content: "older history"},
		{ID: 10, Content: "old text"},
		{ID: 11, Content: "deleted while asleep"},
		{ID: 12, Content: "existing"},
	}
	fetched := []discord.Message{
		{ID: 10, Content: "edited while asleep"},
		{ID: 12, Content: "REST snapshot"},
		{ID: 13, Content: "deleted while fetching"},
	}
	live := map[discord.MessageID]*discord.Message{
		12: {ID: 12, Content: "edited during refresh"},
		13: nil,
		14: {ID: 14, Content: "arrived during refresh"},
	}
	got := reconcileChatHistory(current, fetched, live, false)
	want := []discord.Message{
		{ID: 1, Content: "older history"},
		{ID: 10, Content: "edited while asleep"},
		{ID: 12, Content: "edited during refresh"},
		{ID: 14, Content: "arrived during refresh"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history reconciliation:\ngot  %#v\nwant %#v", got, want)
	}
	if got := reconcileChatHistory(current, nil, nil, true); len(got) != 0 {
		t.Fatal("empty authoritative channel retained deleted messages")
	}
}

func TestChatRefreshRecordsGatewayChanges(t *testing.T) {
	c := &chatView{
		u: &ui{focused: true}, demo: true, selGuild: 1,
		selChannel:  &discord.Channel{ID: 10},
		me:          discord.User{ID: 1},
		refreshing:  true,
		refreshLive: make(map[discord.MessageID]*discord.Message),
		messages:    []discord.Message{{ID: 1, ChannelID: 10, Content: "before"}},
	}
	c.apply(&gateway.MessageUpdateEvent{Message: discord.Message{ID: 1, ChannelID: 10, Content: "after"}})
	c.apply(&gateway.MessageCreateEvent{Message: discord.Message{ID: 2, ChannelID: 10, Author: c.me, Content: "new"}})
	c.apply(&gateway.MessageDeleteEvent{ID: 1, ChannelID: 10})
	if message, ok := c.refreshLive[1]; !ok || message != nil {
		t.Fatal("deleted gateway message would be resurrected")
	}
	if message := c.refreshLive[2]; message == nil || message.Content != "new" {
		t.Fatal("message received during refresh would disappear")
	}
	c.recordRefreshEvent(&gateway.MessageDeleteEvent{ID: 2, ChannelID: 99})
	if c.refreshLive[2] == nil {
		t.Fatal("another channel changed the refresh overlay")
	}
}

func TestSuspensionGapUsesWallTime(t *testing.T) {
	previous := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)
	if suspensionGap(previous, previous.Add(15*time.Second)) {
		t.Fatal("normal watchdog interval triggered a refresh")
	}
	if !suspensionGap(previous, previous.Add(8*time.Hour)) {
		t.Fatal("hibernation was not detected")
	}
	if suspensionGap(previous, previous.Add(-time.Hour)) {
		t.Fatal("clock moving backward was mistaken for sleep")
	}
}

func TestChatRefreshWaitsForHistoryLoad(t *testing.T) {
	c := &chatView{
		st:         newState("fixture"),
		selChannel: &discord.Channel{ID: 10},
		loading:    true,
	}
	c.refreshCurrentChat()
	if !c.refreshPending || c.refreshing {
		t.Fatal("resume during initial loading was lost or started an overlapping request")
	}
	c.loading, c.loadingOlder, c.refreshPending = false, true, false
	c.refreshCurrentChat()
	if !c.refreshPending || c.refreshing {
		t.Fatal("resume during older history loading was lost")
	}
}
