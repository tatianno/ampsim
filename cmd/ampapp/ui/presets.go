package ui

import (
	"fmt"
	"image"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
)

// IDs das sobreposições de presets.
const (
	PresetListID = "__presets__"
	SaveAsID     = "__salvar__"
)

// PresetBar é a linha de presets: navegar, ver o atual (com * se modificado), salvar.
type PresetBar struct {
	chain  *engine.Chain
	dir    string
	status func(msg string, isErr bool)
	open   func(id string)

	list    []presets.Preset
	cur     int          // índice em list (-1 = nenhum)
	loaded  engine.State // estado no momento em que o preset foi carregado/salvo
	dirty   bool
	checked time.Time

	prev, next, name, save, saveAs, del widget.Clickable
	confirmDel                          time.Time // 1º clique em Excluir arma; 2º (em até 3 s) apaga

	// lista e "salvar como"
	rows   []widget.Clickable
	editor widget.Editor
	ok     widget.Clickable
	cancel widget.Clickable
	focus  bool
}

// NewPresetBar cria a barra de presets (dir = pasta dos presets do usuário).
func NewPresetBar(chain *engine.Chain, dir string, status func(string, bool), open func(string)) *PresetBar {
	p := &PresetBar{chain: chain, dir: dir, status: status, open: open, cur: -1}
	p.editor.SingleLine = true
	p.editor.Submit = true
	p.Reload()
	return p
}

// Reload relê a lista (fábrica + usuário).
func (p *PresetBar) Reload() {
	var curName string
	if p.cur >= 0 && p.cur < len(p.list) {
		curName = p.list[p.cur].Name
	}
	list, err := presets.List(p.dir)
	if err != nil && p.status != nil {
		p.status("presets: "+err.Error(), true)
	}
	p.list = list
	p.rows = make([]widget.Clickable, len(list))
	p.cur = -1
	for i, q := range list {
		if q.Name == curName {
			p.cur = i
		}
	}
}

// Current devolve o preset atual (ok = false se nenhum).
func (p *PresetBar) Current() (presets.Preset, bool) {
	if p.cur < 0 || p.cur >= len(p.list) {
		return presets.Preset{}, false
	}
	return p.list[p.cur], true
}

// Dirty informa se o som foi mexido depois de carregar/salvar o preset.
func (p *PresetBar) Dirty() bool { return p.dirty }

// Load aplica o preset i.
func (p *PresetBar) Load(i int) {
	if i < 0 || i >= len(p.list) {
		return
	}
	q := p.list[i]
	if err := p.chain.ApplyState(q.State); err != nil {
		p.status(fmt.Sprintf("preset %s: %v", q.Name, err), true)
	}
	p.cur = i
	p.loaded = p.chain.State()
	p.dirty = false
}

// SaveAs grava o som atual com o nome dado.
func (p *PresetBar) SaveAs(name string) error {
	q, err := presets.Save(p.dir, name, p.chain.State())
	if err != nil {
		return err
	}
	p.Reload()
	for i, r := range p.list {
		if r.Path == q.Path {
			p.cur = i
		}
	}
	p.loaded = p.chain.State()
	p.dirty = false
	p.status("preset salvo: "+q.Name, false)
	return nil
}

func (p *PresetBar) update(gtx layout.Context) {
	n := len(p.list)
	if p.prev.Clicked(gtx) && n > 0 {
		p.Load((max(p.cur, 0) - 1 + n) % n)
	}
	if p.next.Clicked(gtx) && n > 0 {
		p.Load((p.cur + 1) % n)
	}
	if p.name.Clicked(gtx) {
		p.open(PresetListID)
	}
	cur, has := p.Current()
	if p.save.Clicked(gtx) {
		if has && !cur.Factory {
			if err := p.SaveAs(cur.Name); err != nil {
				p.status(err.Error(), true)
			}
		} else {
			p.startSaveAs("") // de fábrica (ou nenhum): salva como novo
		}
	}
	if p.saveAs.Clicked(gtx) {
		name := ""
		if has && !cur.Factory {
			name = cur.Name
		}
		p.startSaveAs(name)
	}
	if p.del.Clicked(gtx) && has && !cur.Factory {
		if gtx.Now.Before(p.confirmDel) {
			if err := presets.Delete(cur); err != nil {
				p.status(err.Error(), true)
			} else {
				p.status("preset excluído: "+cur.Name, false)
				p.cur = -1
				p.Reload()
			}
			p.confirmDel = time.Time{}
		} else {
			p.confirmDel = gtx.Now.Add(3 * time.Second)
		}
	}
	// "modificado": compara com o estado carregado (no máximo 4 vezes por segundo)
	if has && gtx.Now.Sub(p.checked) > 250*time.Millisecond {
		p.checked = gtx.Now
		p.dirty = !presets.Equal(p.chain.State(), p.loaded)
	}
}

