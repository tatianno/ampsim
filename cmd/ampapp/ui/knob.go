package ui

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/engine"
)

// Interação do knob.
const (
	knobDragSpanDp = 180                    // arrastar 180 dp na vertical percorre a faixa inteira
	knobFineFactor = 5                      // com Shift, 5x mais preciso
	knobWheelStep  = 0.01                   // rodinha: 1% da faixa por "clique"
	doubleClick    = 350 * time.Millisecond // duplo clique volta ao padrão
)

// Ângulos do knob: de 135° (embaixo à esquerda) a 405° (embaixo à direita), passando pelo topo.
const (
	knobA0    = 135 * math.Pi / 180
	knobSweep = 270 * math.Pi / 180
)

// Knob é um botão giratório ligado a um engine.Param.
type Knob struct {
	P *engine.Param
	// Label troca o nome mostrado (vazio = o nome do parâmetro); ex.: o Drive vira "Fuzz".
	Label string

	dragging  bool
	acc       float64 // valor normalizado acumulado durante o arrasto (aceita parâmetros discretos)
	lastY     float32
	lastPress time.Duration
	hovered   bool
}

// NewKnob cria um knob para o parâmetro.
func NewKnob(p *engine.Param) *Knob { return &Knob{P: p} }

// Active informa se o knob está sendo arrastado ou com o mouse em cima (mostra o valor).
func (k *Knob) Active() bool { return k.dragging || k.hovered }

func (k *Knob) update(gtx layout.Context) {
	span := float32(gtx.Dp(knobDragSpanDp))
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target:  k,
			Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Scroll | pointer.Enter | pointer.Leave,
			ScrollY: pointer.ScrollRange{Min: -1000, Max: 1000},
		})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		fine := e.Modifiers.Contain(key.ModShift)
		switch e.Kind {
		case pointer.Press:
			if k.lastPress != 0 && e.Time-k.lastPress < doubleClick {
				k.P.Reset()
				k.dragging = false
				k.lastPress = 0
				continue
			}
			k.lastPress = e.Time
			k.dragging = true
			k.acc = k.P.Normalized()
			k.lastY = e.Position.Y
		case pointer.Drag:
			if !k.dragging {
				continue
			}
			s := span
			if fine {
				s *= knobFineFactor
			}
			k.acc = clamp01(k.acc + float64((k.lastY-e.Position.Y)/s))
			k.lastY = e.Position.Y
			k.P.SetNormalized(k.acc)
		case pointer.Release, pointer.Cancel:
			k.dragging = false
		case pointer.Scroll:
			step := knobWheelStep
			if st := k.P.Info().Step; st > 0 {
				step = st / (k.P.Info().Max - k.P.Info().Min)
			} else if fine {
				step /= knobFineFactor
			}
			dir := 1.0
			if e.Scroll.Y > 0 { // rodinha para baixo diminui
				dir = -1
			}
			k.P.SetNormalized(clamp01(k.P.Normalized() + dir*step))
		case pointer.Enter:
			k.hovered = true
		case pointer.Leave:
			k.hovered = false
		}
	}
}

// Layout desenha o knob: nome em cima, botão, valor embaixo.
// size é o diâmetro; cap é a cor do botão; ink a cor de nome/valor; accent a do arco.
func (k *Knob) Layout(gtx layout.Context, th *Theme, size unit.Dp, cap, ink, accent color.NRGBA) layout.Dimensions {
	k.update(gtx)
	d := gtx.Dp(size)
	w := max(d+gtx.Dp(8), gtx.Dp(68))
	info := k.P.Info()

	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			name := info.Name
			if k.Label != "" {
				name = k.Label
			}
			return drawText(gtx, th, name, textOpts{size: 12, color: ink, align: text.Middle, width: w, bold: true})
		}),
		layout.Rigid(spacer(0, 2)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(w, d)
			area := clip.Rect{Max: sz}.Push(gtx.Ops)
			event.Op(gtx.Ops, k)
			pointer.CursorGrab.Add(gtx.Ops)
			if k.dragging {
				pointer.CursorGrabbing.Add(gtx.Ops)
			}
			area.Pop()
			k.draw(gtx, sz, cap, accent)
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(spacer(0, 2)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			c := withAlpha(ink, 215)
			if k.Active() {
				c = ink
			}
			return drawText(gtx, th, FormatValue(info, k.P.Get()), textOpts{size: 11, color: c, align: text.Middle, width: w})
		}),
	)
}

