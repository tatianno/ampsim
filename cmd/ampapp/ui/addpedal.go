package ui

import (
	"image"
	"slices"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/engine"
)

// AddPedalID é a sobreposição "adicionar pedal".
const AddPedalID = "__adicionar__"

// pedalGroups organiza os pedais na lista de adicionar (os que não existirem são ignorados).
var pedalGroups = []struct {
	Title string
	IDs   []string
}{
	{"DINÂMICA", []string{"gate", "comp"}},
	{"DRIVE", []string{"drive"}},
	{"FILTRO E EQ", []string{"wah", "eq"}},
	{"MODULAÇÃO", []string{"chorus", "flanger", "phaser", "vibrato", "tremolo", "rotary", "vibe"}},
	{"TOM", []string{"octave", "pitch"}},
	{"AMBIÊNCIA", []string{"delay", "reverb"}},
}

// AddPedal é a lista de pedais fora da cadeia, para adicionar.
type AddPedal struct {
	chain *engine.Chain
	btns  map[string]*widget.Clickable
	// OnAdded é chamado depois de adicionar (o app abre o pedal).
	OnAdded func(id string)
}

// NewAddPedal cria a lista.
func NewAddPedal(chain *engine.Chain) *AddPedal {
	a := &AddPedal{chain: chain, btns: map[string]*widget.Clickable{}}
	for _, m := range chain.AllModules() {
		a.btns[m.ID()] = new(widget.Clickable)
	}
	return a
}

// Layout desenha a lista (sobreposição).
func (a *AddPedal) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	for id, b := range a.btns {
		if b.Clicked(gtx) && a.chain.Add(id) == nil && a.OnAdded != nil {
			a.OnAdded(id)
		}
	}
	avail := map[string]engine.Module{}
	for _, m := range a.chain.Available() {
		avail[m.ID()] = m
	}
	w := gtx.Dp(460)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return panel(gtx, th.Panel, 14, 16, func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "ADICIONAR PEDAL", textOpts{size: 18, color: th.Text, bold: true})
		})}
		any := false
		for _, g := range pedalGroups {
			var ids []string
			for _, id := range g.IDs {
				if avail[id] != nil {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				continue
			}
			any = true
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, g.Title, textOpts{size: 11, color: th.Gold, bold: true})
				})
			}))
			for row := range slices.Chunk(ids, 3) {
				ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					var cells []layout.FlexChild
					for i := range 3 {
						cells = append(cells, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							if i >= len(row) {
								return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
							}
							id := row[i]
							st := StyleFor(id)
							return layout.Inset{Right: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return coloredButton(gtx, th, a.btns[id], avail[id].Name(), gtx.Constraints.Max.X, st.Body, st.Text)
							})
						}))
					}
					return layout.Flex{}.Layout(gtx, cells...)
				}))
			}
		}
		if !any {
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "todos os pedais já estão na cadeia", textOpts{size: 12, color: th.Dim})
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}
