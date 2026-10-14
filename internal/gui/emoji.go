package gui

import (
	"image"
	"strings"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/arikawa/v3/discord"
)

// emojiCategory is a group of the picker. Each entry is the emoji followed by
// English search keywords that find it.
type emojiCategory struct {
	name    string
	entries []string
}

var emojiCategories = []emojiCategory{
	{"Faces", []string{
		"😀 grinning smile happy", "😃 smiley happy", "😄 smile laugh", "😁 grin teeth", "😆 laughing laugh", "😅 sweat laugh",
		"🤣 rofl laugh", "😂 joy laugh tears haha", "🙂 slight smile", "🙃 upside down", "😉 wink", "😊 blush happy",
		"😇 innocent angel", "🥰 love hearts", "😍 heart eyes love", "🤩 star struck", "😘 kiss", "😗 kissing",
		"😋 yum tongue", "😛 tongue", "😜 wink tongue", "🤪 zany crazy", "😝 tongue", "🤑 money",
		"🤗 hug", "🤭 oops mouth", "🤫 shush quiet", "🤔 thinking", "🤐 zipper quiet", "🤨 raised eyebrow suspicious",
		"😐 neutral", "😑 expressionless", "😶 no mouth", "😏 smirk", "😒 unamused bored", "🙄 roll eyes",
		"😬 grimace", "😌 relieved", "😔 pensive sad", "😪 sleepy", "🤤 drool", "😴 sleeping sleep",
		"😷 mask sick", "🤒 thermometer fever sick", "🤕 bandage injured", "🤢 nauseated", "🤮 vomit", "🥵 hot",
		"🥶 cold", "😵 dizzy", "🤯 mind blown", "🤠 cowboy", "🥳 party", "😎 cool sunglasses",
		"🤓 nerd glasses", "😕 confused", "😟 worried", "🙁 frown sad", "😮 open mouth surprised", "😯 hushed",
		"😲 astonished", "😳 flushed", "🥺 pleading please", "😦 frown", "😧 anguished", "😨 fearful scared",
		"😰 anxious sweat", "😥 sad relieved", "😢 cry tears", "😭 sob crying", "😱 scream scared", "😖 confounded",
		"😣 persevere", "😞 disappointed", "😓 sweat", "😩 weary", "😫 tired", "🥱 yawn",
		"😤 triumph huff angry", "😡 rage furious", "😠 angry", "🤬 cursing", "😈 smiling imp devil", "👿 imp devil",
		"💀 skull dead", "☠️ skull crossbones", "💩 poop", "🤡 clown", "👹 ogre", "👻 ghost",
		"👽 alien", "🤖 robot", "😺 cat", "😹 cat joy laugh", "😻 cat heart love", "🙈 monkey see no evil",
	}},
	{"Gestures", []string{
		"👋 wave hello", "🤚 raised back hand", "✋ hand stop", "🖖 vulcan", "👌 ok perfect", "🤌 pinched",
		"✌️ victory peace", "🤞 crossed fingers luck", "🤟 love you", "🤘 rock horns", "🤙 call me", "👈 left",
		"👉 right", "👆 up", "👇 down", "☝️ index up", "👍 thumbs up like good", "👎 thumbs down dislike bad",
		"✊ fist", "👊 punch", "🤛 left fist", "🤜 right fist", "👏 clap applause", "🙌 raised hands celebrate",
		"👐 open hands", "🤲 palms", "🤝 handshake deal", "🙏 pray please thanks", "✍️ writing", "💪 muscle strong",
		"🦵 leg", "🦶 foot", "👂 ear", "👃 nose", "🧠 brain", "👀 eyes look",
		"👁️ eye", "👅 tongue", "👄 mouth", "🫡 salute", "🫶 heart hands", "🤦 facepalm",
		"🤷 shrug", "🙋 raising hand", "🙇 bow", "💁 tipping information", "🙆 ok gesture", "🙅 no gesture",
	}},
	{"Hearts", []string{
		"❤️ red heart love", "🧡 orange heart", "💛 yellow heart", "💚 green heart", "💙 blue heart", "💜 purple heart",
		"🖤 black heart", "🤍 white heart", "🤎 brown heart", "💔 broken heart", "❣️ heart exclamation", "💕 two hearts",
		"💞 revolving hearts", "💓 beating heart", "💗 growing heart", "💖 sparkling heart", "💘 arrow cupid", "💝 gift heart",
		"💟 heart decoration", "♥️ hearts", "💋 kiss mark", "💌 love letter", "😻 cat love", "🥰 love",
	}},
	{"Animals and nature", []string{
		"🐶 dog", "🐱 cat", "🐭 mouse", "🐹 hamster", "🐰 rabbit", "🦊 fox",
		"🐻 bear", "🐼 panda", "🐨 koala", "🐯 tiger", "🦁 lion", "🐮 cow",
		"🐷 pig", "🐸 frog", "🐵 monkey", "🐔 chicken", "🐧 penguin", "🐦 bird",
		"🦆 duck", "🦅 eagle", "🦉 owl", "🐺 wolf", "🐗 boar", "🐴 horse",
		"🦄 unicorn", "🐝 bee", "🐛 bug worm", "🦋 butterfly", "🐌 snail", "🐞 ladybug",
		"🐢 turtle", "🐍 snake", "🦖 dinosaur t-rex", "🐙 octopus", "🦈 shark", "🐬 dolphin",
		"🐳 whale", "🐟 fish", "🦀 crab", "🌵 cactus", "🌲 tree", "🌴 palm tree",
		"🍀 clover luck", "🌹 rose", "🌻 sunflower", "🌸 cherry blossom", "🌙 moon", "⭐ star",
		"🌟 glowing star", "✨ sparkles", "⚡ lightning", "🔥 fire", "🌈 rainbow", "☀️ sun",
		"⛅ cloud", "🌧️ rain", "❄️ snow", "💧 drop", "🌊 wave", "🌍 earth world",
	}},
	{"Food and drink", []string{
		"🍏 green apple", "🍎 apple", "🍌 banana", "🍉 watermelon", "🍇 grapes", "🍓 strawberry",
		"🍒 cherries", "🍑 peach", "🍍 pineapple", "🥭 mango", "🥑 avocado", "🌶️ pepper chili",
		"🌽 corn", "🥕 carrot", "🥔 potato", "🍞 bread", "🧀 cheese", "🥚 egg",
		"🥓 bacon", "🍗 chicken", "🍖 meat", "🌭 hot dog", "🍔 burger", "🍟 fries",
		"🍕 pizza", "🌮 taco", "🌯 burrito", "🥗 salad", "🍝 pasta spaghetti", "🍜 ramen soup",
		"🍣 sushi", "🍤 shrimp", "🍦 ice cream", "🍩 donut", "🍪 cookie", "🎂 birthday cake",
		"🍰 cake", "🍫 chocolate", "🍬 candy", "🍭 lollipop", "🍿 popcorn", "☕ coffee",
		"🍵 tea", "🧃 juice", "🥤 soda", "🍺 beer", "🍻 cheers", "🍷 wine",
	}},
	{"Activities", []string{
		"⚽ soccer", "🏀 basketball", "🏈 football", "⚾ baseball", "🎾 tennis", "🏐 volleyball",
		"🎱 pool", "🏓 ping pong", "🥊 boxing", "🥋 karate", "⛳ golf", "🎣 fishing",
		"🏆 trophy win", "🥇 gold first place", "🥈 silver", "🥉 bronze", "🏅 medal", "🎖️ military medal",
		"🎮 game controller", "🕹️ joystick arcade", "🎲 dice", "♟️ chess", "🎯 target", "🎳 bowling",
		"🎨 art palette", "🎬 clapper film", "🎤 mic karaoke", "🎧 headphones music", "🎼 score", "🎹 piano",
		"🥁 drum", "🎸 guitar", "🎺 trumpet", "🎻 violin", "🎉 party confetti", "🎊 confetti",
		"🎁 gift", "🎈 balloon", "🎄 christmas", "🎃 halloween pumpkin", "🎆 fireworks", "🎟️ ticket",
	}},
	{"Travel", []string{
		"🚗 car", "🚕 taxi", "🚌 bus", "🏎️ race car", "🚓 police", "🚑 ambulance",
		"🚒 fire engine", "🚲 bike bicycle", "🛵 scooter", "🏍️ motorcycle", "✈️ airplane", "🚀 rocket",
		"🛸 ufo", "🚁 helicopter", "⛵ sailboat", "🚢 ship", "🏠 house", "🏢 office building",
		"🏫 school", "🏥 hospital", "🏰 castle", "🗽 statue liberty", "🗼 tower", "⛰️ mountain",
		"🏖️ beach", "🏝️ island", "🌋 volcano", "🗺️ map", "🧭 compass", "⛺ tent camping",
	}},
	{"Objects", []string{
		"⌚ watch", "📱 phone", "💻 laptop computer", "⌨️ keyboard", "🖥️ desktop computer", "🖱️ mouse",
		"🕹️ joystick", "💾 floppy disk", "💿 cd disc", "📷 camera", "📸 camera flash", "🎥 video camera",
		"📺 tv", "📻 radio", "🔋 battery", "🔌 plug", "💡 bulb idea", "🔦 flashlight",
		"💸 flying money", "💵 dollar", "💰 money bag", "💳 card", "💎 gem diamond", "🔧 wrench",
		"🔨 hammer", "⚙️ gear", "🔫 water gun", "💣 bomb", "🔪 knife", "🛡️ shield",
		"🔒 lock", "🔓 unlock", "🔑 key", "🚪 door", "🛏️ bed", "🚽 toilet",
		"📦 package", "📚 books", "📖 book", "✏️ pencil", "📝 memo note", "📌 pin",
		"📎 clip", "✂️ scissors", "📅 calendar", "📈 chart up", "📉 chart down", "🗑️ trash",
		"🔔 bell", "🔕 no bell mute", "📢 loudspeaker", "📣 megaphone", "💬 chat speech message", "💭 thought",
	}},
	{"Symbols", []string{
		"✅ check yes done", "☑️ checked box", "✔️ check mark", "❌ cross no wrong", "❎ cross box", "➕ plus",
		"➖ minus", "➗ divide", "✖️ multiply", "❓ question", "❔ white question", "❗ exclamation",
		"❕ white exclamation", "‼️ double exclamation", "⁉️ exclamation question", "💯 hundred 100 perfect", "🔞 adults 18", "⚠️ warning",
		"🚫 prohibited", "⛔ no entry", "♻️ recycle", "💤 sleep zzz", "💢 anger", "💥 explosion boom",
		"💫 dizzy", "💦 sweat drops", "💨 dash fast", "🕳️ hole", "🔴 red circle", "🟠 orange circle",
		"🟡 yellow circle", "🟢 green circle", "🔵 blue circle", "🟣 purple circle", "⚫ black circle", "⚪ white circle",
		"🔺 red triangle", "🔻 down triangle", "🔷 blue diamond", "🔶 orange diamond", "⬆️ up", "⬇️ down",
		"⬅️ left", "➡️ right", "🔄 repeat reload", "🔁 repeat", "▶️ play", "⏸️ pause",
		"⏹️ stop", "⏩ fast forward", "⏪ rewind", "🎵 music note", "🎶 music notes", "™️ trademark tm",
		"©️ copyright", "®️ registered", "🆗 ok", "🆕 new", "🆒 cool", "🆘 sos help",
	}},
	{"Flags", []string{
		"🏁 checkered race finish", "🚩 red flag", "🏳️ white flag", "🏴 black flag", "🏳️‍🌈 rainbow pride",
		"🇲🇽 mexico", "🇦🇷 argentina", "🇨🇴 colombia", "🇨🇱 chile", "🇵🇪 peru", "🇻🇪 venezuela",
		"🇪🇨 ecuador", "🇧🇴 bolivia", "🇺🇾 uruguay", "🇵🇾 paraguay", "🇬🇹 guatemala", "🇭🇳 honduras",
		"🇸🇻 el salvador", "🇳🇮 nicaragua", "🇨🇷 costa rica", "🇵🇦 panama", "🇨🇺 cuba", "🇩🇴 dominican republic",
		"🇵🇷 puerto rico", "🇪🇸 spain", "🇺🇸 united states usa", "🇧🇷 brazil", "🇯🇵 japan", "🇬🇧 united kingdom uk",
	}},
}

