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
)

// emojiCategory is a group of the picker. Each entry is the emoji followed by
// the words, in Spanish and English, that find it.
type emojiCategory struct {
	name    string
	entries []string
}

var emojiCategories = []emojiCategory{
	{"Caras", []string{
		"😀 sonrisa grinning feliz", "😃 sonrisa smiley feliz", "😄 sonrisa smile risa", "😁 dientes grin", "😆 risa laughing", "😅 sudor sweat risa",
		"🤣 carcajada rofl risa", "😂 llorar risa joy jaja", "🙂 sonrisa slight", "🙃 al reves upside", "😉 guiño wink", "😊 sonrojo blush feliz",
		"😇 angel innocent", "🥰 amor corazones love", "😍 enamorado heart eyes", "🤩 estrellas star struck", "😘 beso kiss", "😗 beso kissing",
		"😋 rico yum lengua", "😛 lengua tongue", "😜 lengua guiño wink", "🤪 loco zany", "😝 lengua tongue", "🤑 dinero money",
		"🤗 abrazo hug", "🤭 tapar boca oops", "🤫 silencio shush", "🤔 pensar thinking", "🤐 callado zipper", "🤨 ceja sospecha raised",
		"😐 neutral", "😑 sin expresion expressionless", "😶 sin boca no mouth", "😏 picaro smirk", "😒 aburrido unamused", "🙄 ojos roll eyes",
		"😬 mueca grimace", "😌 aliviado relieved", "😔 pensativo pensive triste", "😪 sueño sleepy", "🤤 baba drool", "😴 dormir sleeping",
		"😷 mascarilla mask enfermo", "🤒 fiebre thermometer enfermo", "🤕 herido bandage", "🤢 nauseas nauseated", "🤮 vomitar vomit", "🥵 calor hot",
		"🥶 frio cold", "😵 mareado dizzy", "🤯 explotar mind blown", "🤠 vaquero cowboy", "🥳 fiesta party", "😎 lentes cool sunglasses",
		"🤓 nerd lentes", "😕 confundido confused", "😟 preocupado worried", "🙁 triste frown", "😮 sorpresa open mouth", "😯 asombro hushed",
		"😲 asombrado astonished", "😳 sonrojado flushed", "🥺 suplicar pleading porfa", "😦 frown", "😧 angustia anguished", "😨 miedo fearful",
		"😰 ansiedad anxious sweat", "😥 triste sad relieved", "😢 llorar cry", "😭 llanto sob llorar", "😱 grito scream miedo", "😖 confounded",
		"😣 perseverar persevere", "😞 decepcion disappointed", "😓 sudor sweat", "😩 cansado weary", "😫 agotado tired", "🥱 bostezo yawn",
		"😤 enojo triumph resoplar", "😡 enojado rage furioso", "😠 enojado angry", "🤬 maldecir cursing", "😈 diablo smiling imp", "👿 diablo imp",
		"💀 calavera skull muerto", "☠️ calavera skull crossbones", "💩 popo poop", "🤡 payaso clown", "👹 ogro ogre", "👻 fantasma ghost",
		"👽 alien extraterrestre", "🤖 robot", "😺 gato cat", "😹 gato risa cat joy", "😻 gato amor cat heart", "🙈 mono no ver monkey",
	}},
	{"Gestos", []string{
		"👋 hola saludo wave", "🤚 mano raised back", "✋ mano alto hand", "🖖 vulcano vulcan", "👌 ok perfecto", "🤌 pellizco pinched",
		"✌️ paz victoria victory", "🤞 suerte dedos cruzados crossed", "🤟 te quiero love you", "🤘 rock cuernos", "🤙 llamame call me", "👈 izquierda left",
		"👉 derecha right", "👆 arriba up", "👇 abajo down", "☝️ uno arriba index", "👍 bien like pulgar arriba thumbs up", "👎 mal dislike pulgar abajo thumbs down",
		"✊ puño fist", "👊 puñetazo punch", "🤛 puño izquierda", "🤜 puño derecha", "👏 aplausos clap", "🙌 celebrar raised hands",
		"👐 manos abiertas open hands", "🤲 palmas palms", "🤝 trato handshake", "🙏 por favor gracias pray", "✍️ escribir writing", "💪 fuerza musculo muscle",
		"🦵 pierna leg", "🦶 pie foot", "👂 oreja ear", "👃 nariz nose", "🧠 cerebro brain", "👀 ojos mirar eyes",
		"👁️ ojo eye", "👅 lengua tongue", "👄 boca mouth", "🫡 saludo salute", "🫶 corazon manos heart hands", "🤦 facepalm",
		"🤷 no se shrug", "🙋 levantar mano raising hand", "🙇 reverencia bow", "💁 informacion tipping", "🙆 ok gesture", "🙅 no gesture",
	}},
	{"Corazones", []string{
		"❤️ corazon rojo red heart amor", "🧡 corazon naranja orange", "💛 corazon amarillo yellow", "💚 corazon verde green", "💙 corazon azul blue", "💜 corazon morado purple",
		"🖤 corazon negro black", "🤍 corazon blanco white", "🤎 corazon cafe brown", "💔 corazon roto broken", "❣️ exclamacion corazon", "💕 dos corazones two hearts",
		"💞 corazones girando revolving", "💓 latido beating", "💗 creciendo growing", "💖 brillante sparkling", "💘 flecha cupido arrow", "💝 regalo corazon gift",
		"💟 decoracion heart", "♥️ corazones hearts", "💋 beso kiss mark", "💌 carta amor love letter", "😻 gato enamorado", "🥰 enamorado",
	}},
	{"Animales", []string{
		"🐶 perro dog", "🐱 gato cat", "🐭 raton mouse", "🐹 hamster", "🐰 conejo rabbit", "🦊 zorro fox",
		"🐻 oso bear", "🐼 panda", "🐨 koala", "🐯 tigre tiger", "🦁 leon lion", "🐮 vaca cow",
		"🐷 cerdo pig", "🐸 rana frog", "🐵 mono monkey", "🐔 gallina chicken", "🐧 pinguino penguin", "🐦 pajaro bird",
		"🦆 pato duck", "🦅 aguila eagle", "🦉 buho owl", "🐺 lobo wolf", "🐗 jabali boar", "🐴 caballo horse",
		"🦄 unicornio unicorn", "🐝 abeja bee", "🐛 gusano bug", "🦋 mariposa butterfly", "🐌 caracol snail", "🐞 mariquita ladybug",
		"🐢 tortuga turtle", "🐍 serpiente snake", "🦖 dinosaurio t-rex", "🐙 pulpo octopus", "🦈 tiburon shark", "🐬 delfin dolphin",
		"🐳 ballena whale", "🐟 pez fish", "🦀 cangrejo crab", "🌵 cactus", "🌲 arbol tree", "🌴 palmera palm",
		"🍀 trebol suerte clover", "🌹 rosa rose", "🌻 girasol sunflower", "🌸 flor cerezo blossom", "🌙 luna moon", "⭐ estrella star",
		"🌟 estrella brillante glowing star", "✨ brillos sparkles", "⚡ rayo lightning", "🔥 fuego fire", "🌈 arcoiris rainbow", "☀️ sol sun",
		"⛅ nublado cloud", "🌧️ lluvia rain", "❄️ nieve snow", "💧 gota drop", "🌊 ola wave", "🌍 tierra mundo earth",
	}},
	{"Comida", []string{
		"🍏 manzana verde apple", "🍎 manzana apple", "🍌 platano banana", "🍉 sandia watermelon", "🍇 uvas grapes", "🍓 fresa strawberry",
		"🍒 cerezas cherries", "🍑 durazno peach", "🍍 piña pineapple", "🥭 mango", "🥑 aguacate avocado", "🌶️ chile pepper",
		"🌽 elote corn", "🥕 zanahoria carrot", "🥔 papa potato", "🍞 pan bread", "🧀 queso cheese", "🥚 huevo egg",
		"🥓 tocino bacon", "🍗 pollo chicken", "🍖 carne meat", "🌭 hot dog", "🍔 hamburguesa burger", "🍟 papas fritas fries",
		"🍕 pizza", "🌮 taco", "🌯 burrito", "🥗 ensalada salad", "🍝 pasta spaghetti", "🍜 ramen sopa",
		"🍣 sushi", "🍤 camaron shrimp", "🍦 helado ice cream", "🍩 dona donut", "🍪 galleta cookie", "🎂 pastel cumpleaños cake",
		"🍰 pastel cake", "🍫 chocolate", "🍬 dulce candy", "🍭 paleta lollipop", "🍿 palomitas popcorn", "☕ cafe coffee",
		"🍵 te tea", "🧃 jugo juice", "🥤 refresco soda", "🍺 cerveza beer", "🍻 brindis cheers", "🍷 vino wine",
	}},
	{"Actividades", []string{
		"⚽ futbol soccer", "🏀 basquet basketball", "🏈 americano football", "⚾ beisbol baseball", "🎾 tenis tennis", "🏐 voleibol volleyball",
		"🎱 billar pool", "🏓 ping pong", "🥊 boxeo boxing", "🥋 karate", "⛳ golf", "🎣 pesca fishing",
		"🏆 trofeo trophy ganar", "🥇 oro primer lugar gold", "🥈 plata silver", "🥉 bronce bronze", "🏅 medalla medal", "🎖️ condecoracion",
		"🎮 videojuego control game", "🕹️ joystick arcade", "🎲 dado dice", "♟️ ajedrez chess", "🎯 diana target", "🎳 boliche bowling",
		"🎨 arte paleta art", "🎬 cine clapper", "🎤 microfono karaoke mic", "🎧 audifonos headphones musica", "🎼 partitura score", "🎹 piano",
		"🥁 tambor drum", "🎸 guitarra guitar", "🎺 trompeta trumpet", "🎻 violin", "🎉 fiesta confeti party", "🎊 confeti confetti",
		"🎁 regalo gift", "🎈 globo balloon", "🎄 navidad christmas", "🎃 halloween calabaza", "🎆 fuegos artificiales fireworks", "🎟️ boleto ticket",
	}},
	{"Viajes", []string{
		"🚗 carro auto car", "🚕 taxi", "🚌 autobus bus", "🏎️ carreras race car", "🚓 policia police", "🚑 ambulancia",
		"🚒 bomberos fire engine", "🚲 bicicleta bike", "🛵 moto scooter", "🏍️ motocicleta motorcycle", "✈️ avion airplane", "🚀 cohete rocket",
		"🛸 ovni ufo", "🚁 helicoptero", "⛵ velero sailboat", "🚢 barco ship", "🏠 casa house", "🏢 edificio office",
		"🏫 escuela school", "🏥 hospital", "🏰 castillo castle", "🗽 estatua libertad", "🗼 torre tower", "⛰️ montaña mountain",
		"🏖️ playa beach", "🏝️ isla island", "🌋 volcan volcano", "🗺️ mapa map", "🧭 brujula compass", "⛺ acampar tent",
	}},
	{"Objetos", []string{
		"⌚ reloj watch", "📱 celular phone", "💻 laptop computadora", "⌨️ teclado keyboard", "🖥️ computadora desktop", "🖱️ mouse raton",
		"🕹️ palanca joystick", "💾 disquete floppy", "💿 cd disco", "📷 camara camera", "📸 foto flash", "🎥 video camera",
		"📺 tele tv", "📻 radio", "🔋 bateria battery", "🔌 enchufe plug", "💡 idea foco bulb", "🔦 linterna flashlight",
		"💸 dinero volando money", "💵 dolares dollar", "💰 bolsa dinero money bag", "💳 tarjeta card", "💎 diamante gem", "🔧 llave wrench",
		"🔨 martillo hammer", "⚙️ engranaje gear", "🔫 pistola agua gun", "💣 bomba bomb", "🔪 cuchillo knife", "🛡️ escudo shield",
		"🔒 candado cerrado lock", "🔓 abierto unlock", "🔑 llave key", "🚪 puerta door", "🛏️ cama bed", "🚽 baño toilet",
		"📦 paquete package", "📚 libros books", "📖 libro book", "✏️ lapiz pencil", "📝 nota memo", "📌 chincheta pin",
		"📎 clip", "✂️ tijeras scissors", "📅 calendario calendar", "📈 subir chart up", "📉 bajar chart down", "🗑️ basura trash",
		"🔔 campana bell", "🔕 silencio no bell", "📢 altavoz megafono loudspeaker", "📣 megafono megaphone", "💬 mensaje chat speech", "💭 pensamiento thought",
	}},
	{"Símbolos", []string{
		"✅ check si listo hecho", "☑️ casilla checked", "✔️ check mark", "❌ x no mal cross", "❎ x cuadro", "➕ mas plus",
		"➖ menos minus", "➗ dividir", "✖️ multiplicar", "❓ pregunta question", "❔ pregunta blanca", "❗ exclamacion",
		"❕ exclamacion blanca", "‼️ doble exclamacion", "⁉️ exclamacion pregunta", "💯 cien 100 perfecto", "🔞 mayores 18", "⚠️ advertencia warning",
		"🚫 prohibido prohibited", "⛔ no entrar", "♻️ reciclar recycle", "💤 dormir zzz", "💢 enojo anger", "💥 explosion boom",
		"💫 mareo dizzy", "💦 gotas sweat", "💨 rapido dash", "🕳️ hoyo hole", "🔴 rojo red circle", "🟠 naranja orange circle",
		"🟡 amarillo yellow circle", "🟢 verde green circle", "🔵 azul blue circle", "🟣 morado purple circle", "⚫ negro black circle", "⚪ blanco white circle",
		"🔺 triangulo rojo", "🔻 triangulo abajo", "🔷 diamante azul", "🔶 diamante naranja", "⬆️ arriba up", "⬇️ abajo down",
		"⬅️ izquierda left", "➡️ derecha right", "🔄 recargar repeat", "🔁 repetir", "▶️ play", "⏸️ pausa pause",
		"⏹️ stop", "⏩ adelantar", "⏪ atrasar", "🎵 nota musica music", "🎶 notas musica", "™️ marca tm",
		"©️ copyright", "®️ registrado", "🆗 ok", "🆕 nuevo new", "🆒 cool", "🆘 sos ayuda",
	}},
	{"Banderas", []string{
		"🏁 meta carrera checkered", "🚩 bandera roja red flag", "🏳️ blanca white flag", "🏴 negra black flag", "🏳️‍🌈 arcoiris pride",
		"🇲🇽 mexico", "🇦🇷 argentina", "🇨🇴 colombia", "🇨🇱 chile", "🇵🇪 peru", "🇻🇪 venezuela",
		"🇪🇨 ecuador", "🇧🇴 bolivia", "🇺🇾 uruguay", "🇵🇾 paraguay", "🇬🇹 guatemala", "🇭🇳 honduras",
		"🇸🇻 el salvador", "🇳🇮 nicaragua", "🇨🇷 costa rica", "🇵🇦 panama", "🇨🇺 cuba", "🇩🇴 republica dominicana",
		"🇵🇷 puerto rico", "🇪🇸 españa spain", "🇺🇸 estados unidos usa", "🇧🇷 brasil brazil", "🇯🇵 japon japan", "🇬🇧 reino unido uk",
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
			name := "Este servidor"
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
				c.insertText(cl.text)
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
				return textField(gtx, th, &p.search, "Buscar emoji (por ejemplo: risa, corazon, fuego)")
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if len(rows) == 0 {
					return layout.Center.Layout(gtx, label(th, 14, colorMuted, "No hay emojis que coincidan.").Layout)
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
