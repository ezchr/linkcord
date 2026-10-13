package gui

import (
	"fmt"
	"image"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/x/richtext"
	"github.com/ayn2op/arikawa/v3/discord"
)

// blockKind is a kind of block of a message.
type blockKind int

const (
	blockText blockKind = iota
	blockCode
	blockQuote
	blockHeading
	blockSubtext
)

// mdBlock is a block of a message: a paragraph, a quoted paragraph, a
// heading, or a code block, which is shown as is.
type mdBlock struct {
	kind  blockKind
	level int // of headings: 1 to 3
	text  string
}

var reFence = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*\n)?(.*?)```")

// parseBlocks splits the content of a message into blocks.
func parseBlocks(s string) []mdBlock {
	var blocks []mdBlock
	for {
		loc := reFence.FindStringSubmatchIndex(s)
		if loc == nil {
			break
		}
		blocks = append(blocks, parseLines(s[:loc[0]])...)
		code := strings.Trim(s[loc[4]:loc[5]], "\n")
		blocks = append(blocks, mdBlock{kind: blockCode, text: code})
		s = s[loc[1]:]
	}
	return append(blocks, parseLines(s)...)
}

// parseLines splits text without code blocks into paragraphs, quotes and
// headings, joining consecutive lines of the same kind.
func parseLines(s string) []mdBlock {
	s = strings.Trim(s, "\n")
	if s == "" {
		return nil
	}
	var blocks []mdBlock
	add := func(kind blockKind, level int, line string) {
		if n := len(blocks); n > 0 && blocks[n-1].kind == kind && kind != blockHeading && kind != blockSubtext {
			blocks[n-1].text += "\n" + line
			return
		}
		blocks = append(blocks, mdBlock{kind: kind, level: level, text: line})
	}
	for _, line := range strings.Split(s, "\n") {
		switch {
		case line == ">" || strings.HasPrefix(line, "> "):
			add(blockQuote, 0, strings.TrimPrefix(strings.TrimPrefix(line, ">"), " "))
		case strings.HasPrefix(line, "### "):
			add(blockHeading, 3, line[4:])
		case strings.HasPrefix(line, "## "):
			add(blockHeading, 2, line[3:])
		case strings.HasPrefix(line, "# "):
			add(blockHeading, 1, line[2:])
		case strings.HasPrefix(line, "-# "):
			add(blockSubtext, 0, line[3:])
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			add(blockText, 0, "• "+line[2:])
		default:
			add(blockText, 0, line)
		}
	}
	return blocks
}

// inlineStyle is the formatting in effect at a point of a line.
type inlineStyle struct {
	bold, italic, strike, spoiler bool
}

var (
	reInlineToken = regexp.MustCompile(`^(?:<@!?(\d+)>|<@&(\d+)>|<#(\d+)>|<a?:(\w+):\d+>|<t:(-?\d+)(?::[tTdDfFR])?>|\[([^\]\n]+)\]\(<?(https?://[^)\s>]+)>?\)|<?(https?://[^\s<>]+[^\s<>.,:;"')\]])>?)`)
	// delimiters are checked longest first, so that ** is not taken as *.
	delimiters = []string{"**", "__", "~~", "||", "*", "_", "`"}
)

// spanKind tells what clicking an interactive span does.
const (
	metaURL     = "url"
	metaUser    = "user"
	metaSpoiler = "spoiler"
)