type emojiCell struct {
	text  string // the emoji, or the markup of a custom emoji
	name  string
	url   string // of a custom emoji
	click widget.Clickable
}

// emojiPicker is the popup to put emoji in the message.
type emojiPicker struct {
	search  widget.Editor
	list    widget.List
	focused bool
	// cells are made once per emoji, so that each keeps its click state.
	cells   map[string]*emojiCell
	hovered string
	// react is the message the picked emoji reacts to; with none, the emoji
	// goes in the box to write.
	react discord.MessageID
}

func newEmojiPicker() *emojiPicker {
	p := &emojiPicker{cells: make(map[string]*emojiCell)}
	p.search.SingleLine = true
	p.list.Axis = layout.Vertical
	return p
}

func (p *emojiPicker) cell(key, txt, name, url string) *emojiCell {
	cl, ok := p.cells[key]
	if !ok {
		cl = &emojiCell{text: txt, name: name, url: url}
		p.cells[key] = cl
	}
	return cl
}

// emojiSection is a titled group of cells in the picker.
type emojiSection struct {
	title string
	cells []*emojiCell
}

// sections returns the groups of emoji that match the search: the custom
// emoji of the server, then the categories.
func (c *chatView) emojiSections(p *emojiPicker) []emojiSection {
	q := strings.ToLower(strings.TrimSpace(p.search.Text()))
	var out []emojiSection
	if ch := c.selChannel; ch != nil && ch.GuildID.IsValid() {
		emojis, _ := c.st.Cabinet.Emojis(ch.GuildID)
		var cells []*emojiCell
		for _, e := range emojis {
			if q != "" && !strings.Contains(strings.ToLower(e.Name), q) {
				continue
			}
			markup := "<:" + e.Name + ":" + e.ID.String() + ">"
			if e.Animated {
				markup = "<a:" + e.Name + ":" + e.ID.String() + ">"
			}
			url := "https://cdn.discordapp.com/emojis/" + e.ID.String() + ".png?size=48"
			cells = append(cells, p.cell(markup, markup+" ", ":"+e.Name+":", url))
		}
		if len(cells) > 0 {
			name := "This server"
			if g, err := c.st.Cabinet.Guild(ch.GuildID); err == nil {
				name = g.Name
			}
			out = append(out, emojiSection{name, cells})
		}
	}
	for _, cat := range emojiCategories {
		var cells []*emojiCell
		for _, entry := range cat.entries {
			emoji, words, _ := strings.Cut(entry, " ")
			if q != "" && !strings.Contains(words, q) {
				continue
			}
			name, _, _ := strings.Cut(words, " ")
			cells = append(cells, p.cell(emoji, emoji, ":"+name+":", ""))
		}
		if len(cells) > 0 {
			out = append(out, emojiSection{cat.name, cells})
		}
	}
	return out
}

