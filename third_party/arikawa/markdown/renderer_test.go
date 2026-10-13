package markdown

import (
	"strings"
	"testing"

	_ "embed"

	"github.com/kylelemons/godebug/diff"
)

//go:embed renderer_test.txt
var message string

//go:embed renderer_test_want.txt
var messageWant string

func TestRenderer(t *testing.T) {
	node := Parse([]byte(message))
	buff := strings.Builder{}
	DefaultRenderer.Render(&buff, []byte(message), node)

	if diff := diff.Diff(buff.String(), messageWant); diff != "" {
		t.Error(diff)
	}
}
