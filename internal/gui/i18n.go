package gui

import (
	"sort"
	"strings"
	"sync"
)

// uiLang is the language of the interface: "" is English, "es" Spanish.
var uiLang string

// tr is s in the language of the interface. Text that is not known, such
// as names and messages, stays as it is.
func tr(s string) string {
	if uiLang != "es" || s == "" {
		return s
	}
	trMu.Lock()
	defer trMu.Unlock()
	if v, ok := trCache[s]; ok {
		return v
	}
	v := translate(s)
	if len(trCache) > 20000 {
		clear(trCache)
	}
	trCache[s] = v
	return v
}

var (
	trMu    sync.Mutex
	trCache = map[string]string{}
	// trHeads and trTails are the texts that begin or end longer ones,
	// longest first.
	trHeads, trTails []string
	trOnce           sync.Once
)

func translate(s string) string {
	if v, ok := spanish[s]; ok {
		return v
	}
	trOnce.Do(func() {
		for k := range spanish {
			if len(k) < 3 {
				continue
			}
			if strings.HasSuffix(k, " ") || strings.HasSuffix(k, "#") || strings.HasSuffix(k, "@") || strings.HasSuffix(k, "“") || strings.HasSuffix(k, "'") {
				trHeads = append(trHeads, k)
			}
			if strings.HasPrefix(k, " ") {
				trTails = append(trTails, k)
			}
		}
		longest := func(l []string) {
			sort.Slice(l, func(i, j int) bool { return len(l[i]) > len(l[j]) })
		}
		longest(trHeads)
		longest(trTails)
	})
	for _, h := range trHeads {
		if strings.HasPrefix(s, h) {
			return spanish[h] + s[len(h):]
		}
	}
	for _, t := range trTails {
		if strings.HasSuffix(s, t) {
			return s[:len(s)-len(t)] + spanish[t]
		}
	}
	return s
}