func (c *chatView) layoutEmojiPicker(gtx layout.Context) layout.Dimensions {
	th := c.u.th
	p := c.comp.emoji
	if !p.focused {
		gtx.Execute(key.FocusCmd{Tag: &p.search})
		p.focused = true
	}
	for {
		if _, ok := p.search.Update(gtx); !ok {
			break
		}
	}
	// Escape closes the picker.
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &p.search, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			c.comp.emoji = nil
			gtxFocus(c)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}
	}

	sections := c.emojiSections(p)
	cellPx := gtx.Dp(40)
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, colorPanel, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Op(gtx.Ops))
	perRow := max((size.X-gtx.Dp(16))/cellPx, 1)

	// Each section is a title and rows of cells, flattened for the list.
	type row struct {
		title string
		cells []*emojiCell
	}
	var rows []row
	for _, s := range sections {
		rows = append(rows, row{title: s.title})
		for i := 0; i < len(s.cells); i += perRow {
			rows = append(rows, row{cells: s.cells[i:min(i+perRow, len(s.cells))]})
		}
	}
	for _, r := range rows {
		for _, cl := range r.cells {
			if cl.click.Clicked(gtx) {
				if id := p.react; id.IsValid() {
					e := emojiOfCell(cl)
					c.u.post(func() { c.toggleReaction(id, e) })
				} else {
					c.insertText(cl.text)
				}
				c.comp.emoji = nil
				return layout.Dimensions{Size: size}
			}
			if cl.click.Hovered() {
				p.hovered = cl.text
				if cl.url != "" {
					p.hovered = cl.name
				} else {
					p.hovered = cl.text + "  " + cl.name
				}
			}
		}
	}

	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return textField(gtx, th, &p.search, "Search emoji (for example: laugh, heart, fire)")
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if len(rows) == 0 {
					return layout.Center.Layout(gtx, label(th, 14, colorMuted, "No matching emoji.").Layout)
				}
				return listLayout(gtx, th, &p.list, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
					r := rows[i]
					if r.title != "" {
						return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(4), Left: unit.Dp(4)}.Layout(gtx,
							oneLine(bold(label(th, 12, colorMuted, strings.ToUpper(r.title)))).Layout)
					}
					var cells []layout.FlexChild
					for _, cl := range r.cells {
						cells = append(cells, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return cl.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								pointer.CursorPointer.Add(gtx.Ops)
								sz := image.Pt(cellPx, cellPx)
								if cl.click.Hovered() {
									paint.FillShape(gtx.Ops, colorSelected, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(4)).Op(gtx.Ops))
								}
								gtx.Constraints = layout.Exact(sz)
								return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									if cl.url != "" {
										return c.u.picture(gtx, cl.url, 28, colorPanel, cl.name, 0)
									}
									if u := twemojiURL(cl.text, 28); isEmoji(cl.text) && !c.u.images.failed(u, gtx.Dp(28)) {
										return c.u.picture(gtx, u, 28, colorT{}, "", 0)
									}
									l := label(th, 22, colorHeader, cl.text)
									l.Alignment = text.Middle
									l.MaxLines = 1
									return l.Layout(gtx)
								})
							})
						}))
					}
					return layout.Flex{}.Layout(gtx, cells...)
				})
			}),
			// The emoji under the pointer, with its name.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				h := gtx.Dp(28)
				gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
				return vcenter(gtx, oneLine(label(th, 13, colorText, p.hovered)).Layout)
			}),
		)
	})
}
