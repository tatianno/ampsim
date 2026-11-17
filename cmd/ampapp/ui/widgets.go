package ui

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
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
