package defaultstore

import (
	"sync"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
)

type User struct {
	mut   sync.RWMutex
	users map[discord.UserID]discord.User
}

var _ store.UserStore = (*User)(nil)

func NewUser() *User {
	return &User{users: make(map[discord.UserID]discord.User)}
}

func (s *User) Reset() error {
	s.mut.Lock()
	s.users = make(map[discord.UserID]discord.User)
	s.mut.Unlock()
	return nil
}

func (s *User) User(id discord.UserID) (*discord.User, error) {
	s.mut.RLock()
	user, ok := s.users[id]
	s.mut.RUnlock()
	if !ok {
		return nil, store.ErrNotFound
	}
	return &user, nil
}

func (s *User) UserSet(users ...discord.User) error {
	s.mut.Lock()
	for _, user := range users {
		s.users[user.ID] = user
	}
	s.mut.Unlock()
	return nil
}
