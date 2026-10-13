package gui

import (
	"context"
	"errors"
	"image"
	"io"
	"strings"

	"gioui.org/io/clipboard"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"github.com/ayn2op/discordo/internal/auth"
	"github.com/skip2/go-qrcode"
)

type loginTab int

const (
	tabQR loginTab = iota
	tabPassword
	tabToken
	tabCount
)

var tabNames = [tabCount]string{"Código QR", "Correo", "Token"}

type loginView struct {
	u    *ui
	tab  loginTab
	tabs [tabCount]widget.Clickable
	err  string
	busy bool

	// QR login.
	qrCancel  context.CancelFunc
	qrImage   *paint.ImageOp
	qrStatus  string
	qrRetry   widget.Clickable
	qrStopped bool

	// Email and password login.
	email, password, code widget.Editor
	mfa                   *auth.MFARequired
	submit                widget.Clickable

	// Token login.
	token     widget.Editor
	showGuide bool
	guide     widget.Clickable
	openWeb   widget.Clickable
	// copyScript copies tokenScript, and copied reports that it did.
	copyScript widget.Clickable
	copied     bool
	scriptText widget.Selectable

	// scroll lets the card scroll when it is taller than the window.
	scroll widget.List
}

func newLoginView(u *ui) *loginView {
	l := &loginView{u: u}
	for _, ed := range []*widget.Editor{&l.email, &l.password, &l.code, &l.token} {
		ed.SingleLine = true
		ed.Submit = true
	}
	l.password.Mask = '•'
	l.token.Mask = '•'
	l.startQR()
	return l
}

// stop ends the QR login, if it runs.
func (l *loginView) stop() {
	if l.qrCancel != nil {
		l.qrCancel()
		l.qrCancel = nil
	}
}

func (l *loginView) startQR() {
	l.stop()
	ctx, cancel := context.WithCancel(context.Background())
	l.qrCancel, l.qrImage, l.qrStopped = cancel, nil, false
	l.qrStatus = "Preparando el código…"
	u := l.u
	go func() {
		token, err := auth.QR(ctx, auth.QRHandlers{
			Code: func(url string) {
				qr, err := qrcode.New(url, qrcode.Medium)
				if err != nil {
					return
				}
				qr.DisableBorder = true
				img := paint.NewImageOp(qr.Image(256))
				img.Filter = paint.FilterNearest
				u.post(func() {
					l.qrImage = &img
					l.qrStatus = "Abre Discord en tu celular, ve a Ajustes → Escanear código QR y apunta a este código."
				})
			},
			Scanned: func(username string) {
				u.post(func() {
					l.qrStatus = "Hola, " + username + ". Confirma el inicio de sesión en tu celular."
				})
			},
		})
		u.post(func() {
			if ctx.Err() != nil || u.login != l {
				return
			}
			l.qrStopped = true
			if err != nil {
				l.qrImage = nil
				switch {
				case errors.Is(err, auth.ErrQRCanceled):
					l.qrStatus = err.Error() + "."
				case errors.Is(err, auth.ErrCaptcha):
					l.qrStatus = "Discord pidió un captcha (\"no soy un robot\") y reDiscord no puede mostrarlo. " +
						"Prueba a generar otro código; si vuelve a pasar, entra con tu token (pestaña Token, ahí te explico cómo)."
				default:
					l.qrStatus = "No se pudo usar el código QR: " + err.Error()
				}
				return
			}
			u.loggedIn(token)
		})
	}()
}

// run calls f in a goroutine, showing the login as busy meanwhile, and logs
// in with the token it returns.
func (l *loginView) run(f func() (string, error)) {
	if l.busy {
		return
	}
	l.busy, l.err = true, ""
	u := l.u
	go func() {
		token, err := f()
		u.post(func() {
			if u.login != l {
				return
			}
			l.busy = false
			var mfa *auth.MFARequired
			switch {
			case errors.As(err, &mfa):
				l.mfa = mfa
				l.code.SetText("")
			case errors.Is(err, auth.ErrCaptcha):
				l.err = "Discord pidió un captcha (\"no soy un robot\") y reDiscord no puede mostrarlo. Usa el código QR o tu token."
			case err != nil:
				l.err = err.Error()
			default:
				u.loggedIn(token)
			}
		})
	}()
}

func (l *loginView) submitPassword() {
	if l.mfa != nil {
		mfa, code := l.mfa, l.code.Text()
		l.run(func() (string, error) { return auth.TOTP(mfa, code) })
		return
	}
	email, password := strings.TrimSpace(l.email.Text()), l.password.Text()
	if email == "" || password == "" {
		l.err = "Escribe tu correo y tu contraseña."
		return
	}
	l.run(func() (string, error) { return auth.Password(email, password) })
}

