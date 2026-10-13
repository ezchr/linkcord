package markdown

import (
	"strconv"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type listItemType int

const (
	notList listItemType = iota
	bulletList
	orderedList
)

var skipListParserKey = parser.NewContextKey()
var emptyListItemWithBlankLines = parser.NewContextKey()
var listItemFlagValue any = true

// Same as
// `^(([ ]*)([\-\*\+]))(\s+.*)?\n?$`.FindSubmatchIndex or
// `^(([ ]*)(\d{1,9}[\.\)]))(\s+.*)?\n?$`.FindSubmatchIndex.
func parseListItem(line []byte) ([6]int, listItemType) {
	i := 0
	l := len(line)
	ret := [6]int{}
	for ; i < l && line[i] == ' '; i++ {
		c := line[i]
		if c == '\t' {
			return ret, notList
		}
	}
	if i > 3 {
		return ret, notList
	}
	ret[0] = 0
	ret[1] = i
	ret[2] = i
	var typ listItemType
	switch {
	case i >= l:
		return ret, notList
	case line[i] == '-' || line[i] == '*':
		i++
		ret[3] = i
		typ = bulletList
	default:
		for ; i < l && util.IsNumeric(line[i]); i++ {
		}
		ret[3] = i
		if ret[3] == ret[2] || ret[3]-ret[2] > 9 {
			return ret, notList
		}
		if i < l && (line[i] == '.' || line[i] == ')') {
			i++
			ret[3] = i
		} else {
			return ret, notList
		}
		typ = orderedList
	}
	if i < l && line[i] != '\n' {
		w, _ := util.IndentWidth(line[i:], 0)
		if w == 0 {
			return ret, notList
		}
	}
	if i >= l {
		ret[4] = -1
		ret[5] = -1
		return ret, typ
	}
	ret[4] = i
	ret[5] = len(line)
	if line[ret[5]-1] == '\n' && line[i] != '\n' {
		ret[5]--
	}
	return ret, typ
}

func matchesListItem(source []byte, strict bool) ([6]int, listItemType) {
	m, typ := parseListItem(source)
	if typ != notList && (!strict || strict && m[1] < 4) {
		return m, typ
	}
	return m, notList
}

func calcListOffset(source []byte, match [6]int) int {
	var offset int
	if match[4] < 0 || util.IsBlank(source[match[4]:]) { // list item starts with a blank line
		offset = 1
	} else {
		offset, _ = util.IndentWidth(source[match[4]:], match[4])
		if offset > 4 { // offseted codeblock
			offset = 1
		}
	}
	return offset
}

func lastOffset(node ast.Node) int {
	lastChild := node.LastChild()
	if lastChild != nil {
		return lastChild.(*ast.ListItem).Offset
	}
	return 0
}

type listParser struct {
}

func newListParser() parser.BlockParser {
	return (*listParser)(nil)
}

func (b *listParser) Trigger() []byte {
	return []byte{'-', '*', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9'}
}

func (b *listParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	last := pc.LastOpenedBlock().Node
	if _, lok := last.(*ast.List); lok || pc.Get(skipListParserKey) != nil {
		pc.Set(skipListParserKey, nil)
		return nil, parser.NoChildren
	}
	line, _ := reader.PeekLine()
	match, typ := matchesListItem(line, true)
	if typ == notList {
		return nil, parser.NoChildren
	}
	start := -1
	if typ == orderedList {
		number := line[match[2] : match[3]-1]
		start, _ = strconv.Atoi(string(number))
	}

	if ast.IsParagraph(last) && last.Parent() == parent {
		// we allow only lists starting with 1 to interrupt paragraphs.
		if typ == orderedList && start != 1 {
			return nil, parser.NoChildren
		}
		// An empty list item cannot interrupt a paragraph:
		if match[4] < 0 || util.IsBlank(line[match[4]:match[5]]) {
			return nil, parser.NoChildren
		}
	}

	marker := line[match[3]-1]
	node := ast.NewList(marker)
	if start > -1 {
		node.Start = start
	}
	pc.Set(emptyListItemWithBlankLines, nil)
	return node, parser.HasChildren
}

func (b *listParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	return parser.NewListParser().Continue(node, reader, pc)
}

func (b *listParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {
	parser.NewListParser().Close(node, reader, pc)
}

func (b *listParser) CanInterruptParagraph() bool {
	return true
}

func (b *listParser) CanAcceptIndentedLine() bool {
	return false
}

type listItemParser struct {
}

func newListItemParser() parser.BlockParser {
	return (*listItemParser)(nil)
}

func (b *listItemParser) Trigger() []byte {
	return []byte{'-', '*', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9'}
}

func (b *listItemParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	list, lok := parent.(*ast.List)
	if !lok { // list item must be a child of a list
		return nil, parser.NoChildren
	}
	offset := lastOffset(list)
	line, _ := reader.PeekLine()
	match, typ := matchesListItem(line, false)
	if typ == notList {
		return nil, parser.NoChildren
	}
	if match[1]-offset > 3 {
		return nil, parser.NoChildren
	}

	pc.Set(emptyListItemWithBlankLines, nil)

	itemOffset := calcListOffset(line, match)
	node := ast.NewListItem(match[3] + itemOffset)
	if match[4] < 0 || util.IsBlank(line[match[4]:match[5]]) {
		return node, parser.NoChildren
	}

	pos, padding := util.IndentPosition(line[match[4]:], match[4], itemOffset)
	child := match[3] + pos
	reader.AdvanceAndSetPadding(child, padding)
	return node, parser.HasChildren
}

func (b *listItemParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	if util.IsBlank(line) {
		reader.Advance(len(line) - 1)
		return parser.Continue | parser.HasChildren
	}

	offset := lastOffset(node.Parent())
	isEmpty := node.ChildCount() == 0
	indent, _ := util.IndentWidth(line, reader.LineOffset())
	if (isEmpty || indent < offset) && indent < 4 {
		_, typ := matchesListItem(line, true)
		// new list item found
		if typ != notList {
			pc.Set(skipListParserKey, listItemFlagValue)
			return parser.Close
		}
		if !isEmpty {
			return parser.Close
		}
	}
	pos, padding := util.IndentPosition(line, reader.LineOffset(), offset)
	reader.AdvanceAndSetPadding(pos, padding)

	return parser.Continue | parser.HasChildren
}

func (b *listItemParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {
	// nothing to do
}

func (b *listItemParser) CanInterruptParagraph() bool {
	return true
}

func (b *listItemParser) CanAcceptIndentedLine() bool {
	return false
}
