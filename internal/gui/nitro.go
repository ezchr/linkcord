package gui

import (
	"os"
	"path/filepath"

	"github.com/ayn2op/arikawa/v3/discord"
)

// What the user may send, by their Nitro and the boosts of the server.

const (
	// maxUpload is the largest file Discord takes from accounts without
	// Nitro, in servers without enough boosts.
	maxUpload = 10 << 20
	// maxMessage is the longest message without Nitro, in characters, and
	// maxNitroMessage with Nitro.
	maxMessage      = 2000
	maxNitroMessage = 4000
)

// nitroUpload is the largest file the user's Nitro lets them send.
func nitroUpload(n discord.UserNitro) int64 {
	switch n {
	case discord.NitroFull:
		return 500 << 20
	case discord.NitroBasic, discord.NitroClassic:
		return 50 << 20
	}
	return maxUpload
}

// boostUpload is the largest file anyone may send in a server of the boost
// level given.
func boostUpload(level discord.NitroBoost) int64 {
	switch {
	case level >= discord.NitroLevel3:
		return 100 << 20
	case level >= discord.NitroLevel2:
		return 50 << 20
	}
	return maxUpload
}

// uploadLimit is the largest file the user may send in the channel open:
// what their Nitro or the server's boosts allow, the more of the two.
func (c *chatView) uploadLimit() int64 {
	limit := nitroUpload(c.me.Nitro)
	if ch := c.selChannel; ch != nil && ch.GuildID.IsValid() && !c.demo {
		if g, err := c.st.Cabinet.Guild(ch.GuildID); err == nil {
			limit = max(limit, boostUpload(g.NitroBoost))
		}
	}
	return limit
}

// messageLimit is the longest message the user may send, in characters.
func (c *chatView) messageLimit() int {
	if c.me.Nitro == discord.NitroFull {
		return maxNitroMessage
	}
	return maxMessage
}

// textAsFile keeps text too long for a message as message.txt, which is
// sent in its place, as Discord does with long text pasted in its box.
func textAsFile(text string) (*pendingFile, error) {
	cleanPastes()
	if err := os.MkdirAll(pasteDir, 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(pasteDir, "t")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "message.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return nil, err
	}
	return &pendingFile{path: path, name: "message.txt", size: int64(len(text))}, nil
}
