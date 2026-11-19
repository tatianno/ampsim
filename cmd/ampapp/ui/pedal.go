package ui

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/engine"
)

// Pedal desenha um pedal a partir da tabela de parâmetros do módulo:
// knobs (3 por linha), chaves para parâmetros discretos, nome, LED e footswitch.
type Pedal struct {
	chain *engine.Chain
	mod   engine.Module
	style PedalStyle
	knobs []*Knob
	sels  []*Selector
	foot  widget.Clickable
	tap   *TapControl // delay e tremolo
	eq    *eqView     // só no EQ (curva e knobs por banda)
	synth *synthView  // só no Synth (sequência, knobs e chaves)
	rm    widget.Clickable
	// OnRemoved é chamado depois de tirar o pedal da cadeia (o app fecha a sobreposição).
	OnRemoved func()
}

// NewPedal cria a vista do pedal para o módulo.
func NewPedal(chain *engine.Chain, mod engine.Module) *Pedal {
	p := &Pedal{chain: chain, mod: mod, style: StyleFor(mod.ID())}
	switch mod.ID() {
	case "delay":
		p.tap = NewTapControl(mod.Param("time"))
	case "tremolo":
		p.tap = NewRateTapControl(mod.Param("rate"), engine.TremoloSubdivisions, 1) // 1/8: 5 Hz = 150 BPM
	case "synth":
		p.tap = NewRateTapControl(mod.Param("rate"), engine.TremoloSubdivisions, 3) // 1/16: 8 Hz = 120 BPM
	}
	if m, ok := mod.(*engine.Synth); ok {
		p.synth = newSynthView(m)
		return p
	}
	if m, ok := mod.(*engine.EQ); ok {
		p.eq = newEQView(m, chain)
		return p
	}
	for _, par := range mod.Params() {
		in := par.Info()
		if in.Step > 0 && (in.Max-in.Min)/in.Step <= 4 {
			p.sels = append(p.sels, NewSelector(par))
		} else {
			p.knobs = append(p.knobs, NewKnob(par))
		}
	}
	return p
}

// visibleKnobs devolve os knobs a mostrar: todos, ou só os do modo atual (com o nome do modo)
// nos módulos que mudam de controles com o modo (engine.ParamViewer).
func (p *Pedal) visibleKnobs() []*Knob {
	pv, ok := p.mod.(engine.ParamViewer)
	if !ok {
		return p.knobs
	}
	var out []*Knob
	for _, s := range pv.ParamView() {
		for _, k := range p.knobs {
			if k.P.Info().ID == s.ID {
				k.Label = s.Name
				out = append(out, k)
			}
		}
	}
	return out
}

// PedalWidthDp é a largura do pedal aberto.
const PedalWidthDp = 330

