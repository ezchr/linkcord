package markdown

import (
	"testing"

	_ "embed"
)

const _fencedInline = "hi ```thing```"
const _fencedInlineHTML = `
Document {
    Paragraph {
        RawText: "hi ` + "```thing```" + `"
        HasBlankPreviousLines: true
        Text: "hi "
        FencedCodeBlock {
            RawText: "thing"
            HasBlankPreviousLines: false
        }
    }
}`

const _fencedLanguage = "hi ```go" + `
package main

func main() {
	fmt.Println("Hello, 世界！")
}
` + "```"
const _fencedLanguageHTML = `<p>hi <pre><code class="language-go">package main

func main() {
	fmt.Println(&quot;Hello, 世界！&quot;)
}</code></pre>
</p>
`

const _fencedBroken = "hi `````go" + `
package main
` + "````"
const _fencedBrokenHTML = `<p>hi <pre><code class="language-go">package main
` + "````" + `</code></pre>
</p>
`

const _fencedMixed = "hii ```go" + `
package main
asdas
# bruh
ased
# hi
` + "```"
const _fencedMixedHTML = `<p>hii <pre><code class="language-go">package main
# hi
</code></pre>
</p>`

func TestFenced(t *testing.T) {
	var tests = []struct {
		md, html, name string
	}{
		{_fencedInline, _fencedInlineHTML, "inline"},
		{_fencedLanguage, _fencedLanguageHTML, "language"},
		{_fencedBroken, _fencedBrokenHTML, "broken"},
		{_fencedMixed, _fencedMixedHTML, "mixed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := Parse([]byte(test.md))
			dump := dump(node, []byte(test.md))
			t.Log("node:\n", dump)
		})
	}
}
