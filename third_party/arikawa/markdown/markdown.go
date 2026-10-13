package markdown

import (
	"sync"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state/store"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var (
	messageCtx = parser.NewContextKey()
	sessionCtx = parser.NewContextKey()
	parserPool = sync.Pool{
		New: func() any { return newPooledParser() },
	}
)

type pooledParser struct {
	parser.Parser
	emoji *emoji
}

func newPooledParser() *pooledParser {
	emojiParser := &emoji{}
	return &pooledParser{
		Parser: parser.NewParser(
			parser.WithBlockParsers(blockParsers()...),
			parser.WithInlineParsers(inlineParsers(emojiParser)...),
		),
		emoji: emojiParser,
	}
}

func parse(content []byte, opts ...parser.ParseOption) ast.Node {
	p := parserPool.Get().(*pooledParser)
	*p.emoji = emoji{}
	root := p.Parse(text.NewReader(content), opts...)
	parserPool.Put(p)
	return root
}

// ParseWithMessage parses the given byte slice with the Discord state and the
// Message as source for the ast nodes.
func ParseWithMessage(b []byte, s store.Cabinet, m *discord.Message) ast.Node {
	// Context to pass down messages:
	ctx := parser.NewContext()
	ctx.Set(messageCtx, m)
	ctx.Set(sessionCtx, &s)

	return parse(b, parser.WithContext(ctx))
}

// Parse parses the given byte slice with extra options.
func Parse(content []byte, opts ...parser.ParseOption) ast.Node {
	return parse(content, opts...)
}

func getMessage(pc parser.Context) *discord.Message {
	if v := pc.Get(messageCtx); v != nil {
		return v.(*discord.Message)
	}
	return nil
}
func getSession(pc parser.Context) *store.Cabinet {
	if v := pc.Get(sessionCtx); v != nil {
		return v.(*store.Cabinet)
	}
	return nil
}
