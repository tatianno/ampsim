package ui

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

func fillRRect(ops *op.Ops, r image.Rectangle, radius int, c color.NRGBA) {
	paint.FillShape(ops, c, clip.UniformRRect(r, radius).Op(ops))
}

func fillCircle(ops *op.Ops, center f32.Point, radius float32, c color.NRGBA) {
	r := image.Rect(int(center.X-radius), int(center.Y-radius), int(center.X+radius), int(center.Y+radius))
	paint.FillShape(ops, c, clip.Ellipse(r).Op(ops))
}

// strokeArc desenha um arco de a0 a a1 (radianos, sentido horário na tela, 0 = direita).
func strokeArc(ops *op.Ops, center f32.Point, radius, width float32, a0, a1 float64, c color.NRGBA) {
	if a1 <= a0 {
		return
	}
	n := max(2, int((a1-a0)/(math.Pi/48)))
	var p clip.Path
	p.Begin(ops)
	for i := 0; i <= n; i++ {
		a := a0 + (a1-a0)*float64(i)/float64(n)
		pt := f32.Pt(center.X+radius*float32(math.Cos(a)), center.Y+radius*float32(math.Sin(a)))
		if i == 0 {
			p.MoveTo(pt)
		} else {
			p.LineTo(pt)
		}
	}
	paint.FillShape(ops, c, clip.Stroke{Path: p.End(), Width: width}.Op())
}

func strokeLine(ops *op.Ops, a, b f32.Point, width float32, c color.NRGBA) {
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(a)
	p.LineTo(b)
	paint.FillShape(ops, c, clip.Stroke{Path: p.End(), Width: width}.Op())
}

// led desenha um LED redondo (com brilho quando aceso).
func led(ops *op.Ops, center f32.Point, radius float32, on bool, onColor, offColor color.NRGBA) {
	if on {
		fillCircle(ops, center, radius*2, withAlpha(onColor, 50))
		fillCircle(ops, center, radius, onColor)
		fillCircle(ops, f32.Pt(center.X-radius*0.3, center.Y-radius*0.3), radius*0.35, withAlpha(rgb(0xffffff), 140))
	} else {
		fillCircle(ops, center, radius, offColor)
	}
}

type textOpts struct {
	size  unit.Sp
	color color.NRGBA
	align text.Alignment
	bold  bool
	width int // > 0: largura fixa (para centralizar)
}

func drawText(gtx layout.Context, th *Theme, s string, o textOpts) layout.Dimensions {
	if o.size == 0 {
		o.size = 13
	}
	l := material.Label(th.M, o.size, s)
	l.Color = o.color
	l.Alignment = o.align
	l.MaxLines = 1
	if o.bold {
		l.Font.Weight = font.Bold
	}
	if o.width > 0 {
		gtx.Constraints.Min.X = o.width
		gtx.Constraints.Max.X = o.width
	}
	gtx.Constraints.Min.Y = 0
	return l.Layout(gtx)
}

// panel desenha um retângulo arredondado atrás do conteúdo, com margem interna.
func panel(gtx layout.Context, bg color.NRGBA, radius, inset unit.Dp, w layout.Widget) layout.Dimensions {
	m := op.Record(gtx.Ops)
	dims := layout.UniformInset(inset).Layout(gtx, w)
	call := m.Stop()
	fillRRect(gtx.Ops, image.Rectangle{Max: dims.Size}, gtx.Dp(radius), bg)
	call.Add(gtx.Ops)
	return dims
}

func spacer(w, h unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(w), gtx.Dp(h))}
	}
}

func offset(ops *op.Ops, x, y int) op.TransformStack {
	return op.Offset(image.Pt(x, y)).Push(ops)
}
