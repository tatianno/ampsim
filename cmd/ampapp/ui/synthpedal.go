package ui

import (
	"fmt"
	"image"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/text"

	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/engine"
)

// synthView é a parte própria do pedal Synth: o editor da sequência (barras que se arrastam,
// com o passo atual aceso), os knobs em duas linhas de 5 e as chaves de modo e de filtro.
type synthView struct {
	m          *engine.Synth
	knobs      [][]*Knob
	mode, kind *Selector
	steps      [dsp.SynthSteps]*engine.Param
	area       image.Point // tamanho do editor no último quadro
	dragging   bool
}

func newSynthView(m *engine.Synth) *synthView {
	v := &synthView{m: m, steps: m.Steps(), mode: NewSelector(m.Param("mode")), kind: NewSelector(m.Param("type"))}
	for _, row := range [][]string{{"cutoff", "res", "depth", "fm", "mix"}, {"sens", "rate", "glide", "drive", "level"}} {
		var ks []*Knob
		for _, id := range row {
			ks = append(ks, NewKnob(m.Param(id)))
		}
		v.knobs = append(v.knobs, ks)
	}
	return v
}

// setFrom ajusta o passo sob o ponteiro (x, y no editor).
func (v *synthView) setFrom(p f32.Point) {
	if v.area.X == 0 || v.area.Y == 0 {
		return
	}
	i := int(p.X / float32(v.area.X) * dsp.SynthSteps)
	i = max(0, min(dsp.SynthSteps-1, i))
	val := 1 - float64(p.Y)/float64(v.area.Y)
	v.steps[i].Set(100 * max(0, min(1, val)))
}

func (v *synthView) update(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: v, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			v.dragging = true
			v.setFrom(e.Position)
		case pointer.Drag:
			if v.dragging {
				v.setFrom(e.Position)
			}
		default:
			v.dragging = false
		}
	}
}

// controls desenha o editor, os knobs e as chaves.
func (v *synthView) controls(gtx layout.Context, th *Theme, st PedalStyle) layout.Dimensions {
	v.update(gtx)
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.sequence(gtx, th, st) }),
		layout.Rigid(spacer(0, 10)),
	}
	for _, row := range v.knobs {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var ch []layout.FlexChild
			for i := range 5 {
				ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if i >= len(row) {
						return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
					}
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return row[i].Layout(gtx, th, 40, st.Knob, st.Text, st.Text)
					})
				}))
			}
			return layout.Flex{}.Layout(gtx, ch...)
		}), layout.Rigid(spacer(0, 6)))
	}
	rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.mode.Layout(gtx, th, st.Text, st.Text)
			}),
			layout.Flexed(1, hfill),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.kind.Layout(gtx, th, st.Text, st.Text)
			}),
		)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// sequence desenha o editor dos passos.
func (v *synthView) sequence(gtx layout.Context, th *Theme, st PedalStyle) layout.Dimensions {
	sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(80))
	v.area = sz
	seq := v.m.Param("mode").Get() == dsp.SynthSequence
	fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(6), rgb(0x0b0c12))
	cur := v.m.CurrentStep()
	bw := float32(sz.X) / dsp.SynthSteps
	gap := float32(gtx.Dp(4))
	for i, p := range v.steps {
		h := float32(p.Get()/100) * float32(sz.Y-gtx.Dp(18))
		x0 := float32(i)*bw + gap/2
		c := withAlpha(st.Text, 90)
		if seq {
			c = withAlpha(st.Text, 170)
			if i == cur {
				c = st.Text
			}
		}
		r := image.Rect(int(x0), sz.Y-gtx.Dp(14)-int(h), int(x0+bw-gap), sz.Y-gtx.Dp(14))
		fillRRect(gtx.Ops, r, gtx.Dp(2), c)
		func() {
			defer offset(gtx.Ops, int(x0), sz.Y-gtx.Dp(14)).Pop()
			drawText(gtx, th, fmt.Sprint(i+1), textOpts{size: 10, color: withAlpha(st.Text, 140),
				width: int(bw - gap), align: text.Middle})
		}()
	}
	if !seq {
		func() {
			defer offset(gtx.Ops, 0, gtx.Dp(6)).Pop()
			drawText(gtx, th, "sequência (Modo: Sequência)", textOpts{size: 10, color: withAlpha(st.Text, 140),
				width: sz.X, align: text.Middle})
		}()
	}
	area := clip.Rect{Max: sz}.Push(gtx.Ops)
	event.Op(gtx.Ops, v)
	pointer.CursorPointer.Add(gtx.Ops)
	area.Pop()
	return layout.Dimensions{Size: sz}
}
