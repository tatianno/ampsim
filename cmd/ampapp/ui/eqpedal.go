package ui

import (
	"fmt"
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"

	"github.com/tatianno/ampsim/engine"
)

// Faixa do gráfico do EQ.
const (
	eqPlotFMin  = 20.0
	eqPlotFMax  = 20000.0
	eqPlotDB    = 18.0 // ±
	eqPlotSteps = 160
)

var eqBandTitles = [engine.EQBands]string{"GRAVES", "MÉDIO 1", "MÉDIO 2", "MÉDIO 3", "AGUDOS"}

// eqView é a parte própria do pedal de EQ: curva de resposta e knobs em colunas por banda.
type eqView struct {
	eq            *engine.EQ
	freq, gain    [engine.EQBands]*Knob
	q             [engine.EQBands]*Knob // nil nos shelves
	level         *Knob
	freqs, respDB []float64
}

func newEQView(m *engine.EQ) *eqView {
	v := &eqView{eq: m, freqs: make([]float64, eqPlotSteps), respDB: make([]float64, eqPlotSteps)}
	for i := range v.freq {
		n := i + 1
		v.freq[i] = NewKnob(m.Param(fmt.Sprintf("f%d", n)))
		v.gain[i] = NewKnob(m.Param(fmt.Sprintf("g%d", n)))
		if p := m.Param(fmt.Sprintf("q%d", n)); p != nil {
			v.q[i] = NewKnob(p)
		}
	}
	v.level = NewKnob(m.Param("level"))
	for k := range v.freqs {
		v.freqs[k] = eqPlotFMin * math.Pow(eqPlotFMax/eqPlotFMin, float64(k)/float64(eqPlotSteps-1))
	}
	return v
}

// EQPedalWidthDp é a largura do pedal de EQ aberto (mais largo que os outros: 5 colunas).
const EQPedalWidthDp = 540

// controls desenha a curva e os knobs.
func (v *eqView) controls(gtx layout.Context, th *Theme, st PedalStyle, on bool) layout.Dimensions {
	knobRow := func(knobs [engine.EQBands]*Knob) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var ch []layout.FlexChild
			for _, k := range knobs {
				ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if k == nil {
						return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
					}
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return k.Layout(gtx, th, 42, st.Knob, st.Text, darken(st.Body, 0.45))
					})
				}))
			}
			return layout.Flex{}.Layout(gtx, ch...)
		})
	}
	qRow := v.q
	qRow[engine.EQBands-1] = v.level // o Level fica na coluna dos agudos, na linha do Q
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.plot(gtx, th, st, on) }),
		layout.Rigid(spacer(0, 10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var ch []layout.FlexChild
			for _, t := range eqBandTitles {
				ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, t, textOpts{size: 11, color: withAlpha(st.Text, 200), bold: true,
						align: text.Middle, width: gtx.Constraints.Max.X})
				}))
			}
			return layout.Flex{}.Layout(gtx, ch...)
		}),
		layout.Rigid(spacer(0, 4)),
		knobRow(v.freq),
		layout.Rigid(spacer(0, 6)),
		knobRow(v.gain),
		layout.Rigid(spacer(0, 6)),
		knobRow(qRow),
	)
}

// plot desenha a curva de resposta (20 Hz a 20 kHz, ±18 dB) com a grade.
func (v *eqView) plot(gtx layout.Context, th *Theme, st PedalStyle, on bool) layout.Dimensions {
	sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(120))
	fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(6), rgb(0x15181c))
	w, h := float32(sz.X), float32(sz.Y)
	x := func(f float64) float32 {
		return w * float32(math.Log(f/eqPlotFMin)/math.Log(eqPlotFMax/eqPlotFMin))
	}
	y := func(db float64) float32 {
		db = math.Max(-eqPlotDB, math.Min(eqPlotDB, db))
		return h/2 - float32(db/eqPlotDB)*(h/2-4)
	}
	grid := withAlpha(rgb(0xffffff), 28)
	for _, db := range []float64{-12, -6, 6, 12} {
		strokeLine(gtx.Ops, f32.Pt(0, y(db)), f32.Pt(w, y(db)), 1, grid)
	}
	strokeLine(gtx.Ops, f32.Pt(0, y(0)), f32.Pt(w, y(0)), 1, withAlpha(rgb(0xffffff), 70))
	for _, f := range []float64{50, 100, 200, 500, 1000, 2000, 5000, 10000} {
		strokeLine(gtx.Ops, f32.Pt(x(f), 0), f32.Pt(x(f), h), 1, grid)
	}
	for _, l := range []struct {
		f     float64
		label string
	}{{100, "100"}, {1000, "1k"}, {10000, "10k"}} {
		func() {
			defer offset(gtx.Ops, int(x(l.f))+gtx.Dp(3), sz.Y-gtx.Dp(16)).Pop()
			drawText(gtx, th, l.label, textOpts{size: 10, color: withAlpha(rgb(0xffffff), 110)})
		}()
	}
	func() {
		defer offset(gtx.Ops, gtx.Dp(4), int(y(12))-gtx.Dp(7)).Pop()
		drawText(gtx, th, "+12", textOpts{size: 10, color: withAlpha(rgb(0xffffff), 110)})
	}()
	func() {
		defer offset(gtx.Ops, gtx.Dp(4), int(y(-12))-gtx.Dp(7)).Pop()
		drawText(gtx, th, "−12", textOpts{size: 10, color: withAlpha(rgb(0xffffff), 110)})
	}()

	v.eq.ResponseDB(v.freqs, v.respDB)
	var p clip.Path
	p.Begin(gtx.Ops)
	for k, f := range v.freqs {
		pt := f32.Pt(x(f), y(v.respDB[k]))
		if k == 0 {
			p.MoveTo(pt)
		} else {
			p.LineTo(pt)
		}
	}
	curve := th.Gold
	if !on {
		curve = withAlpha(curve, 110)
	}
	paint.FillShape(gtx.Ops, curve, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
	// pontos das bandas (na frequência de cada uma)
	for i := range v.freq {
		f := v.freq[i].P.Get()
		k := int(math.Round(math.Log(f/eqPlotFMin) / math.Log(eqPlotFMax/eqPlotFMin) * float64(eqPlotSteps-1)))
		k = max(0, min(eqPlotSteps-1, k))
		fillCircle(gtx.Ops, f32.Pt(x(f), y(v.respDB[k])), float32(gtx.Dp(4)), curve)
	}
	return layout.Dimensions{Size: sz}
}