func (l *loginView) submitToken() {
	token := auth.CleanToken(l.token.Text())
	if token == "" {
		l.err = "Pega tu token."
		return
	}
	l.err = ""
	l.u.loggedIn(token)
}

func (l *loginView) update(gtx layout.Context) {
	for i := range l.tabs {
		if l.tabs[i].Clicked(gtx) && loginTab(i) != l.tab {
			l.tab, l.err = loginTab(i), ""
		}
	}
	if l.qrRetry.Clicked(gtx) {
		l.startQR()
	}
	submitted := func(ed *widget.Editor) bool {
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				return false
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				return true
			}
		}
	}
	switch l.tab {
	case tabPassword:
		s := submitted(&l.email)
		s = submitted(&l.password) || s
		s = submitted(&l.code) || s
		if s || l.submit.Clicked(gtx) {
			l.submitPassword()
		}
	case tabToken:
		if submitted(&l.token) || l.submit.Clicked(gtx) {
			l.submitToken()
		}
		if l.guide.Clicked(gtx) {
			l.showGuide = !l.showGuide
		}
		if l.openWeb.Clicked(gtx) {
			openLink("https://discord.com/app")
		}
		if l.copyScript.Clicked(gtx) {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(tokenScript))})
			l.copied = true
		}
	}
}

func (l *loginView) layout(gtx layout.Context) layout.Dimensions {
	l.update(gtx)
	l.scroll.Axis = layout.Vertical
	viewHeight := gtx.Constraints.Max.Y
	return listLayout(gtx, l.u.th, &l.scroll, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		// Measure the card to center it while it fits the window.
		macro := op.Record(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints.Min = image.Point{}
		cgtx.Constraints.Max.X = min(gtx.Constraints.Max.X-gtx.Dp(32), gtx.Dp(480))
		dims := l.layoutCard(cgtx)
		call := macro.Stop()

		margin := gtx.Dp(16)
		top := max((viewHeight-dims.Size.Y)/2, margin)
		left := (gtx.Constraints.Max.X - dims.Size.X) / 2
		t := op.Offset(image.Pt(left, top)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, top+dims.Size.Y+margin)}
	})
}

func (l *loginView) layoutCard(gtx layout.Context) layout.Dimensions {
	th := l.u.th
	{
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return rounded(gtx, colorSidebar, 8, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(32)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return bold(label(th, 24, colorHeader, "¡Hola de nuevo!")).Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Rigid(label(th, 14, colorMuted, "Inicia sesión en reDiscord con tu cuenta de Discord").Layout),
					layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
					layout.Rigid(l.layoutTabs),
					layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						switch l.tab {
						case tabQR:
							return l.layoutQR(gtx)
						case tabPassword:
							return l.layoutPassword(gtx)
						default:
							return l.layoutToken(gtx)
						}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if l.err == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, label(th, 14, colorRed, l.err).Layout)
					}),
				)
			})
		})
	}
}

func (l *loginView) layoutTabs(gtx layout.Context) layout.Dimensions {
	children := make([]layout.FlexChild, 0, tabCount)
	for i := range l.tabs {
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return l.tabs[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				active := loginTab(i) == l.tab
				c := colorMuted
				if active || l.tabs[i].Hovered() {
					c = colorHeader
				}
				dims := layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					lb := bold(label(l.u.th, 15, c, tabNames[i]))
					lb.Alignment = text.Middle
					return lb.Layout(gtx)
				})
				if active {
					h := gtx.Dp(2)
					paint.FillShape(gtx.Ops, colorAccent, clip.Rect{Min: image.Pt(0, dims.Size.Y-h), Max: dims.Size}.Op())
				}
				return dims
			})
		}))
	}
	return layout.Flex{}.Layout(gtx, children...)
}

func (l *loginView) layoutQR(gtx layout.Context) layout.Dimensions {
	th := l.u.th
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := gtx.Dp(200)
			pad := gtx.Dp(12)
			box := image.Pt(size+2*pad, size+2*pad)
			if l.qrImage == nil {
				gtx.Constraints = layout.Exact(box)
				if l.qrStopped {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return button(gtx, th, &l.qrRetry, colorAccent, "Generar otro código")
					})
				}
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					s := gtx.Dp(36)
					gtx.Constraints = layout.Exact(image.Pt(s, s))
					ld := loader(th)
					return ld.Layout(gtx)
				})
			}
			paint.FillShape(gtx.Ops, rgb(0xffffff), clip.UniformRRect(image.Rectangle{Max: box}, gtx.Dp(6)).Op(gtx.Ops))
			gtx.Constraints = layout.Exact(box)
			return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				img := widget.Image{Src: *l.qrImage, Fit: widget.Contain}
				gtx.Constraints = layout.Exact(image.Pt(size, size))
				return img.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lb := label(th, 14, colorText, l.qrStatus)
			lb.Alignment = text.Middle
			return lb.Layout(gtx)
		}),
	)
}

