//go:build !windows

package hotkey

func layoutName(Key) string { return "" }

// Watcher watches no key outside Windows.
type Watcher struct{}

func Start() *Watcher { return new(Watcher) }

func (*Watcher) Set([]Bind)         {}
func (*Watcher) Record(func(Combo)) {}
func (*Watcher) Close()             {}
