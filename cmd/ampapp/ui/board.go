package ui

import (
	"image"
	"image/color"
	"math"
	"slices"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"

	"github.com/tatianno/ampsim/engine"
)

// Board mostra a cadeia na ordem real, com o loop de efeitos destacado.
//
//   - clicar num pedal abre o pedal (OnOpen);
//   - clicar no LED de um bloco liga/desliga;
//   - arrastar um pedal reordena a cadeia (soltar entre PRÉ e POWER = loop de efeitos).
//
// Os blocos do amp (PRÉ, POWER, CAB) ficam fixos; só os pedais se movem.
type Board struct {
	chain  *engine.Chain
	OnOpen func(id string)

	chips map[string]*chip

	// geometria do último quadro (os eventos chegam nas coordenadas dele)
	lastIDs      []string
	chipW, gapPx int
	chipH, top   int

	drag struct {
		id      string
		pressed bool
		active  bool // passou do limiar: é um arrasto, não um clique
		startX  float32
		x       float32 // posição do ponteiro na faixa
		onLED   bool
	}
}

type chip struct{ id string }

// NewBoard cria a faixa da cadeia.
func NewBoard(chain *engine.Chain) *Board {
	// lastIDs já começa com a ordem atual: os filtros de evento dos blocos precisam ser
	// registrados desde o primeiro quadro, senão o primeiro clique é descartado.
	return &Board{chain: chain, chips: map[string]*chip{}, lastIDs: chain.Order()}
}

func (b *Board) chip(id string) *chip {
	c := b.chips[id]
	if c == nil {
		c = &chip{id: id}
		b.chips[id] = c
	}
	return c
}

func isAmpBlock(id string) bool {
	return id == engine.PreID || id == engine.PowerID || id == engine.CabID
}

func (b *Board) slotX(i int) int { return i * (b.chipW + b.gapPx) }

// Dragging informa o pedal sendo arrastado ("" se nenhum).
func (b *Board) Dragging() string {
	if b.drag.active {
		return b.drag.id
	}
	return ""
}

// dropIndex calcula a posição final do pedal arrastado a partir do x do ponteiro:
// quantos outros blocos têm o centro à esquerda dele.
func (b *Board) dropIndex(ids []string, id string, x float32) int {
	n := 0
	for i, other := range ids {
		if other == id {
			continue
		}
		if float32(b.slotX(i))+float32(b.chipW)/2 < x {
			n++
		}
	}
	return n
}

func (b *Board) update(gtx layout.Context) {
	threshold := float32(gtx.Dp(6))
	for i, id := range b.lastIDs {
		c := b.chip(id)
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: c, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
			if !ok {
				break
			}
			e, ok := ev.(pointer.Event)
			if !ok {
				continue
			}
			x := float32(b.slotX(i)) + e.Position.X // coordenada na faixa
			switch e.Kind {
			case pointer.Press:
				b.drag.id, b.drag.pressed, b.drag.active = id, true, false
				b.drag.startX, b.drag.x = x, x
				b.drag.onLED = b.ledHit(e.Position)
			case pointer.Drag:
				if b.drag.id != id || !b.drag.pressed {
					continue
				}
				b.drag.x = x
				if !isAmpBlock(id) && math.Abs(float64(x-b.drag.startX)) > float64(threshold) {
					b.drag.active = true
				}
			case pointer.Release:
				if b.drag.id == id && b.drag.pressed {
					switch {
					case b.drag.active:
						b.chain.Move(id, b.dropIndex(b.lastIDs, id, x))
					case b.drag.onLED:
						b.chain.SetEnabled(id, !b.chain.Enabled(id))
					case b.OnOpen != nil:
						b.OnOpen(id)
					}
				}
				b.drag.id, b.drag.pressed, b.drag.active = "", false, false
			case pointer.Cancel:
				b.drag.id, b.drag.pressed, b.drag.active = "", false, false
			}
		}
	}
}

// LED fica no canto superior direito do bloco.
func (b *Board) ledCenter() f32.Point {
	return f32.Pt(float32(b.chipW)-float32(b.chipH)*0.18, float32(b.chipH)*0.2)
}

func (b *Board) ledHit(p f32.Point) bool {
	c := b.ledCenter()
	r := float32(b.chipH) * 0.2
	return (p.X-c.X)*(p.X-c.X)+(p.Y-c.Y)*(p.Y-c.Y) <= r*r
}

