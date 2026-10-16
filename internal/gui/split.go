package gui

import (
	"strings"
	"unicode/utf8"
)

// splitMessage cuts s into messages of at most limit characters, where it
// breaks best: at a line, else at a space, else anywhere. A code block cut
// in two is closed at the end of one and opened again at the start of the
// next, so that both still show as code.
func splitMessage(s string, limit int) []string {
	const fence = "```"
	var out []string
	open := ""
	for s != "" {
		s = open + s
		if utf8.RuneCountInString(s) <= limit {
			if s = strings.TrimRight(s, " \t\n"); s != "" {
				out = append(out, s)
			}
			break
		}
		// Room for a fence to close the block, if one stays open.
		room := limit - len(fence) - 1
		cut := runeOffset(s, room)
		chunk := s[:cut]
		if i := strings.LastIndex(chunk, "\n"); i > len(open) && i > cut/2 {
			chunk = s[:i]
		} else if i := strings.LastIndexAny(chunk, " \t"); i > len(open) && i > cut/2 {
			chunk = s[:i]
		}
		rest := strings.TrimLeft(s[len(chunk):], " \t\n")
		chunk = strings.TrimRight(chunk, " \t\n")
		open = ""
		if strings.Count(chunk, fence)%2 == 1 {
			// The block goes on, with its language, in the next message.
			start := strings.LastIndex(chunk, fence)
			lang := chunk[start+len(fence):]
			if i := strings.IndexByte(lang, '\n'); i >= 0 {
				lang = lang[:i]
			} else {
				lang = ""
			}
			chunk += "\n" + fence
			open = fence + lang + "\n"
		}
		out = append(out, chunk)
		s = rest
	}
	return out
}

// runeOffset is the byte offset of the n-th character of s, or len(s).
func runeOffset(s string, n int) int {
	for i := range s {
		if n == 0 {
			return i
		}
		n--
	}
	return len(s)
}
