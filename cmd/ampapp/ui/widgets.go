package ui

import (
	"image"
	"image/color"

	"gioui.org/f32"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

// smallButton é um botão retangular com texto (width 0 = ajusta ao texto).
func smallButton(gtx layout.Context, th *Theme, c *widget.Clickable, label string, width int) layout.Dimensions {
	return coloredButton(gtx, th, c, label, width, th.PanelHi, th.Text)
}

func coloredButton(gtx layout.Context, th *Theme, c *widget.Clickable, label string, width int, bg, fg color.NRGBA) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		h := gtx.Dp(28)
		padX := gtx.Dp(10)
		// mede o texto
		m := recordText(gtx, th, label, 13, fg)
		w := width
		if w <= 0 {
			w = m + 2*padX
		}
		b := bg
		if c.Hovered() {
			b = lighten(bg, 0.08)
		}
		if c.Pressed() {
			b = darken(bg, 0.15)
		}
		fillRRect(gtx.Ops, image.Rectangle{Max: image.Pt(w, h)}, gtx.Dp(5), b)
		defer offset(gtx.Ops, padX, (h-gtx.Sp(17))/2).Pop()
		drawText(gtx, th, label, textOpts{size: 13, color: fg, width: w - 2*padX, align: textAlign(width)})
		return layout.Dimensions{Size: image.Pt(w, h)}
	})
}

func textAlign(width int) text.Alignment {
	if width > 0 {
		return text.Start
	}
	return text.Middle
}

// recordText mede a largura de um texto sem desenhá-lo (a gravação é descartada).
func recordText(gtx layout.Context, th *Theme, s string, size unit.Sp, c color.NRGBA) int {
	m := op.Record(gtx.Ops)
	gtx.Constraints.Min.X = 0
	d := drawText(gtx, th, s, textOpts{size: size, color: c})
	m.Stop()
	return d.Size.X
}

// closeButton é um botão redondo com um X desenhado (sem depender de glifo da fonte).
func closeButton(gtx layout.Context, c *widget.Clickable, size unit.Dp) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		d := gtx.Dp(size)
		r := float32(d) / 2
		bg := withAlpha(rgb(0x000000), 90)
		if c.Hovered() {
			bg = withAlpha(rgb(0x000000), 150)
		}
		fillCircle(gtx.Ops, f32.Pt(r, r), r, bg)
		k := r * 0.42
		w := float32(gtx.Dp(2))
		strokeLine(gtx.Ops, f32.Pt(r-k, r-k), f32.Pt(r+k, r+k), w, rgb(0xffffff))
		strokeLine(gtx.Ops, f32.Pt(r-k, r+k), f32.Pt(r+k, r-k), w, rgb(0xffffff))
		return layout.Dimensions{Size: image.Pt(d, d)}
	})
}

// arrowButton é um botão com um triângulo desenhado (dir = −1 esquerda, +1 direita).
func arrowButton(gtx layout.Context, th *Theme, c *widget.Clickable, dir int) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		sz := image.Pt(gtx.Dp(32), gtx.Dp(28))
		bg := th.PanelHi
		if c.Hovered() {
			bg = lighten(bg, 0.08)
		}
		fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(5), bg)
		cx, cy := float32(sz.X)/2, float32(sz.Y)/2
		w, h := float32(gtx.Dp(5)), float32(gtx.Dp(6))
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(cx+float32(dir)*w, cy))
		p.LineTo(f32.Pt(cx-float32(dir)*w, cy-h))
		p.LineTo(f32.Pt(cx-float32(dir)*w, cy+h))
		p.Close()
		paint.FillShape(gtx.Ops, th.Text, clip.Outline{Path: p.End()}.Op())
		return layout.Dimensions{Size: sz}
	})
}
