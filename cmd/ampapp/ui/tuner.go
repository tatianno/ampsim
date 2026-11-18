package ui

import (
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/engine"
)

// TunerID é a sobreposição do afinador.
const TunerID = "__afinador__"

// standardTuning são as cordas da afinação padrão, da 6ª para a 1ª.
var standardTuning = []string{"E2", "A2", "D3", "G3", "B3", "E4"}

// Faixas do afinador (cents).
const (
	inTuneCents = 3.0  // verde
	closeCents  = 15.0 // dourado
	holdNote    = 600 * time.Millisecond
)

// TunerPanel é o afinador (sobreposição).
type TunerPanel struct {
	s            *audio.Session
	muteBtn      widget.Clickable
	a4Down, a4Up widget.Clickable
	mute         bool
	cents        float64 // ponteiro suavizado
	shown        audio.Reading
	lastOK       time.Time
	OnClose      func()
}

// NewTunerPanel cria o afinador (saída muda por padrão, como num pedal afinador).
func NewTunerPanel(s *audio.Session) *TunerPanel { return &TunerPanel{s: s, mute: true} }

// Open liga a detecção (e o mudo, se escolhido).
func (t *TunerPanel) Open() {
	t.s.Tuner.Start()
	t.s.Renderer.SetMute(t.mute)
	t.shown, t.lastOK = audio.Reading{}, time.Time{}
}

// Close desliga a detecção e devolve a saída.
func (t *TunerPanel) Close() {
	t.s.Tuner.Stop()
	t.s.Renderer.SetMute(false)
}

func (t *TunerPanel) update(gtx layout.Context) {
	if t.muteBtn.Clicked(gtx) {
		t.mute = !t.mute
		t.s.Renderer.SetMute(t.mute)
	}
	if t.a4Down.Clicked(gtx) {
		t.s.Tuner.SetA4(t.s.Tuner.A4() - 1)
	}
	if t.a4Up.Clicked(gtx) {
		t.s.Tuner.SetA4(t.s.Tuner.A4() + 1)
	}
	rd := t.s.Tuner.Reading()
	if rd.OK {
		if rd.Note != t.shown.Note || rd.Octave != t.shown.Octave || t.lastOK.IsZero() {
			t.cents = rd.Cents // nota nova: o ponteiro pula direto
		} else {
			t.cents += 0.35 * (rd.Cents - t.cents) // mesma nota: suaviza
		}
		t.shown, t.lastOK = rd, gtx.Now
	} else if gtx.Now.Sub(t.lastOK) > holdNote {
		t.shown = audio.Reading{} // segura a última nota um pouco depois de a corda parar
	}
}

// TunerWidthDp é a largura do afinador.
const TunerWidthDp = 480

// Layout desenha o afinador.
func (t *TunerPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	t.update(gtx)
	w := gtx.Dp(TunerWidthDp)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	rd := t.shown
	noteColor := th.Dim
	if rd.OK {
		switch c := math.Abs(t.cents); {
		case c < inTuneCents:
			noteColor = th.Good
		case c < closeCents:
			noteColor = th.Gold
		default:
			noteColor = th.Text
		}
	}
	center := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Center.Layout(gtx, w)
		})
	}
	return panel(gtx, th.Panel, 14, 18, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "AFINADOR", textOpts{size: 18, color: th.Text, bold: true})
			}),
			layout.Rigid(spacer(0, 6)),
			center(func(gtx layout.Context) layout.Dimensions {
				note := "—"
				if rd.OK {
					note = rd.Note
				}
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, note, textOpts{size: 72, color: noteColor, bold: true})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !rd.OK {
							return layout.Dimensions{}
						}
						return drawText(gtx, th, fmt.Sprint(rd.Octave), textOpts{size: 24, color: noteColor})
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.meter(gtx, th, rd.OK) }),
			layout.Rigid(spacer(0, 6)),
			center(func(gtx layout.Context) layout.Dimensions {
				s := "toque uma corda solta"
				if rd.OK {
					s = fmt.Sprintf("%+.0f cents  ·  %.1f Hz", t.cents, rd.Freq)
				}
				return drawText(gtx, th, s, textOpts{size: 13, color: th.Dim})
			}),
			layout.Rigid(spacer(0, 12)),
			center(func(gtx layout.Context) layout.Dimensions { return t.strings(gtx, th, rd) }),
			layout.Rigid(spacer(0, 14)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arrowButton(gtx, th, &t.a4Down, -1) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, fmt.Sprintf("A4 = %.0f Hz", t.s.Tuner.A4()),
							textOpts{size: 13, color: th.Text, width: gtx.Dp(110), align: text.Middle})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arrowButton(gtx, th, &t.a4Up, +1) }),
					layout.Flexed(1, hfill),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return toggle(gtx, th, &t.muteBtn, t.mute, "MUDO")
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "MUDO: a saída fica em silêncio enquanto o afinador está aberto", textOpts{size: 11, color: th.Dim})
				})
			}),
		)
	})
}

