package defaultstore

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
)

type atomicMap[K comparable, V any] struct {
	value atomic.Pointer[sync.Map]
	new   func() V
	mu    sync.Mutex
}

func newAtomicMap[K comparable, V any](new func() V) *atomicMap[K, V] {
	m := &atomicMap[K, V]{new: new}
	m.Reset()
	return m
}

func (m *atomicMap[K, V]) Reset() {
	m.value.Store(new(sync.Map))
}

func (m *atomicMap[K, V]) Load(key K) (V, bool) {
	value, ok := m.value.Load().Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return value.(V), true
}

func (m *atomicMap[K, V]) LoadOrStore(key K) (V, bool) {
	if value, ok := m.Load(key); ok {
		return value, true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if value, ok := m.Load(key); ok {
		return value, true
	}
	value := m.new()
	m.value.Load().Store(key, value)
	return value, false
}

// guildMap stores values of type V per guild, keyed by K.
type guildMap[K comparable, V any] struct {
	guilds *atomicMap[discord.GuildID, *guildValues[K, V]]
}

type guildValues[K comparable, V any] struct {
	mut    sync.RWMutex
	values map[K]V
}

func newGuildMap[K comparable, V any]() guildMap[K, V] {
	return guildMap[K, V]{
		guilds: newAtomicMap[discord.GuildID](func() *guildValues[K, V] {
			return &guildValues[K, V]{values: make(map[K]V, 1)}
		}),
	}
}

func (m guildMap[K, V]) reset() error {
	m.guilds.Reset()
	return nil
}

func (m guildMap[K, V]) get(guildID discord.GuildID, key K) (*V, error) {
	gv, ok := m.guilds.Load(guildID)
	if !ok {
		return nil, store.ErrNotFound
	}

	gv.mut.RLock()
	defer gv.mut.RUnlock()

	v, ok := gv.values[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &v, nil
}

func (m guildMap[K, V]) all(guildID discord.GuildID) ([]V, error) {
	gv, ok := m.guilds.Load(guildID)
	if !ok {
		return nil, store.ErrNotFound
	}

	gv.mut.RLock()
	defer gv.mut.RUnlock()

	return slices.AppendSeq(make([]V, 0, len(gv.values)), maps.Values(gv.values)), nil
}

func (m guildMap[K, V]) set(guildID discord.GuildID, key K, v *V, update bool) error {
	gv, _ := m.guilds.LoadOrStore(guildID)

	gv.mut.Lock()
	if _, ok := gv.values[key]; !ok || update {
		gv.values[key] = *v
	}
	gv.mut.Unlock()

	return nil
}

func (m guildMap[K, V]) remove(guildID discord.GuildID, key K) error {
	gv, ok := m.guilds.Load(guildID)
	if !ok {
		return nil
	}

	gv.mut.Lock()
	delete(gv.values, key)
	gv.mut.Unlock()

	return nil
}