// inlineSpans turns a line of Discord markup into styled spans: formatting,
// code, links, mentions, spoilers and timestamps.
func (c *chatView) inlineSpans(s string, m discord.Message, base richtext.SpanStyle, revealed bool) []richtext.SpanStyle {
	var spans []richtext.SpanStyle
	var buf strings.Builder
	st := inlineStyle{}
	guildID := m.GuildID
	if !guildID.IsValid() && c.selChannel != nil {
		guildID = c.selChannel.GuildID
	}

	style := func() richtext.SpanStyle {
		sp := base
		if st.bold {
			sp.Font.Weight = font.Bold
		}
		if st.italic {
			sp.Font.Style = font.Italic
		}
		if st.strike {
			sp.Color = withAlpha(sp.Color, 0x90)
		}
		return sp
	}
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		sp := style()
		txt := buf.String()
		buf.Reset()
		if st.spoiler && !revealed {
			// A hidden spoiler is a dark bar as long as the text, which
			// reveals the spoilers of the message when clicked.
			sp.Content = strings.Repeat("█", utf8.RuneCountInString(txt))
			sp.Color = colorServers
			sp.Interactive = true
			sp.Set(metaSpoiler, "1")
		} else {
			sp.Content = txt
		}
		spans = append(spans, sp)
	}
	special := func(content string, col colorT, key, value string, bold bool) {
		flush()
		sp := style()
		sp.Content, sp.Color = content, col
		if bold {
			sp.Font.Weight = font.Bold
		}
		if st.spoiler && !revealed {
			sp.Content = strings.Repeat("█", utf8.RuneCountInString(content))
			sp.Color = colorServers
			key, value = metaSpoiler, "1"
		}
		if key != "" {
			sp.Interactive = true
			sp.Set(key, value)
		}
		spans = append(spans, sp)
	}

	for i := 0; i < len(s); {
		rest := s[i:]
		// A backslash shows the next character as is.
		if rest[0] == '\\' && len(rest) > 1 && strings.ContainsRune(`*_~|\`+"`<>[]#-", rune(rest[1])) {
			buf.WriteByte(rest[1])
			i += 2
			continue
		}
		if rest[0] == '<' || rest[0] == '[' || strings.HasPrefix(rest, "http") {
			if mt := reInlineToken.FindStringSubmatch(rest); mt != nil {
				switch {
				case mt[1] != "":
					id, _ := discord.ParseSnowflake(mt[1])
					uid := discord.UserID(id)
					name := c.memberName(guildID, uid, nil)
					for _, u := range m.Mentions {
						if u.ID == uid {
							name = c.memberName(guildID, u.ID, u.Member)
						}
					}
					special("@"+name, colorMention, metaUser, mt[1], false)
				case mt[2] != "":
					id, _ := discord.ParseSnowflake(mt[2])
					name, col := "@rol", colorMention
					if r, err := c.st.Cabinet.Role(guildID, discord.RoleID(id)); err == nil {
						name = "@" + r.Name
						if r.Color != 0 {
							col = rgb(uint32(r.Color))
						}
					}
					special(name, col, "", "", false)
				case mt[3] != "":
					id, _ := discord.ParseSnowflake(mt[3])
					name := "#canal"
					if ch, err := c.st.Cabinet.Channel(discord.ChannelID(id)); err == nil {
						name = "#" + ch.Name
					}
					special(name, colorMention, "", "", false)
				case mt[4] != "":
					special(":"+mt[4]+":", colorMuted, "", "", false)
				case mt[5] != "":
					txt := mt[0]
					if sec, err := strconv.ParseInt(mt[5], 10, 64); err == nil {
						txt = time.Unix(sec, 0).Local().Format("02/01/2006 15:04")
					}
					special(txt, colorText, "", "", false)
				case mt[6] != "":
					special(mt[6], colorLink, metaURL, mt[7], false)
				case mt[8] != "":
					special(mt[8], colorLink, metaURL, mt[8], false)
				}
				i += len(mt[0])
				continue
			}
		}
		matched := false
		for _, d := range delimiters {
			if !strings.HasPrefix(rest, d) {
				continue
			}
			if d == "`" {
				// Inline code shows its content as is, in a mono font.
				if end := strings.Index(rest[1:], "`"); end > 0 {
					flush()
					sp := base
					sp.Font = font.Font{Typeface: "Go Mono"}
					sp.Content = rest[1 : 1+end]
					sp.Color = colorHeader
					if st.spoiler && !revealed {
						sp.Content = strings.Repeat("█", utf8.RuneCountInString(sp.Content))
						sp.Color = colorServers
						sp.Interactive = true
						sp.Set(metaSpoiler, "1")
					}
					spans = append(spans, sp)
					i += end + 2
					matched = true
				}
				break
			}
			flag := map[string]*bool{"**": &st.bold, "__": &st.bold, "~~": &st.strike, "||": &st.spoiler, "*": &st.italic, "_": &st.italic}[d]
			// A delimiter opens only if it closes later on the line, and
			// closes only what it opened.
			if !*flag && !strings.Contains(rest[len(d):], d) {
				break
			}
			// A single _ inside a word, as in snake_case, is not italic.
			if d == "_" && !*flag && i > 0 && isWordByte(s[i-1]) {
				break
			}
			flush()
			*flag = !*flag
			i += len(d)
			matched = true
			break
		}
		if matched {
			continue
		}
		r, size := utf8.DecodeRuneInString(rest)
		buf.WriteRune(r)
		i += size
	}
	flush()
	return spans
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// layoutContent lays out the content of a message with its formatting.
func (c *chatView) layoutContent(gtx layout.Context, m discord.Message) layout.Dimensions {
	th := c.u.th
	state := c.richState(m.ID)
	for {
		span, ev, ok := state.Update(gtx)
		if !ok {
			break
		}
		if ev.Type != richtext.Click {
			continue
		}
		if url, _ := span.Get(metaURL).(string); url != "" {
			openLink(url)
		}
		if id, _ := span.Get(metaUser).(string); id != "" {
			if sf, err := discord.ParseSnowflake(id); err == nil {
				user := discord.User{ID: discord.UserID(sf)}
				if u, err := c.st.Cabinet.User(user.ID); err == nil {
					user = *u
				}
				for _, mu := range m.Mentions {
					if mu.ID == user.ID {
						user = mu.User
					}
				}
				c.openProfile(user, c.selChannel.GuildID)
			}
		}
		if s, _ := span.Get(metaSpoiler).(string); s != "" {
			c.revealed[m.ID] = true
		}
	}

	revealed := c.revealed[m.ID]
	// Re-parse only when the message, its spoilers or the minute change
	// (the minute keeps "5 minutes ago" timestamps and mention names fresh).
	key := fmt.Sprintf("%v|%v|%d|%s", m.EditedTimestamp, revealed, time.Now().Unix()/60, m.Content)
	if state.key != key {
		state.key = key
		state.parts, state.all = c.buildContent(m, revealed)
	}
	// Every block draws from the one state, so that the spans keep their
	// indices from frame to frame.
	all := state.all
	var children []layout.FlexChild
	for bi, part := range state.parts {
		if bi > 0 {
			children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout))
		}
		if part.kind == blockCode {
			txt := part.text
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return rounded(gtx, colorCode, 4, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := label(th, 13, colorText, txt)
							l.Font.Typeface = "Go Mono"
							return l.Layout(gtx)
						})
					})
				})
			}))
			continue
		}
		start, n, quote := part.start, part.n, part.kind == blockQuote
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			text := func(gtx layout.Context) layout.Dimensions {
				return c.richSlice(gtx, state, all, start, start+n)
			}
			if !quote {
				return text(gtx)
			}
			// Quotes have a bar on their left.
			dims := layout.Inset{Left: unit.Dp(12)}.Layout(gtx, text)
			paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(image.Rect(0, 0, gtx.Dp(4), dims.Size.Y), gtx.Dp(2)).Op(gtx.Ops))
			return dims
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// contentPart is a block of a message ready to draw: a code block with its
// text, or formatted text that is spans[start:start+n] of the message.
type contentPart struct {
	kind     blockKind
	text     string
	start, n int
}