// Layout desenha o pedal.
func (p *Pedal) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	id := p.mod.ID()
	if p.foot.Clicked(gtx) {
		p.chain.SetEnabled(id, !p.chain.Enabled(id))
	}
	if p.rm.Clicked(gtx) && p.chain.Remove(id) == nil && p.OnRemoved != nil {
		p.OnRemoved()
	}
	on := p.chain.Enabled(id)
	st := p.style
	w := gtx.Dp(PedalWidthDp)
	if p.eq != nil || p.synth != nil {
		w = gtx.Dp(EQPedalWidthDp)
	}
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}

	return panel(gtx, st.Body, 18, 18, func(gtx layout.Context) layout.Dimensions {
		// "Remover" no canto superior esquerdo (o "×" de fechar fica no direito)
		rows := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return coloredButton(gtx, th, &p.rm, "Remover", 0, withAlpha(rgb(0x000000), 60), st.Text)
		}), layout.Rigid(spacer(0, 8))}
		if p.eq != nil {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.eq.controls(gtx, th, st, on)
			}), layout.Rigid(spacer(0, 10)))
		}
		if p.synth != nil {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.synth.controls(gtx, th, st)
			}), layout.Rigid(spacer(0, 10)))
		}
		knobs := p.visibleKnobs()
		perRow := 3
		if len(knobs) == 4 {
			perRow = 2 // 2 + 2 fica melhor que 3 + 1
		}
		for i := 0; i < len(knobs); i += perRow {
			row := knobs[i:min(i+perRow, len(knobs))]
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var ch []layout.FlexChild
				for _, k := range row {
					ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return k.Layout(gtx, th, 54, st.Knob, st.Text, lighten(st.Body, 0.55))
						})
					}))
				}
				return layout.Flex{}.Layout(gtx, ch...)
			}), layout.Rigid(spacer(0, 10)))
		}
		if gr, ok := p.mod.(engine.GainReducer); ok {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return grMeter(gtx, th, gr.GainReductionDB(), st)
			}), layout.Rigid(spacer(0, 10)))
		}
		if p.tap != nil {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.tap.Layout(gtx, th, st)
			}), layout.Rigid(spacer(0, 10)))
		}
		for _, s := range p.sels {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return s.Layout(gtx, th, st.Text, lighten(st.Body, 0.55))
				})
			}), layout.Rigid(spacer(0, 10)))
		}
		rows = append(rows,
			layout.Rigid(spacer(0, 6)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, strings.ToUpper(p.mod.Name()), textOpts{
					size: 26, color: st.Text, align: text.Middle, bold: true, width: gtx.Constraints.Max.X,
				})
			}),
			layout.Rigid(spacer(0, 10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { // LED
				sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(20))
				led(gtx.Ops, f32.Pt(float32(sz.X)/2, float32(sz.Y)/2), float32(gtx.Dp(6)), on, th.LedOn, th.LedOff)
				return layout.Dimensions{Size: sz}
			}),
			layout.Rigid(spacer(0, 8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return footswitch(gtx, &p.foot, 58)
				})
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

// footswitch desenha o botão de pisar (metálico) e devolve suas dimensões.
func footswitch(gtx layout.Context, c *widget.Clickable, size unit.Dp) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		d := gtx.Dp(size)
		ctr := f32.Pt(float32(d)/2, float32(d)/2)
		r := float32(d) / 2
		fillCircle(gtx.Ops, f32.Pt(ctr.X, ctr.Y+r*0.08), r, rgb(0x111111))
		fillCircle(gtx.Ops, ctr, r, rgb(0x8d9399))
		fillCircle(gtx.Ops, ctr, r*0.78, rgb(0xb9bfc5))
		c2 := rgb(0xd7dce0)
		if c.Pressed() {
			c2 = rgb(0x9aa0a6)
		}
		fillCircle(gtx.Ops, ctr, r*0.6, c2)
		return layout.Dimensions{Size: image.Pt(d, d)}
	})
}

// grMaxDB é o fundo de escala do medidor de redução de ganho.
const grMaxDB = 20.0

// grMeter mostra a redução de ganho (dB) de um compressor: a barra cresce da direita para
// a esquerda, como nos medidores de GR de verdade.
func grMeter(gtx layout.Context, th *Theme, grDB float64, st PedalStyle) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "GR", textOpts{size: 12, color: st.Text, bold: true, width: gtx.Dp(26)})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			w, h := gtx.Constraints.Max.X, gtx.Dp(10)
			fillRRect(gtx.Ops, image.Rect(0, 0, w, h), gtx.Dp(3), withAlpha(rgb(0x000000), 90))
			f := clamp01(grDB / grMaxDB)
			if x := int(f * float64(w)); x > 0 {
				fillRRect(gtx.Ops, image.Rect(w-x, 0, w, h), gtx.Dp(3), rgb(0xffb020))
			}
			return layout.Dimensions{Size: image.Pt(w, h)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			s := "0.0 dB"
			if grDB >= 0.05 {
				s = fmt.Sprintf("−%.1f dB", grDB)
			}
			return drawText(gtx, th, s, textOpts{size: 11, color: st.Text, width: gtx.Dp(62), align: text.End})
		}),
	)
}