func (k *Knob) draw(gtx layout.Context, sz image.Point, cap, accent color.NRGBA) {
	ops := gtx.Ops
	c := f32.Pt(float32(sz.X)/2, float32(sz.Y)/2)
	r := float32(sz.Y) / 2
	track := float32(gtx.Dp(3))
	n := k.P.Normalized()
	a := knobA0 + knobSweep*n

	// trilho e arco do valor
	strokeArc(ops, c, r-track/2, track, knobA0, knobA0+knobSweep, withAlpha(rgb(0x000000), 110))
	if in := k.P.Info(); in.Min < 0 && in.Max > 0 && in.Unit != "dB" {
		// parâmetro bipolar (ex.: feedback −90..+90%): o arco parte do zero, no meio
		z := knobA0 + knobSweep*(-in.Min/(in.Max-in.Min))
		strokeArc(ops, c, r-track/2, track, math.Min(z, a), math.Max(z, a), accent)
	} else {
		strokeArc(ops, c, r-track/2, track, knobA0, a, accent)
	}
	// corpo do botão
	body := r - track*2.2
	fillCircle(ops, f32.Pt(c.X, c.Y+body*0.06), body, darken(cap, 0.5)) // sombra
	fillCircle(ops, c, body, cap)
	fillCircle(ops, c, body*0.82, lighten(cap, 0.08))
	// ponteiro
	cos, sin := float32(math.Cos(a)), float32(math.Sin(a))
	p0 := f32.Pt(c.X+cos*body*0.25, c.Y+sin*body*0.25)
	p1 := f32.Pt(c.X+cos*body*0.9, c.Y+sin*body*0.9)
	strokeLine(ops, p0, p1, float32(gtx.Dp(2.5)), rgb(0xf5f5f5))
	if k.dragging {
		strokeArc(ops, c, body+1, 1.5, 0, 2*math.Pi, withAlpha(accent, 200))
	}
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// Selector é uma chave de posições para parâmetros discretos (ex.: estágios do phaser 4/6/8).
type Selector struct {
	P      *engine.Param
	values []float64
	btns   []widget.Clickable
	Inline bool // o nome sempre na mesma linha dos botões (senão, acima deles com 5 posições ou mais)
}

// NewSelector cria uma chave com todas as posições do parâmetro.
func NewSelector(p *engine.Param) *Selector {
	in := p.Info()
	s := &Selector{P: p}
	for v := in.Min; v <= in.Max+1e-9; v += in.Step {
		s.values = append(s.values, v)
	}
	s.btns = make([]widget.Clickable, len(s.values))
	return s
}

// Layout desenha o nome e as posições lado a lado.
func (s *Selector) Layout(gtx layout.Context, th *Theme, ink, accent color.NRGBA) layout.Dimensions {
	for i := range s.btns {
		if s.btns[i].Clicked(gtx) {
			s.P.Set(s.values[i])
		}
	}
	info := s.P.Info()
	name := func(gtx layout.Context) layout.Dimensions {
		return drawText(gtx, th, info.Name, textOpts{size: 12, color: ink, bold: true})
	}
	// muitas posições (ex.: os 5 modos do drive): o nome vai numa linha acima dos botões
	stacked := len(s.values) >= 5 && !s.Inline
	// sem espaço para o tamanho normal (a voz do amp numa janela estreita): versão compacta,
	// com menos margem nos botões e letra menor
	size, pad, gap, nameGap := unit.Sp(12), unit.Dp(14), unit.Dp(4), unit.Dp(8)
	need := func() int {
		w := 0
		if !stacked {
			w += recordText(gtx, th, info.Name, 12, ink) + gtx.Dp(nameGap)
		}
		for _, v := range s.values {
			w += max(gtx.Dp(30), recordText(gtx, th, FormatValue(info, v), size, ink)+gtx.Dp(pad)) + gtx.Dp(gap)
		}
		return w
	}
	if need() > gtx.Constraints.Max.X {
		size, pad, gap, nameGap = 11, 8, 2, 5
	}
	var children []layout.FlexChild
	if !stacked {
		children = append(children, layout.Rigid(name), layout.Rigid(spacer(nameGap, 0)))
	}
	for i := range s.values {
		i := i
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.btns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				sel := s.P.Get() == s.values[i]
				label := FormatValue(info, s.values[i])
				sz := image.Pt(max(gtx.Dp(30), recordText(gtx, th, label, size, ink)+gtx.Dp(pad)), gtx.Dp(22))
				bg := withAlpha(rgb(0x000000), 70)
				fg := ink
				if sel {
					bg, fg = accent, rgb(0x101010)
				}
				fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(4), bg)
				defer offset(gtx.Ops, 0, (sz.Y-gtx.Sp(size+4))/2).Pop()
				drawText(gtx, th, label, textOpts{size: size, color: fg, align: text.Middle, width: sz.X, bold: sel})
				return layout.Dimensions{Size: sz}
			})
		}), layout.Rigid(spacer(gap, 0)))
	}
	row := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
	if !stacked {
		return row(gtx)
	}
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(name), layout.Rigid(spacer(0, 4)), layout.Rigid(row))
}