// Layout desenha a faixa.
func (b *Board) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	b.update(gtx)

	ids := b.chain.Order()
	n := len(ids)
	b.gapPx = gtx.Dp(10)
	b.chipW = min(gtx.Dp(108), (gtx.Constraints.Max.X-(n-1)*b.gapPx)/n)
	b.chipH = gtx.Dp(64)
	b.top = gtx.Dp(22)
	totalW := b.slotX(n-1) + b.chipW
	height := b.top + b.chipH + gtx.Dp(10)

	pre, pow := slices.Index(ids, engine.PreID), slices.Index(ids, engine.PowerID)

	// fundo do loop de efeitos (entre PRÉ e POWER)
	if pre >= 0 && pow > pre {
		x0 := b.slotX(pre) + b.chipW + b.gapPx/4
		x1 := b.slotX(pow) - b.gapPx/4
		r := image.Rect(x0, b.top-gtx.Dp(18), x1, b.top+b.chipH+gtx.Dp(6))
		fillRRect(gtx.Ops, r, gtx.Dp(8), th.Loop)
		label := "LOOP DE EFEITOS"
		if pow == pre+1 {
			label = "LOOP"
		}
		func() {
			defer offset(gtx.Ops, x0, b.top-gtx.Dp(17)).Pop()
			drawText(gtx, th, label, textOpts{size: 10, color: withAlpha(th.Gold, 190), bold: true, width: x1 - x0, align: text.Middle})
		}()
	}

	// cabo passando por todos os blocos
	mid := float32(b.top + b.chipH/2)
	strokeLine(gtx.Ops, f32.Pt(float32(b.chipW)/2, mid), f32.Pt(float32(b.slotX(n-1)+b.chipW/2), mid), float32(gtx.Dp(3)), th.Line)

	for i, id := range ids {
		func() {
			defer offset(gtx.Ops, b.slotX(i), b.top).Pop()
			alpha := float32(1)
			if b.drag.active && b.drag.id == id {
				alpha = 0.3 // o original fica apagado enquanto o fantasma acompanha o mouse
			}
			b.drawChip(gtx, th, id, alpha)
			area := clip.Rect{Max: image.Pt(b.chipW, b.chipH)}.Push(gtx.Ops)
			event.Op(gtx.Ops, b.chip(id))
			if isAmpBlock(id) {
				pointer.CursorPointer.Add(gtx.Ops)
			} else if b.drag.active {
				pointer.CursorGrabbing.Add(gtx.Ops)
			} else {
				pointer.CursorGrab.Add(gtx.Ops)
			}
			area.Pop()
		}()
	}

	if b.drag.active {
		// marcador de onde o pedal vai cair
		idx := b.dropIndex(ids, b.drag.id, b.drag.x)
		others := slices.DeleteFunc(slices.Clone(ids), func(s string) bool { return s == b.drag.id })
		var mx int
		switch {
		case idx == 0:
			mx = -b.gapPx / 2
		case idx >= len(others):
			mx = b.slotX(n-1) + b.chipW + b.gapPx/2
		default:
			// entre others[idx-1] e others[idx], nas posições atuais
			left := slices.Index(ids, others[idx-1])
			mx = b.slotX(left) + b.chipW + b.gapPx/2
		}
		fillRRect(gtx.Ops, image.Rect(mx-gtx.Dp(2), b.top-gtx.Dp(4), mx+gtx.Dp(2), b.top+b.chipH+gtx.Dp(4)), gtx.Dp(2), th.Gold)
		// fantasma
		gx := int(b.drag.x) - b.chipW/2
		func() {
			defer offset(gtx.Ops, gx, b.top-gtx.Dp(6)).Pop()
			b.drawChip(gtx, th, b.drag.id, 0.85)
		}()
		gtx.Execute(op.InvalidateCmd{})
	}
	b.lastIDs = ids
	return layout.Dimensions{Size: image.Pt(max(totalW, gtx.Constraints.Min.X), height)}
}

func (b *Board) drawChip(gtx layout.Context, th *Theme, id string, alpha float32) {
	m := b.chain.Module(id)
	if m == nil {
		return
	}
	defer paint.PushOpacity(gtx.Ops, alpha).Pop()
	on := b.chain.Enabled(id)
	st := StyleFor(id)
	sz := image.Pt(b.chipW, b.chipH)
	body, ink := st.Body, st.Text
	if isAmpBlock(id) {
		body, ink = th.Tolex, th.Gold
		if id == engine.CabID {
			body, ink = th.Grill, rgb(0xd9c7a3)
		}
	}
	if !on {
		body = mix(body, th.Panel, 0.65)
		ink = withAlpha(ink, 150)
	}
	fillRRect(gtx.Ops, image.Rect(0, gtx.Dp(3), sz.X, sz.Y+gtx.Dp(3)), gtx.Dp(8), withAlpha(rgb(0x000000), 90))
	fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(8), body)
	if isAmpBlock(id) {
		strokeRRect(gtx, image.Rectangle{Max: sz}, gtx.Dp(8), withAlpha(ink, 120))
	}
	led(gtx.Ops, b.ledCenter(), float32(gtx.Dp(4)), on, th.LedOn, th.LedOff)

	name := m.Name()
	switch id {
	case "comp":
		name = "COMP" // "Compressor" não cabe no bloco
	case engine.PreID:
		name = "PRÉ"
	case engine.PowerID:
		name = "POWER"
	case engine.CabID:
		name = "CAB"
	}
	defer offset(gtx.Ops, gtx.Dp(6), sz.Y/2-gtx.Sp(9)).Pop()
	drawText(gtx, th, strings.ToUpper(name), textOpts{size: 12, color: ink, bold: true, width: sz.X - gtx.Dp(12), align: text.Middle})
}

func strokeRRect(gtx layout.Context, r image.Rectangle, radius int, c color.NRGBA) {
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: clip.UniformRRect(r, radius).Path(gtx.Ops), Width: 1}.Op())
}
