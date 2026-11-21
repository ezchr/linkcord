package gui

import (
	"container/list"
	"sync"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
)

// The state keeps the last messages of every channel a message arrives in,
// for as long as Linkcord runs: on an account in many busy servers, after
// hours, that was thousands of channels and hundreds of megabytes. recentStore
// keeps them for the channels with the latest activity only (a message set in
// them, or their messages asked for), the others are let go and asked for
// again when opened. The open channel and the busy ones stay, so a message
// deleted in them is still known (deleted.go).
const (
	storeMessagesPerChannel = 100
	storeChannels           = 300
)

type recentStore struct {
	inner *defaultstore.Message
	max   int

	mu    sync.Mutex
	order *list.List // of discord.ChannelID, the latest at the front
	at    map[discord.ChannelID]*list.Element
}

var _ store.MessageStore = (*recentStore)(nil)

func newRecentStore(perChannel, channels int) *recentStore {
	return &recentStore{
		inner: defaultstore.NewMessage(perChannel),
		max:   channels,
		order: list.New(),
		at:    make(map[discord.ChannelID]*list.Element),
	}
}

// touch makes ch the latest channel, and lets the oldest go past max.
func (s *recentStore) touch(ch discord.ChannelID) {
	s.mu.Lock()
	if e, ok := s.at[ch]; ok {
		s.order.MoveToFront(e)
		s.mu.Unlock()
		return
	}
	s.at[ch] = s.order.PushFront(ch)
	var gone []discord.ChannelID
	for s.order.Len() > s.max {
		e := s.order.Back()
		id := e.Value.(discord.ChannelID)
		s.order.Remove(e)
		delete(s.at, id)
		gone = append(gone, id)
	}
	s.mu.Unlock()
	for _, id := range gone {
		s.drop(id)
	}
}

// drop lets a channel's messages go.
func (s *recentStore) drop(ch discord.ChannelID) {
	msgs, err := s.inner.Messages(ch)
	if err != nil {
		return
	}
	for _, m := range msgs {
		_ = s.inner.MessageRemove(ch, m.ID)
	}
}

func (s *recentStore) Reset() error {
	s.mu.Lock()
	s.order.Init()
	clear(s.at)
	s.mu.Unlock()
	return s.inner.Reset()
}

func (s *recentStore) MaxMessages() int { return s.inner.MaxMessages() }

func (s *recentStore) Message(ch discord.ChannelID, id discord.MessageID) (*discord.Message, error) {
	return s.inner.Message(ch, id)
}

func (s *recentStore) Messages(ch discord.ChannelID) ([]discord.Message, error) {
	msgs, err := s.inner.Messages(ch)
	if err == nil && len(msgs) > 0 {
		s.touch(ch)
	}
	return msgs, err
}

func (s *recentStore) MessageSet(m *discord.Message, update bool) error {
	if !update {
		s.touch(m.ChannelID)
	}
	return s.inner.MessageSet(m, update)
}

func (s *recentStore) MessageRemove(ch discord.ChannelID, id discord.MessageID) error {
	return s.inner.MessageRemove(ch, id)
}
