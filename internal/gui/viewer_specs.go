package gui

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// noteMediaBytes remembers the file size of the attachment at u.
func (c *chatView) noteMediaBytes(u string, n uint64) {
	if n == 0 {
		return
	}
	if c.mediaBytes == nil {
		c.mediaBytes = make(map[string]uint64)
	}
	c.mediaBytes[u] = n
}

// mediaSpecs is what the fullscreen viewer tells of a picture: its
// resolution, file size if known, and type.
func (c *chatView) mediaSpecs(w, h int, u string) string {
	parts := []string{fmt.Sprintf("%d×%d", w, h)}
	if n := c.mediaBytes[u]; n > 0 {
		parts = append(parts, formatSize(n))
	}
	if k := mediaKind(u); k != "" {
		parts = append(parts, k)
	}
	return strings.Join(parts, " · ")
}

// mediaKind is the file type of the media at u, from its name: "PNG", "GIF".
func mediaKind(u string) string {
	p := u
	if parsed, err := url.Parse(u); err == nil {
		p = parsed.Path
	}
	ext := strings.TrimPrefix(path.Ext(p), ".")
	if ext == "" || len(ext) > 5 {
		return ""
	}
	return strings.ToUpper(ext)
}