func (p *PresetBar) startSaveAs(name string) {
	p.editor.SetText(name)
	p.focus = true
	p.open(SaveAsID)
}

// Layout desenha a linha de presets.
func (p *PresetBar) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	p.update(gtx)
	cur, has := p.Current()
	label := "— nenhum preset —"
	if has {
		label = cur.Name
		if cur.Factory {
			label += "  (fábrica)"
		}
		if p.dirty {
			label += "  *"
		}
	}
	delLabel := "Excluir"
	if gtx.Now.Before(p.confirmDel) {
		delLabel = "Confirmar exclusão"
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "PRESET", textOpts{size: 11, color: th.Dim, bold: true})
		}),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arrowButton(gtx, th, &p.prev, -1) }),
		layout.Rigid(spacer(4, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return coloredButton(gtx, th, &p.name, label, gtx.Dp(240), th.PanelHi, th.Text)
		}),
		layout.Rigid(spacer(4, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arrowButton(gtx, th, &p.next, +1) }),
		layout.Rigid(spacer(12, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &p.save, "Salvar", 0) }),
		layout.Rigid(spacer(6, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return smallButton(gtx, th, &p.saveAs, "Salvar como…", 0)
		}),
	}
	if has && !cur.Factory {
		children = append(children, layout.Rigid(spacer(6, 0)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			bg := th.PanelHi
			if gtx.Now.Before(p.confirmDel) {
				bg = darken(th.Clip, 0.3)
			}
			return coloredButton(gtx, th, &p.del, delLabel, 0, bg, th.Text)
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// ListLayout desenha a lista de presets (sobreposição).
func (p *PresetBar) ListLayout(gtx layout.Context, th *Theme, close func()) layout.Dimensions {
	for i := range p.rows {
		if p.rows[i].Clicked(gtx) {
			p.Load(i)
			close()
		}
	}
	w := gtx.Dp(380)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return panel(gtx, th.Panel, 14, 16, func(gtx layout.Context) layout.Dimensions {
		var ch []layout.FlexChild
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "PRESETS", textOpts{size: 18, color: th.Text, bold: true})
		}))
		section := ""
		for i, q := range p.list {
			s := "FÁBRICA"
			if !q.Factory {
				s = "MEUS PRESETS"
			}
			if s != section {
				section = s
				ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, s, textOpts{size: 11, color: th.Gold, bold: true})
					})
				}))
			}
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return choice(gtx, th, &p.rows[i], q.Name, i == p.cur, gtx.Constraints.Max.X)
				})
			}))
		}
		if !p.hasUser() {
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "nenhum preset seu ainda: use Salvar como…", textOpts{size: 11, color: th.Dim})
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}

func (p *PresetBar) hasUser() bool {
	for _, q := range p.list {
		if !q.Factory {
			return true
		}
	}
	return false
}

// SaveAsLayout desenha o "salvar como" (sobreposição): nome + Salvar/Cancelar.
func (p *PresetBar) SaveAsLayout(gtx layout.Context, th *Theme, close func()) layout.Dimensions {
	submit := false
	for {
		e, ok := p.editor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := e.(widget.SubmitEvent); ok {
			submit = true
		}
	}
	if p.ok.Clicked(gtx) {
		submit = true
	}
	if p.cancel.Clicked(gtx) {
		close()
	}
	if submit {
		if err := p.SaveAs(p.editor.Text()); err != nil {
			p.status(err.Error(), true)
		} else {
			close()
		}
	}
	if p.focus {
		gtx.Execute(key.FocusCmd{Tag: &p.editor})
		p.focus = false
	}
	w := gtx.Dp(420)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return panel(gtx, th.Panel, 14, 18, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "SALVAR PRESET", textOpts{size: 18, color: th.Text, bold: true})
			}),
			layout.Rigid(spacer(0, 10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th.Bg, 6, 8, func(gtx layout.Context) layout.Dimensions {
					e := material.Editor(th.M, &p.editor, "nome do preset")
					e.Color = th.Text
					e.HintColor = th.Dim
					return e.Layout(gtx)
				})
			}),
			layout.Rigid(spacer(0, 4)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "salva o som atual (pedais, ordem, knobs e IR); mesmo nome substitui",
					textOpts{size: 11, color: th.Dim, align: text.Start})
			}),
			layout.Rigid(spacer(0, 12)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return coloredButton(gtx, th, &p.ok, "Salvar", 0, darken(th.Good, 0.45), th.Text)
					}),
					layout.Rigid(spacer(8, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &p.cancel, "Cancelar", 0) }),
				)
			}),
		)
	})
}
