package ui

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/presets"
)

// Itens do menu, na ordem em que aparecem (menuSepBefore põe uma linha divisória antes do item).
const (
	MenuAudio = iota
	MenuTuner
	MenuMetro
	MenuBackup
	MenuRestore
	MenuShortcuts
	MenuAbout
	menuItems
)

var menuLabels = [menuItems]string{"Áudio…", "Afinador", "Metrônomo", "Backup dos presets…", "Restaurar presets…", "Atalhos", "Sobre"}
var menuHints = [menuItems]string{MenuTuner: "A", MenuMetro: "M"}
var menuSepBefore = [menuItems]bool{MenuBackup: true, MenuShortcuts: true}

// Menu é o botão "Menu" da barra de topo e a lista que ele abre.
type Menu struct {
	open  bool
	btn   widget.Clickable
	items [menuItems]widget.Clickable
	scrim int
}

// Clicked devolve o item clicado (−1 = nenhum) e fecha a lista.
func (m *Menu) Clicked(gtx layout.Context) int {
	if m.btn.Clicked(gtx) {
		m.open = !m.open
	}
	for i := range m.items {
		if m.items[i].Clicked(gtx) {
			m.open = false
			return i
		}
	}
	return -1
}

// Button desenha o botão da barra de topo.
func (m *Menu) Button(gtx layout.Context, th *Theme) layout.Dimensions {
	bg := th.PanelHi
	if m.open {
		bg = darken(th.Gold, 0.35)
	}
	return coloredButton(gtx, th, &m.btn, "Menu", 0, bg, th.Text)
}

// Dropdown desenha a lista aberta com o canto superior direito em (largura − right, top).
// Clicar fora fecha.
func (m *Menu) Dropdown(gtx layout.Context, th *Theme, right, top int) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			m.open = false
		}
	}
	full := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.scrim)
	full.Pop()

	winW := gtx.Constraints.Max.X
	w := gtx.Dp(220)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	rec := op.Record(gtx.Ops)
	dims := layout.UniformInset(6).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		var ch []layout.FlexChild
		for i := range m.items {
			if menuSepBefore[i] {
				ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					y := gtx.Dp(4)
					fillRRect(gtx.Ops, image.Rect(gtx.Dp(6), y, gtx.Constraints.Max.X-gtx.Dp(6), y+1), 0, th.Line)
					return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(9))}
				}))
			}
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return m.item(gtx, th, i) }))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	call := rec.Stop()
	defer offset(gtx.Ops, winW-right-dims.Size.X, top).Pop()
	r := image.Rectangle{Max: dims.Size}
	fillRRect(gtx.Ops, r.Add(image.Pt(0, gtx.Dp(2))), gtx.Dp(8), withAlpha(rgb(0x000000), 120)) // sombra
	fillRRect(gtx.Ops, r, gtx.Dp(8), th.PanelHi)
	strokeRRect(gtx, r, gtx.Dp(8), th.Line)
	area := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &m.items) // absorve cliques entre os itens (não fecha)
	area.Pop()
	call.Add(gtx.Ops)
}

func (m *Menu) item(gtx layout.Context, th *Theme, i int) layout.Dimensions {
	c := &m.items[i]
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(30))
		if c.Hovered() {
			fillRRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(5), lighten(th.PanelHi, 0.1))
		}
		func() {
			defer offset(gtx.Ops, gtx.Dp(10), (sz.Y-gtx.Sp(17))/2).Pop()
			drawText(gtx, th, menuLabels[i], textOpts{size: 13, color: th.Text})
		}()
		if h := menuHints[i]; h != "" {
			defer offset(gtx.Ops, 0, (sz.Y-gtx.Sp(16))/2).Pop()
			drawText(gtx, th, "tecla "+h, textOpts{size: 11, color: th.Dim, width: sz.X - gtx.Dp(10), align: text.End})
		}
		return layout.Dimensions{Size: sz}
	})
}

// menuActions trata os cliques do menu.
func (a *App) menuActions(gtx layout.Context) {
	switch a.menu.Clicked(gtx) {
	case MenuAudio:
		a.Open(AudioID)
	case MenuTuner:
		a.open = TunerID
	case MenuMetro:
		a.open = MetroID
	case MenuBackup:
		a.backupAsync()
	case MenuRestore:
		a.restoreAsync()
	case MenuShortcuts:
		a.open = ShortcutsID
	case MenuAbout:
		a.open = AboutID
	}
}

// pedaleiraPath é o pedaleira.json ao lado da pasta de presets ("" sem pasta).
func pedaleiraPath(dir string) string { return presets.BoardPath(dir) }

// backupAsync pergunta onde salvar e grava o backup dos presets do usuário.
func (a *App) backupAsync() {
	dir := a.presetBar.dir
	if dir == "" {
		a.setStatus("sem pasta de configuração: não há presets do usuário para guardar", true)
		return
	}
	go func() {
		path, err := a.PickSaveZip("Backup dos presets", presets.BackupName(time.Now()))
		var res presets.BackupResult
		if err == nil && path != "" {
			res, err = presets.Backup(path, dir, pedaleiraPath(dir))
		}
		a.Post(func() {
			switch {
			case err != nil:
				a.setStatus("backup: "+err.Error(), true)
			case path != "":
				msg := fmt.Sprintf("backup salvo: %d preset(s)", res.Presets)
				if res.Files > 0 {
					msg += fmt.Sprintf(", %d arquivo(s) .nam/IR", res.Files)
				}
				if res.Pedaleira {
					msg += ", pedaleira"
				}
				msg += " → " + filepath.Base(path)
				if len(res.Missing) > 0 {
					msg += fmt.Sprintf(" (faltando: %s)", strings.Join(baseNames(res.Missing), ", "))
				}
				a.setStatus(msg, len(res.Missing) > 0)
			}
		})
	}()
}

// restoreAsync pergunta qual backup abrir e restaura (com backup automático do estado atual).
func (a *App) restoreAsync() {
	dir := a.presetBar.dir
	if dir == "" {
		a.setStatus("sem pasta de configuração: não dá para restaurar", true)
		return
	}
	go func() {
		path, err := a.PickZip("Restaurar presets de um backup")
		var res presets.RestoreResult
		if err == nil && path != "" {
			res, err = presets.Restore(path, dir, pedaleiraPath(dir))
		}
		a.Post(func() {
			switch {
			case err != nil:
				a.setStatus("restaurar: "+err.Error(), true)
			case path != "":
				a.presetBar.Reload()
				a.pedalboard = NewPedalboard(a.presetBar, pedaleiraPath(dir), a.setStatus, func(id string) { a.open = id })
				msg := fmt.Sprintf("restaurado: %d preset(s)", res.Presets)
				if res.Pedaleira {
					msg += " e a pedaleira"
				}
				if res.Safety != "" {
					msg += "  ·  o estado anterior ficou em " + res.Safety
				}
				a.setStatus(msg, false)
			}
		})
	}()
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}
