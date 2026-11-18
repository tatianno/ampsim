package ui

import (
	"fmt"
	"image"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/widget"
)

// BoardSlots é o número de footswitches por banco (como num controlador MIDI de 6 botões).
const BoardSlots = 6

// Pedalboard é a pedaleira de presets: footswitches de acesso rápido, em bancos de 6, com o
// LED aceso no preset atual. As teclas 1 a 6 acionam os footswitches.
type Pedalboard struct {
	pb         *PresetBar
	bank       int
	lastCur    int
	sw         [BoardSlots]widget.Clickable
	prev, next widget.Clickable
}

// NewPedalboard cria a pedaleira sobre a lista da barra de presets.
func NewPedalboard(pb *PresetBar) *Pedalboard { return &Pedalboard{pb: pb, lastCur: -1} }

func (b *Pedalboard) banks() int { return max(1, (len(b.pb.list)+BoardSlots-1)/BoardSlots) }

// Press aciona o footswitch i (0..5) do banco atual.
func (b *Pedalboard) Press(i int) {
	if k := b.bank*BoardSlots + i; i >= 0 && i < BoardSlots && k < len(b.pb.list) {
		b.pb.Load(k)
	}
}

// Bank devolve o banco mostrado (0, 1, ...).
func (b *Pedalboard) Bank() int { return b.bank }

// SetBank muda o banco mostrado (dá a volta nas pontas).
func (b *Pedalboard) SetBank(n int) { b.bank = (n%b.banks() + b.banks()) % b.banks() }

// Layout desenha a pedaleira.
func (b *Pedalboard) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	// o banco acompanha o preset atual quando ele muda por outro caminho
	if cur := b.pb.cur; cur != b.lastCur {
		b.lastCur = cur
		if cur >= 0 {
			b.bank = cur / BoardSlots
		}
	}
	b.bank = min(b.bank, b.banks()-1)
	if b.prev.Clicked(gtx) {
		b.SetBank(b.bank - 1)
	}
	if b.next.Clicked(gtx) {
		b.SetBank(b.bank + 1)
	}
	for i := range b.sw {
		if b.sw[i].Clicked(gtx) {
			b.Press(i)
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "PEDALEIRA", textOpts{size: 12, color: th.Dim, bold: true})
				}),
				layout.Rigid(spacer(0, 4)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &b.prev, "◀", 0) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return drawText(gtx, th, fmt.Sprintf("BANCO %d/%d", b.bank+1, b.banks()),
								textOpts{size: 11, color: th.Gold, bold: true, width: gtx.Dp(84), align: text.Middle})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &b.next, "▶", 0) }),
					)
				}),
				layout.Rigid(spacer(0, 4)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "teclas 1 a 6", textOpts{size: 10, color: withAlpha(th.Dim, 180)})
				}),
			)
		}),
		layout.Rigid(spacer(16, 0)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			var cells []layout.FlexChild
			for i := range BoardSlots {
				cells = append(cells, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return b.slot(gtx, th, i)
				}))
			}
			return layout.Flex{}.Layout(gtx, cells...)
		}),
	)
}

// slot desenha um footswitch: LED, botão, número e o nome do preset.
func (b *Pedalboard) slot(gtx layout.Context, th *Theme, i int) layout.Dimensions {
	k := b.bank*BoardSlots + i
	name, on, has := "—", false, k < len(b.pb.list)
	if has {
		name = b.pb.list[k].Name
		on = k == b.pb.cur
	}
	w := gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(w, gtx.Dp(10))
			led(gtx.Ops, f32.Pt(float32(w)/2, float32(sz.Y)/2), float32(gtx.Dp(4)), on, th.LedOn, th.LedOff)
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(spacer(0, 1)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !has {
				d := gtx.Dp(32)
				fillCircle(gtx.Ops, f32.Pt(float32(d)/2, float32(d)/2), float32(d)/2, withAlpha(rgb(0x000000), 60))
				return layout.Dimensions{Size: image.Pt(d, d)}
			}
			return footswitch(gtx, &b.sw[i], 32)
		}),
		layout.Rigid(spacer(0, 3)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			c := th.Text
			if on {
				c = th.Gold
			} else if !has {
				c = th.Dim
			}
			return drawText(gtx, th, fmt.Sprintf("%d · %s", i+1, name), textOpts{size: 11, color: c, bold: on, width: w, align: text.Middle})
		}),
	)
}
