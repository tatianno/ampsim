package ui

import (
	"fmt"
	"image"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
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
//
// Na curva, cada banda tem um ponto em (frequência, ganho da banda): arrastar o ponto muda
// os dois (e os knobs acompanham, porque leem os mesmos parâmetros); a roda do mouse sobre o
// ponto muda o Q (só nos médios); duplo clique zera o ganho.
type eqView struct {
	eq            *engine.EQ
	freq, gain    [engine.EQBands]*Knob
	q             [engine.EQBands]*Knob // nil nos shelves
	level         *Knob
	freqs, respDB []float64

	plotSize  image.Point // tamanho da curva no último quadro (coordenadas dos eventos)
	handleR   float32     // raio de captura dos pontos (px)
	drag      int         // banda sendo arrastada (−1 = nenhuma)
	hover     int         // banda sob o ponteiro (−1 = nenhuma)
	lastPress time.Time
	lastBand  int
}

// Conversões entre a curva e os valores (w, h = tamanho da curva em px).
func eqX(f float64, w float32) float32 {
	return w * float32(math.Log(f/eqPlotFMin)/math.Log(eqPlotFMax/eqPlotFMin))
}

func eqY(db float64, h float32) float32 {
	db = math.Max(-eqPlotDB, math.Min(eqPlotDB, db))
	return h/2 - float32(db/eqPlotDB)*(h/2-4)
}

func eqFreqAt(x, w float32) float64 {
	return eqPlotFMin * math.Pow(eqPlotFMax/eqPlotFMin, float64(max(0, min(1, x/w))))
}

func eqGainAt(y, h float32) float64 { return float64((h/2-y)/(h/2-4)) * eqPlotDB }

// handle devolve a posição do ponto da banda i.
func (v *eqView) handle(i int) f32.Point {
	w, h := float32(v.plotSize.X), float32(v.plotSize.Y)
	return f32.Pt(eqX(v.freq[i].P.Get(), w), eqY(v.gain[i].P.Get(), h))
}

// handleAt devolve a banda cujo ponto está mais perto de p (dentro do raio), ou −1.
func (v *eqView) handleAt(p f32.Point) int {
	best, bi := v.handleR*v.handleR, -1
	for i := range v.freq {
		c := v.handle(i)
		if d := (c.X-p.X)*(c.X-p.X) + (c.Y-p.Y)*(c.Y-p.Y); d <= best {
			best, bi = d, i
		}
	}
	return bi
}

// dragTo leva o ponto da banda i para p (a faixa de cada banda limita a frequência).
func (v *eqView) dragTo(i int, p f32.Point) {
	w, h := float32(v.plotSize.X), float32(v.plotSize.Y)
	v.freq[i].P.Set(eqFreqAt(p.X, w))
	v.gain[i].P.Set(math.Round(eqGainAt(p.Y, h)*10) / 10)
}

// scrollQ muda o Q da banda i com a roda (para cima = mais estreito); nada nos shelves.
func (v *eqView) scrollQ(i int, dy float32) {
	if q := v.q[i]; q != nil {
		q.P.Set(q.P.Get() * math.Exp(-float64(dy)*0.004))
	}
}

// press trata um clique: começa a arrastar o ponto; duplo clique zera o ganho da banda.
func (v *eqView) press(p f32.Point, now time.Time) {
	i := v.handleAt(p)
	v.drag = i
	if i < 0 {
		return
	}
	if i == v.lastBand && now.Sub(v.lastPress) < 350*time.Millisecond {
		v.gain[i].P.Set(0)
		v.drag = -1
	}
	v.lastPress, v.lastBand = now, i
}

func (v *eqView) update(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: v,
			Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Leave | pointer.Scroll,
			ScrollY: pointer.ScrollRange{Min: -1000, Max: 1000}})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			v.press(e.Position, gtx.Now)
		case pointer.Drag:
			if v.drag >= 0 {
				v.dragTo(v.drag, e.Position)
			}
		case pointer.Move:
			v.hover = v.handleAt(e.Position)
		case pointer.Leave:
			v.hover = -1
		case pointer.Scroll:
			if i := v.handleAt(e.Position); i >= 0 {
				v.scrollQ(i, e.Scroll.Y)
			}
		default: // soltou ou cancelou
			v.drag = -1
		}
	}
}

func newEQView(m *engine.EQ) *eqView {
	v := &eqView{eq: m, freqs: make([]float64, eqPlotSteps), respDB: make([]float64, eqPlotSteps), drag: -1, hover: -1, lastBand: -1}
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
	v.update(gtx)
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
	v.plotSize, v.handleR = sz, float32(gtx.Dp(12))
	fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(6), rgb(0x15181c))
	w, h := float32(sz.X), float32(sz.Y)
	x := func(f float64) float32 { return eqX(f, w) }
	y := func(db float64) float32 { return eqY(db, h) }
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
	// pontos das bandas, em (frequência, ganho da banda); o ativo fica maior e com o valor
	active := v.drag
	if active < 0 {
		active = v.hover
	}
	for i := range v.freq {
		c := v.handle(i)
		r := float32(gtx.Dp(5))
		if i == active {
			r = float32(gtx.Dp(7))
			fillCircle(gtx.Ops, c, r+float32(gtx.Dp(3)), withAlpha(curve, 60))
		}
		fillCircle(gtx.Ops, c, r, curve)
		fillCircle(gtx.Ops, c, r-float32(gtx.Dp(2)), rgb(0x15181c))
		func() {
			defer offset(gtx.Ops, int(c.X)-gtx.Dp(4), int(c.Y)-gtx.Dp(7)).Pop()
			drawText(gtx, th, fmt.Sprint(i+1), textOpts{size: 9, color: curve, bold: true, width: gtx.Dp(8), align: text.Middle})
		}()
	}
	if active >= 0 {
		info := fmt.Sprintf("%s: %s · %s", eqBandTitles[active], FormatValue(v.freq[active].P.Info(), v.freq[active].P.Get()),
			FormatValue(v.gain[active].P.Info(), v.gain[active].P.Get()))
		if q := v.q[active]; q != nil {
			info += " · Q " + FormatValue(q.P.Info(), q.P.Get())
		}
		func() {
			defer offset(gtx.Ops, gtx.Dp(36), gtx.Dp(4)).Pop()
			drawText(gtx, th, info, textOpts{size: 11, color: rgb(0xffffff), bold: true})
		}()
	}
	area := clip.Rect{Max: sz}.Push(gtx.Ops)
	event.Op(gtx.Ops, v)
	switch {
	case v.drag >= 0:
		pointer.CursorGrabbing.Add(gtx.Ops)
	case v.hover >= 0:
		pointer.CursorGrab.Add(gtx.Ops)
	}
	area.Pop()
	return layout.Dimensions{Size: sz}
}
