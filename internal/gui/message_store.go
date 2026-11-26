package gui

import (
	"container/list"
	"slices"
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
//
// Channels opened recently keep storeMessagesPerChannel messages; the others,
// which only collect what the gateway sends, keep storeMessagesBackground.
//
// The store keeps the messages itself rather than in arikawa's
// defaultstore.Message: that one never forgets a channel, so removing a
// channel's messages one by one left its map entry and a backing array of
// some hundred messages behind, and with it most of the memory (measured:
// 197 MB after 2000 channels went through a store of 300).
const (
	storeMessagesPerChannel = 100
	storeMessagesBackground = 50
	storeChannels           = 300
	storeOpenedChannels     = 20
)

type recentStore struct {
	max        int // messages of an opened channel
	background int // messages of another channel
	channels   int // channels kept
	opened     int // channels kept as opened

	mu       sync.Mutex
	order    *list.List // of discord.ChannelID, the latest at the front
	at       map[discord.ChannelID]*list.Element
	msgs     map[discord.ChannelID][]discord.Message // latest first
	openList *list.List                              // opened channels, the latest at the front
	openAt   map[discord.ChannelID]*list.Element
}

var _ store.MessageStore = (*recentStore)(nil)

func newRecentStore(perChannel, channels int) *recentStore {
	return &recentStore{
		max:        perChannel,
		background: min(perChannel, storeMessagesBackground),
		channels:   channels,
		opened:     storeOpenedChannels,
		order:      list.New(),
		at:         make(map[discord.ChannelID]*list.Element),
		msgs:       make(map[discord.ChannelID][]discord.Message),
		openList:   list.New(),
		openAt:     make(map[discord.ChannelID]*list.Element),
	}
}

// touchLocked makes ch the latest channel, and lets the oldest go past the
// number of channels kept.
func (s *recentStore) touchLocked(ch discord.ChannelID) {
	if e, ok := s.at[ch]; ok {
		s.order.MoveToFront(e)
		return
	}
	s.at[ch] = s.order.PushFront(ch)
	for s.order.Len() > s.channels {
		e := s.order.Back()
		id := e.Value.(discord.ChannelID)
		s.order.Remove(e)
		delete(s.at, id)
		delete(s.msgs, id)
	}
}

// MarkOpened tells the store that ch is shown, so it keeps the full number
// of messages; the channel opened longest ago goes back to the smaller
// number.
func (s *recentStore) MarkOpened(ch discord.ChannelID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.openAt[ch]; ok {
		s.openList.MoveToFront(e)
		return
	}
	s.openAt[ch] = s.openList.PushFront(ch)
	for s.openList.Len() > s.opened {
		e := s.openList.Back()
		id := e.Value.(discord.ChannelID)
		s.openList.Remove(e)
		delete(s.openAt, id)
		s.trimLocked(id)
	}
}

// limitLocked is the number of messages ch keeps.
func (s *recentStore) limitLocked(ch discord.ChannelID) int {
	if _, ok := s.openAt[ch]; ok {
		return s.max
	}
	return s.background
}

// trimLocked cuts ch down to its limit, into a new array so that the memory
// of the old one is given back.
func (s *recentStore) trimLocked(ch discord.ChannelID) {
	msgs := s.msgs[ch]
	if n := s.limitLocked(ch); len(msgs) > n {
		s.msgs[ch] = slices.Clone(msgs[:n])
	}
}

func (s *recentStore) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order.Init()
	clear(s.at)
	s.openList.Init()
	clear(s.openAt)
	s.msgs = make(map[discord.ChannelID][]discord.Message)
	return nil
}

func (s *recentStore) MaxMessages() int { return s.max }

func (s *recentStore) Message(ch discord.ChannelID, id discord.MessageID) (*discord.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.msgs[ch] {
		if m.ID == id {
			return &m, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *recentStore) Messages(ch discord.ChannelID) ([]discord.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs, ok := s.msgs[ch]
	if !ok {
		return nil, store.ErrNotFound
	}
	if len(msgs) > 0 {
		s.touchLocked(ch)
	}
	return slices.Clone(msgs), nil
}

func (s *recentStore) MessageSet(m *discord.Message, update bool) error {
	if s.max <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := m.ChannelID
	msgs := s.msgs[ch]
	if update {
		for i := range msgs {
			if msgs[i].ID == m.ID {
				defaultstore.DiffMessage(m, &msgs[i])
				return nil
			}
		}
		return nil
	}
	s.touchLocked(ch)
	limit := s.limitLocked(ch)
	switch {
	case len(msgs) == 0:
		msgs = append(make([]discord.Message, 0, 8), *m)
	default:
		switch insertPosition(m, msgs) {
		case -1:
			if len(msgs) >= limit {
				// Full: the oldest goes to make room.
				copy(msgs[1:], msgs[:limit-1])
				msgs = msgs[:limit]
				msgs[0] = *m
			} else {
				msgs = slices.Insert(msgs, 0, *m)
			}
		case 1:
			if len(msgs) < limit {
				msgs = append(msgs, *m)
			}
		}
	}
	s.msgs[ch] = msgs
	return nil
}

func (s *recentStore) MessageRemove(ch discord.ChannelID, id discord.MessageID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.msgs[ch]
	for i := range msgs {
		if msgs[i].ID == id {
			s.msgs[ch] = slices.Delete(msgs, i, i+1)
			return nil
		}
	}
	return nil
}

// insertPosition says where m goes in msgs, which are ordered latest first,
// as arikawa's store does: -1 before them all, 1 after them all, 0 nowhere,
// as it is there already or would break the order.
func insertPosition(m *discord.Message, msgs []discord.Message) int {
	t := m.ID.Time()
	first, last := msgs[0].ID.Time(), msgs[len(msgs)-1].ID.Time()
	switch {
	case t.After(first):
		return -1
	case t.Before(last):
		return 1
	case t.Equal(first):
		for i := 0; i < len(msgs) && t.Equal(msgs[i].ID.Time()); i++ {
			if msgs[i].ID == m.ID {
				return 0
			}
		}
		return -1
	case t.Equal(last):
		for i := len(msgs) - 1; i >= 0 && t.Equal(msgs[i].ID.Time()); i-- {
			if msgs[i].ID == m.ID {
				return 0
			}
		}
		return 1
	}
	return 0
}