// meter é a escala de −50 a +50 cents com o ponteiro.
func (t *TunerPanel) meter(gtx layout.Context, th *Theme, ok bool) layout.Dimensions {
	w, h := gtx.Constraints.Max.X, gtx.Dp(46)
	x := func(c float64) float32 { return float32(w) * float32((c+50)/100) }
	// faixa "afinado"
	fillRRect(gtx.Ops, image.Rect(int(x(-inTuneCents)), gtx.Dp(8), int(x(inTuneCents)), h-gtx.Dp(8)), gtx.Dp(3), withAlpha(th.Good, 70))
	for c := -50; c <= 50; c += 10 {
		hh := float32(gtx.Dp(8))
		if c == 0 {
			hh = float32(gtx.Dp(16))
		}
		strokeLine(gtx.Ops, f32.Pt(x(float64(c)), float32(h)/2-hh), f32.Pt(x(float64(c)), float32(h)/2+hh), 1.5, th.Line)
	}
	if ok {
		c := math.Max(-50, math.Min(50, t.cents))
		col := th.Text
		if math.Abs(c) < inTuneCents {
			col = th.Good
		}
		strokeLine(gtx.Ops, f32.Pt(x(c), 0), f32.Pt(x(c), float32(h)), float32(gtx.Dp(4)), col)
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// strings mostra as cordas da afinação padrão, destacando a que está tocando.
func (t *TunerPanel) strings(gtx layout.Context, th *Theme, rd audio.Reading) layout.Dimensions {
	cur := ""
	if rd.OK {
		cur = fmt.Sprintf("%s%d", rd.Note, rd.Octave)
	}
	var ch []layout.FlexChild
	for _, s := range standardTuning {
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(gtx.Dp(54), gtx.Dp(30))
			bg, fg := th.PanelHi, th.Dim
			if s == cur {
				bg, fg = darken(th.Good, 0.35), th.Text
			}
			fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(5), bg)
			defer offset(gtx.Ops, 0, (sz.Y-gtx.Sp(18))/2).Pop()
			drawText(gtx, th, strings.Replace(s, "#", "♯", 1), textOpts{size: 14, color: fg, bold: s == cur, width: sz.X, align: text.Middle})
			return layout.Dimensions{Size: sz}
		}), layout.Rigid(spacer(6, 0)))
	}
	return layout.Flex{}.Layout(gtx, ch...)
}

// ---------------------------------------------------------------- tap tempo

// TapControl é o tap tempo de um pedal: botão TAP, subdivisão e andamento em BPM. Serve ao
// delay (parâmetro de tempo em ms) e ao tremolo (parâmetro de taxa em Hz).
type TapControl struct {
	param *engine.Param
	list  []engine.Subdivision
	// toValue converte andamento e subdivisão no valor do parâmetro; toBPM faz o caminho inverso.
	toValue func(bpm float64, s engine.Subdivision) float64
	toBPM   func(v float64, s engine.Subdivision) float64
	tt      engine.TapTempo
	tap     widget.Clickable
	subs    []widget.Clickable
	sub     int
	flash   time.Time
}

// NewTapControl cria o tap tempo para o parâmetro de tempo do delay (ms).
func NewTapControl(p *engine.Param) *TapControl {
	return newTap(p, engine.Subdivisions, engine.DelayMs, engine.BPM)
}

// NewRateTapControl cria o tap tempo para um parâmetro de taxa (Hz), como o do tremolo,
// começando na subdivisão sub.
func NewRateTapControl(p *engine.Param, list []engine.Subdivision, sub int) *TapControl {
	c := newTap(p, list, engine.RateHz, engine.BPMFromRate)
	c.sub = sub
	return c
}

func newTap(p *engine.Param, list []engine.Subdivision, to func(float64, engine.Subdivision) float64,
	back func(float64, engine.Subdivision) float64) *TapControl {
	return &TapControl{param: p, list: list, toValue: to, toBPM: back, subs: make([]widget.Clickable, len(list))}
}

// Subdivision devolve a subdivisão escolhida.
func (c *TapControl) Subdivision() engine.Subdivision { return c.list[c.sub] }

// BPM devolve o andamento correspondente ao valor atual do parâmetro.
func (c *TapControl) BPM() float64 { return c.toBPM(c.param.Get(), c.Subdivision()) }

// TapAt registra um toque no instante now (botão TAP ou tecla T).
func (c *TapControl) TapAt(now time.Time) {
	c.flash = now
	if ms, ok := c.tt.Tap(now); ok {
		c.param.Set(c.toValue(60000/ms, c.Subdivision()))
	}
}

// Layout desenha a linha do tap tempo.
func (c *TapControl) Layout(gtx layout.Context, th *Theme, st PedalStyle) layout.Dimensions {
	for c.tap.Clicked(gtx) {
		c.TapAt(gtx.Now)
	}
	for i := range c.subs {
		if c.subs[i].Clicked(gtx) && i != c.sub {
			bpm := c.BPM() // troca a subdivisão mantendo o andamento
			c.sub = i
			c.param.Set(c.toValue(bpm, c.Subdivision()))
		}
	}
	lit := gtx.Now.Sub(c.flash) < 120*time.Millisecond
	tapBtn := func(gtx layout.Context) layout.Dimensions {
		return c.tap.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(gtx.Dp(64), gtx.Dp(34))
			bg := withAlpha(rgb(0x000000), 110)
			if lit {
				bg = th.LedOn
			}
			fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(17), bg)
			defer offset(gtx.Ops, 0, (sz.Y-gtx.Sp(17))/2).Pop()
			drawText(gtx, th, "TAP", textOpts{size: 13, color: rgb(0xffffff), bold: true, width: sz.X, align: text.Middle})
			return layout.Dimensions{Size: sz}
		})
	}
	var subs []layout.FlexChild
	for i, s := range c.list {
		subs = append(subs, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			bg := withAlpha(rgb(0x000000), 70)
			fg := st.Text
			if i == c.sub {
				bg, fg = lighten(st.Body, 0.55), rgb(0x101010)
			}
			return layout.Inset{Right: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return coloredButton(gtx, th, &c.subs[i], s.Name, gtx.Constraints.Max.X, bg, fg)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(tapBtn),
				layout.Rigid(spacer(10, 0)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, fmt.Sprintf("%.0f BPM  ·  tecla T", c.BPM()), textOpts{size: 12, color: st.Text, bold: true})
				}),
			)
		}),
		layout.Rigid(spacer(0, 6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, subs...) }),
	)
}
