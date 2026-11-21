package ui

import (
	"fmt"
	"image"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/engine"
)

// MetroID é a sobreposição do metrônomo.
const MetroID = "__metronomo__"

// MetroWidthDp é a largura do painel do metrônomo.
const MetroWidthDp = 440

// beatFlash é quanto o LED do tempo fica aceso depois de cada clique.
const beatFlash = 110 * time.Millisecond

// MetroPanel é o metrônomo: liga/desliga, andamento (setas, knob e TAP), compasso e volume.
// O painel pode fechar com ele tocando; o botão da barra de topo mostra o andamento e pisca no tempo.
type MetroPanel struct {
	m                *audio.Metronome
	startBtn         widget.Clickable
	down, up         widget.Clickable
	tapBtn           widget.Clickable
	bpmKnob, volKnob *Knob
	beats            *Selector
	tt               engine.TapTempo
	tapAt            time.Time

	lastCount uint64
	lastBeat  int
	beatAt    time.Time
}

// NewMetroPanel cria o painel do metrônomo.
func NewMetroPanel(m *audio.Metronome) *MetroPanel {
	p := &MetroPanel{m: m, bpmKnob: NewKnob(m.BPM), volKnob: NewKnob(m.Volume), beats: NewSelector(m.Beats)}
	p.beats.Inline = true
	return p
}

// Toggle liga ou desliga o metrônomo (botão do painel e tecla M).
func (p *MetroPanel) Toggle() {
	if p.m.Running() {
		p.m.Stop()
	} else {
		p.m.Start()
	}
}

// TapAt registra um toque do TAP (botão ou tecla T com o painel aberto).
func (p *MetroPanel) TapAt(now time.Time) {
	p.tapAt = now
	if ms, ok := p.tt.Tap(now); ok {
		p.m.BPM.Set(60000 / ms)
	}
}

// pulse atualiza o LED do tempo e diz se ele está aceso (e em qual tempo do compasso).
func (p *MetroPanel) pulse(now time.Time) (lit bool, beat int) {
	if n, b := p.m.Count(); n != p.lastCount {
		p.lastCount, p.lastBeat, p.beatAt = n, b, now
	}
	return p.m.Running() && now.Sub(p.beatAt) < beatFlash, p.lastBeat
}

func (p *MetroPanel) update(gtx layout.Context) {
	if p.startBtn.Clicked(gtx) {
		p.Toggle()
	}
	if p.down.Clicked(gtx) {
		p.m.BPM.Set(p.m.BPM.Get() - 1)
	}
	if p.up.Clicked(gtx) {
		p.m.BPM.Set(p.m.BPM.Get() + 1)
	}
	for p.tapBtn.Clicked(gtx) {
		p.TapAt(gtx.Now)
	}
}

// Layout desenha o painel.
func (p *MetroPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	p.update(gtx)
	w := gtx.Dp(MetroWidthDp)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	lit, beat := p.pulse(gtx.Now)
	running := p.m.Running()
	center := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Center.Layout(gtx, w)
		})
	}
	return panel(gtx, th.Panel, 14, 18, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "METRÔNOMO", textOpts{size: 18, color: th.Text, bold: true})
			}),
			layout.Rigid(spacer(0, 10)),
			center(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arrowButton(gtx, th, &p.down, -1) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, fmt.Sprintf("%.0f", p.m.BPM.Get()),
							textOpts{size: 60, color: th.Gold, bold: true, width: gtx.Dp(150), align: text.Middle})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arrowButton(gtx, th, &p.up, +1) }),
				)
			}),
			center(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "BPM", textOpts{size: 12, color: th.Dim})
			}),
			layout.Rigid(spacer(0, 10)),
			center(func(gtx layout.Context) layout.Dimensions { return p.beatLEDs(gtx, th, lit, beat) }),
			layout.Rigid(spacer(0, 14)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle, Spacing: layout.SpaceAround}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.bpmKnob.Layout(gtx, th, 52, rgb(0x161616), th.Text, th.Gold)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.tap(gtx, th) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.volKnob.Layout(gtx, th, 52, rgb(0x161616), th.Text, th.Gold)
					}),
				)
			}),
			layout.Rigid(spacer(0, 12)),
			center(func(gtx layout.Context) layout.Dimensions { return p.beats.Layout(gtx, th, th.Text, th.Gold) }),
			layout.Rigid(spacer(0, 14)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label, bg := "► Ligar", th.PanelHi
				if running {
					label, bg = "■ Parar", darken(th.Good, 0.35)
				}
				return coloredButton(gtx, th, &p.startBtn, label, gtx.Constraints.Max.X, bg, th.Text)
			}),
			layout.Rigid(spacer(0, 8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "O clique sai só nos fones/caixas: não entra na gravação.", textOpts{size: 11, color: th.Dim})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "Tecla M liga e desliga; com este painel aberto, T é o TAP.", textOpts{size: 11, color: th.Dim})
			}),
		)
	})
}

// beatLEDs mostra um LED por tempo do compasso; o do tempo atual acende no clique.
func (p *MetroPanel) beatLEDs(gtx layout.Context, th *Theme, lit bool, beat int) layout.Dimensions {
	n := int(p.m.Beats.Get())
	r := float32(gtx.Dp(7))
	step := gtx.Dp(26)
	sz := image.Pt(step*n, gtx.Dp(18))
	for i := range n {
		on := lit && i == beat
		c := th.Good
		if i == 0 && n > 1 {
			c = th.Gold // acento
		}
		led(gtx.Ops, f32.Pt(float32(step*i+step/2), float32(sz.Y)/2), r, on, c, th.LedOff)
	}
	return layout.Dimensions{Size: sz}
}

// tap é o botão TAP (acende a cada toque).
func (p *MetroPanel) tap(gtx layout.Context, th *Theme) layout.Dimensions {
	return p.tapBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := image.Pt(gtx.Dp(72), gtx.Dp(38))
		bg := withAlpha(rgb(0x000000), 110)
		if gtx.Now.Sub(p.tapAt) < 120*time.Millisecond {
			bg = th.LedOn
		}
		fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(19), bg)
		defer offset(gtx.Ops, 0, (sz.Y-gtx.Sp(17))/2).Pop()
		drawText(gtx, th, "TAP", textOpts{size: 13, color: rgb(0xffffff), bold: true, width: sz.X, align: text.Middle})
		return layout.Dimensions{Size: sz}
	})
}

// BarButton é o botão da barra de topo: abre o painel; ligado, mostra o andamento e pisca no tempo.
func (p *MetroPanel) BarButton(gtx layout.Context, th *Theme, c *widget.Clickable) layout.Dimensions {
	lit, beat := p.pulse(gtx.Now)
	label, bg := "Metrônomo", th.PanelHi
	if p.m.Running() {
		label = fmt.Sprintf("Metrônomo %.0f", p.m.BPM.Get())
		bg = darken(th.Good, 0.6)
		if lit {
			bg = darken(th.Good, 0.3)
			if beat == 0 && p.m.Beats.Get() > 1 {
				bg = darken(th.Gold, 0.3)
			}
		}
	}
	return coloredButton(gtx, th, c, label, gtx.Dp(118), bg, th.Text)
}