// buildContent parses a message into the blocks layoutContent draws.
func (c *chatView) buildContent(m discord.Message, revealed bool) ([]contentPart, []richtext.SpanStyle) {
	var parts []contentPart
	var all []richtext.SpanStyle
	for _, b := range parseBlocks(m.Content) {
		if b.kind == blockCode {
			parts = append(parts, contentPart{kind: blockCode, text: b.text})
			continue
		}
		base := richtext.SpanStyle{Size: 15, Color: colorText}
		switch b.kind {
		case blockHeading:
			base.Size = [...]unit.Sp{0, 22, 19, 17}[b.level]
			base.Font.Weight = font.Bold
			base.Color = colorHeader
		case blockSubtext:
			base.Size, base.Color = 12, colorMuted
		}
		var spans []richtext.SpanStyle
		for li, line := range strings.Split(b.text, "\n") {
			if li > 0 {
				nl := base
				nl.Content = "\n"
				spans = append(spans, nl)
			}
			spans = append(spans, c.inlineSpans(line, m, base, revealed)...)
		}
		if len(spans) == 0 {
			continue
		}
		parts = append(parts, contentPart{kind: b.kind, start: len(all), n: len(spans)})
		all = append(all, spans...)
	}
	return parts, all
}

// richSlice lays out the spans from start to end of all with state, which
// keeps a slot for each span of all so that clicks map back to the span
// under the pointer.
func (c *chatView) richSlice(gtx layout.Context, state *richtextState, all []richtext.SpanStyle, start, end int) layout.Dimensions {
	return richtext.Text(state.block(start), c.u.th.Shaper, all[start:end]...).Layout(gtx)
}

// richtextState holds the interactive state of the formatted text of one
// message, which is split in blocks that each need their own state.
type richtextState struct {
	blocks map[int]*richtext.InteractiveText

	// The parsed content (see layoutContent) and what it was parsed from.
	key   string
	parts []contentPart
	all   []richtext.SpanStyle
}

func (s *richtextState) block(start int) *richtext.InteractiveText {
	b, ok := s.blocks[start]
	if !ok {
		b = new(richtext.InteractiveText)
		s.blocks[start] = b
	}
	return b
}

// Update returns the next event of any block of the message.
func (s *richtextState) Update(gtx layout.Context) (*richtext.InteractiveSpan, richtext.Event, bool) {
	for _, b := range s.blocks {
		if span, ev, ok := b.Update(gtx); ok {
			return span, ev, true
		}
	}
	return nil, richtext.Event{}, false
}

func (c *chatView) richState(id discord.MessageID) *richtextState {
	s, ok := c.richStates[id]
	if !ok {
		s = &richtextState{blocks: make(map[int]*richtext.InteractiveText)}
		c.richStates[id] = s
	}
	return s
}