func (l *loginView) layoutPassword(gtx layout.Context) layout.Dimensions {
	th := l.u.th
	if l.mfa != nil {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(label(th, 14, colorText, "Escribe el código de 6 dígitos de tu app de autenticación.").Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &l.code, "123456") }),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return l.submitButton(gtx, "Verificar") }),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(bold(label(th, 12, colorMuted, "CORREO ELECTRÓNICO O TELÉFONO")).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &l.email, "") }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
		layout.Rigid(bold(label(th, 12, colorMuted, "CONTRASEÑA")).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &l.password, "") }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return l.submitButton(gtx, "Iniciar sesión") }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(label(th, 12, colorMuted, "Si Discord pide un captcha, usa el código QR.").Layout),
	)
}

// tokenScript copies the token of the Discord tab it runs in to the
// clipboard, for pasting in the browser's console. It stays short and
// readable so that anyone can check that it sends the token nowhere.
//
// It asks the modules of the web client for the token, and falls back to
// the copy that Discord keeps in the local storage, which the page hides
// from itself but a new frame can read.
const tokenScript = `// reDiscord: copia tu token al portapapeles. No lo envía a ningún sitio.
(() => {
  let token;
  try {
    const mods = [];
    webpackChunkdiscord_app.push([[Symbol()], {}, (r) => { for (const id in r.c) mods.push(r.c[id].exports); }]);
    webpackChunkdiscord_app.pop();
    for (const e of mods) {
      let vals = [];
      try { vals = [e, e?.default, ...Object.values(e ?? {})]; } catch {}
      for (const v of vals) {
        try { if (typeof v?.getToken === "function" && typeof v.getToken() === "string") token = v.getToken(); } catch {}
        if (token) break;
      }
      if (token) break;
    }
  } catch {}
  if (!token) {
    try {
      const f = document.body.appendChild(document.createElement("iframe"));
      token = JSON.parse(f.contentWindow.localStorage.token);
      f.remove();
    } catch {}
  }
  if (!token) return console.log("reDiscord: no encontré el token. ¿Iniciaste sesión en esta pestaña?");
  copy(token);
  console.log("reDiscord: ¡listo! Token copiado. Vuelve a reDiscord y pégalo con Ctrl+V.");
})();`

// tokenSteps explain how to copy the token from Discord in a browser, where
// the captcha can be solved.
var tokenSteps = []string{
	"1. Pulsa «Abrir Discord web» e inicia sesión (ahí sí puedes resolver el captcha).",
	"2. Pulsa «Copiar código» aquí abajo.",
	"3. En el navegador pulsa F12 y entra en la pestaña «Consola».",
	"4. Pega el código con Ctrl+V y pulsa Enter. Si el navegador no te deja pegar, escribe «allow pasting», pulsa Enter y vuelve a pegar.",
	"5. Vuelve aquí, haz clic en la casilla TOKEN, pega con Ctrl+V y pulsa Entrar.",
}

func (l *loginView) layoutToken(gtx layout.Context) layout.Dimensions {
	th := l.u.th
	children := []layout.FlexChild{
		layout.Rigid(bold(label(th, 12, colorMuted, "TOKEN")).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textField(gtx, th, &l.token, "") }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return l.submitButton(gtx, "Entrar") }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			txt := "¿Cómo consigo mi token?"
			if l.showGuide {
				txt = "Ocultar instrucciones"
			}
			return l.guide.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return label(th, 14, colorLink, txt).Layout(gtx)
			})
		}),
	}
	if l.showGuide {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				var steps []layout.FlexChild
				for _, s := range tokenSteps {
					steps = append(steps, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, label(th, 13, colorText, s).Layout)
					}))
				}
				steps = append(steps,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{}.Layout(gtx,
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									return button(gtx, th, &l.openWeb, colorSelected, "Abrir Discord web")
								}),
								layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									txt := "Copiar código"
									if l.copied {
										txt = "¡Código copiado!"
									}
									return button(gtx, th, &l.copyScript, colorAccent, txt)
								}),
							)
						})
					}),
					// The code itself, so that it can be read before running it.
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return rounded(gtx, colorServers, 4, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									lb := label(th, 11, colorMuted, tokenScript)
									lb.Font.Typeface = "Go Mono"
									lb.State = &l.scriptText
									return lb.Layout(gtx)
								})
							})
						})
					}),
				)
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, steps...)
			})
		}))
	}
	children = append(children,
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(label(th, 12, colorRed, "Nunca le des tu token a nadie: quien lo tenga puede entrar a tu cuenta. reDiscord solo lo guarda en tu PC.").Layout),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (l *loginView) submitButton(gtx layout.Context, txt string) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	if l.busy {
		txt = "Un momento…"
	}
	return button(gtx, l.u.th, &l.submit, colorAccent, txt)
}
