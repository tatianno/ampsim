package ui

import (
	"image"
	"path/filepath"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/engine"
)

// Head é o cabeçote: knobs do pré-amp e do power amp num painel só, com chaves de liga/desliga.
type Head struct {
	chain      *engine.Chain
	stackSel   *Selector // tone stack: Moderno / Fender / Marshall
	preKnobs   []*Knob
	powerKnobs []*Knob
	preSw      widget.Clickable
	powerSw    widget.Clickable
}

// NewHead cria o cabeçote.
func NewHead(chain *engine.Chain) *Head {
	h := &Head{chain: chain}
	if m := chain.Module(engine.PreID); m != nil {
		for _, p := range m.Params() {
			if p.Info().Step > 0 {
				h.stackSel = NewSelector(p)
			} else {
				h.preKnobs = append(h.preKnobs, NewKnob(p))
			}
		}
	}
	if m := chain.Module(engine.PowerID); m != nil {
		for _, p := range m.Params() {
			h.powerKnobs = append(h.powerKnobs, NewKnob(p))
		}
	}
	return h
}

// Layout desenha o cabeçote.
func (h *Head) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	if h.preSw.Clicked(gtx) {
		h.chain.SetEnabled(engine.PreID, !h.chain.Enabled(engine.PreID))
	}
	if h.powerSw.Clicked(gtx) {
		h.chain.SetEnabled(engine.PowerID, !h.chain.Enabled(engine.PowerID))
	}
	return panel(gtx, th.Tolex, 14, 10, func(gtx layout.Context) layout.Dimensions {
		return panel(gtx, th.Faceplate, 8, 12, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return drawText(gtx, th, "AMPSIM", textOpts{size: 18, color: th.Gold, bold: true})
						}),
						layout.Flexed(1, hfill),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if h.stackSel == nil {
								return layout.Dimensions{}
							}
							return h.stackSel.Layout(gtx, th, withAlpha(th.Gold, 220), th.Gold)
						}),
					)
				}),
				layout.Rigid(spacer(0, 6)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.End}.Layout(gtx,
						layout.Flexed(4, func(gtx layout.Context) layout.Dimensions {
							return h.section(gtx, th, "PRÉ-AMP", engine.PreID, &h.preSw, h.preKnobs)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { // divisória
							sz := image.Pt(gtx.Dp(14), gtx.Dp(110))
							x := float32(sz.X) / 2
							strokeLine(gtx.Ops, f32.Pt(x, 0), f32.Pt(x, float32(sz.Y)), 1, withAlpha(th.Gold, 90))
							return layout.Dimensions{Size: sz}
						}),
						layout.Flexed(2.2, func(gtx layout.Context) layout.Dimensions {
							return h.section(gtx, th, "POWER", engine.PowerID, &h.powerSw, h.powerKnobs)
						}),
					)
				}),
			)
		})
	})
}

func (h *Head) section(gtx layout.Context, th *Theme, title, id string, sw *widget.Clickable, knobs []*Knob) layout.Dimensions {
	on := h.chain.Enabled(id)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, title, textOpts{size: 11, color: withAlpha(th.Gold, 200), bold: true})
				}),
				layout.Rigid(spacer(8, 0)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return toggle(gtx, th, sw, on, "ON")
				}),
			)
		}),
		layout.Rigid(spacer(0, 6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var ch []layout.FlexChild
			for _, k := range knobs {
				ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return k.Layout(gtx, th, 56, rgb(0x161616), th.Gold, th.Gold)
					})
				}))
			}
			return layout.Flex{}.Layout(gtx, ch...)
		}),
	)
}

// toggle é um botãozinho com LED, para ligar/desligar.
func toggle(gtx layout.Context, th *Theme, c *widget.Clickable, on bool, label string) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// largura pelo texto: LED (18 dp) + texto + margem
		sz := image.Pt(gtx.Dp(26)+recordText(gtx, th, label, 11, th.Text), gtx.Dp(20))
		bg := withAlpha(rgb(0x000000), 90)
		if c.Hovered() {
			bg = withAlpha(rgb(0x000000), 140)
		}
		fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(10), bg)
		led(gtx.Ops, f32.Pt(float32(gtx.Dp(11)), float32(sz.Y)/2), float32(gtx.Dp(4)), on, th.LedOn, th.LedOff)
		defer offset(gtx.Ops, gtx.Dp(18), (sz.Y-gtx.Sp(15))/2).Pop()
		drawText(gtx, th, label, textOpts{size: 11, color: th.Text, bold: true})
		return layout.Dimensions{Size: sz}
	})
}

// CabPanel é o gabinete: arquivo de IR, low cut, high cut e liga/desliga.
type CabPanel struct {
	chain  *engine.Chain
	knobs  []*Knob
	sw     widget.Clickable
	pick   widget.Clickable
	OnPick func() // abre o seletor de arquivo da IR
}

// NewCabPanel cria o painel do gabinete.
func NewCabPanel(chain *engine.Chain) *CabPanel {
	c := &CabPanel{chain: chain}
	if m := chain.Module(engine.CabID); m != nil {
		for _, p := range m.Params() {
			c.knobs = append(c.knobs, NewKnob(p))
		}
	}
	return c
}

// CabWidthDp é a largura do painel do gabinete.
const CabWidthDp = 300

// Layout desenha o gabinete.
func (c *CabPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	if c.sw.Clicked(gtx) {
		c.chain.SetEnabled(engine.CabID, !c.chain.Enabled(engine.CabID))
	}
	if c.pick.Clicked(gtx) && c.OnPick != nil {
		c.OnPick()
	}
	ir := "interna (genérica)"
	if cab := c.chain.Cab(); cab != nil && cab.IRPath() != "" {
		ir = filepath.Base(cab.IRPath())
	}
	gtx.Constraints.Min.X = gtx.Dp(CabWidthDp)
	gtx.Constraints.Max.X = gtx.Dp(CabWidthDp)
	return panel(gtx, th.Grill, 14, 12, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, "GABINETE", textOpts{size: 13, color: rgb(0xd9c7a3), bold: true})
					}),
					layout.Rigid(spacer(8, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return toggle(gtx, th, &c.sw, c.chain.Enabled(engine.CabID), "ON")
					}),
				)
			}),
			layout.Rigid(spacer(0, 8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return smallButton(gtx, th, &c.pick, "IR: "+ir, gtx.Constraints.Max.X)
			}),
			layout.Rigid(spacer(0, 8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var ch []layout.FlexChild
				for _, k := range c.knobs {
					ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return k.Layout(gtx, th, 48, rgb(0x161616), rgb(0xd9c7a3), rgb(0xd9c7a3))
						})
					}))
				}
				return layout.Flex{}.Layout(gtx, ch...)
			}),
		)
	})
}
