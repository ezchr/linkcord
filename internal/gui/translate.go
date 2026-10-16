package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// Messages are translated to the language of the interface, as Vencord's
// Translate does, by Google's translator.

var iconTranslate = mustIcon(icons.ActionTranslate)

// translation is a message in the user's language, or why it is not.
type translation struct {
	text, from string
	loading    bool
	err        string
}

// translateLang is the language messages are translated to.
func translateLang() string {
	if uiLang != "" {
		return uiLang
	}
	return systemLanguage()
}

// translate shows m in the user's language under it, or hides it again.
func (c *chatView) translate(m discord.Message) {
	if c.translations == nil {
		c.translations = make(map[discord.MessageID]*translation)
	}
	if t, ok := c.translations[m.ID]; ok && !t.loading {
		delete(c.translations, m.ID)
		return
	}
	t := &translation{loading: true}
	c.translations[m.ID] = t
	text, to := c.renderContent(m), translateLang()
	go func() {
		out, from, err := googleTranslate(text, to)
		c.u.post(func() {
			t.loading = false
			switch {
			case err != nil:
				t.err = tr("Could not translate: ") + err.Error()
			case from == to:
				t.err = tr("It is already in your language.")
			default:
				t.text, t.from = out, from
			}
		})
	}()
}

// googleTranslate is text in the language to, and the language it was in.
func googleTranslate(text, to string) (string, string, error) {
	q := url.Values{"client": {"gtx"}, "sl": {"auto"}, "tl": {to}, "dt": {"t"}, "q": {text}}
	client := http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get("https://translate.googleapis.com/translate_a/single?" + q.Encode())
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("%s", resp.Status)
	}
	// [[["translated", "original", ...], ...], null, "en", ...]
	var res []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", "", err
	}
	if len(res) < 3 {
		return "", "", fmt.Errorf("unexpected answer")
	}
	var parts [][]any
	json.Unmarshal(res[0], &parts)
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			if s, ok := p[0].(string); ok {
				b.WriteString(s)
			}
		}
	}
	var from string
	json.Unmarshal(res[2], &from)
	return b.String(), from, nil
}

// translateClickIndex is the clickable that hides a translation.
const translateClickIndex = 21

// layoutTranslation is m in the user's language, under what it says.
func (c *chatView) layoutTranslation(gtx layout.Context, m discord.Message) layout.Dimensions {
	t := c.translations[m.ID]
	if t == nil {
		return layout.Dimensions{}
	}
	th := c.u.th
	hide := c.msgClick(m.ID, translateClickIndex)
	if hide.Clicked(gtx) {
		delete(c.translations, m.ID)
		return layout.Dimensions{}
	}
	head := tr("Translating…")
	body := ""
	switch {
	case t.err != "":
		head = t.err
	case !t.loading:
		head = fmt.Sprintf(tr("Translated from %s · hide"), strings.ToUpper(t.from))
		body = t.text
	}
	return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if body == "" {
					return layout.Dimensions{}
				}
				return label(th, 15, colorText, body).Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return hide.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icon(gtx, iconTranslate, 12, colorMuted) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
						layout.Rigid(label(th, 11, colorMuted, head).Layout),
					)
				})
			}),
		)
	})
}
