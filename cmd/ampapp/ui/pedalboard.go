package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/widget"
)

// BoardSlots é o número de footswitches por banco (como num controlador MIDI de 6 botões).
const BoardSlots = 6

// boardMinBanks é o número mínimo de bancos (18 footswitches).
const boardMinBanks = 3

// PedalboardPickID é a sobreposição que escolhe o preset de um footswitch.
const PedalboardPickID = "__pedaleira__"

// boardFile é o arquivo da configuração da pedaleira (na pasta de configuração do app).
type boardFile struct {
	Slots []string `json:"slots"` // nome do preset de cada footswitch ("" = vazio), banco a banco
}

// Pedalboard é a pedaleira de presets: footswitches de acesso rápido, em bancos de 6, com o
// LED aceso no preset atual. As teclas 1 a 6 acionam os footswitches.
//
// Por padrão os footswitches seguem a ordem da lista de presets. No modo de edição ("Editar"),
// clicar num footswitch escolhe o preset dele; "Restaurar padrão" volta à ordem da lista. A
// configuração fica em pedaleira.json (os footswitches guardam o nome do preset).
type Pedalboard struct {
	pb      *PresetBar
	path    string   // pedaleira.json ("" = não salva)
	slots   []string // nil = ordem padrão (a da lista de presets)
	status  func(string, bool)
	open    func(id string)
	bank    int
	lastCur int

	sw                      [BoardSlots]widget.Clickable
	prev, next, edit, reset widget.Clickable
	editing                 bool
	confirmReset            time.Time

	// escolha do preset de um footswitch (sobreposição)
	pick     int // footswitch sendo configurado (índice global)
	rows     []widget.Clickable
	empty    widget.Clickable
	pickDone func()
}

// NewPedalboard cria a pedaleira sobre a lista da barra de presets; path é o arquivo da
// configuração ("" = não salva). Lê a configuração salva, se houver.
func NewPedalboard(pb *PresetBar, path string, status func(string, bool), open func(string)) *Pedalboard {
	b := &Pedalboard{pb: pb, path: path, status: status, open: open, lastCur: -1, pick: -1}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var f boardFile
			if json.Unmarshal(data, &f) == nil && len(f.Slots) > 0 {
				b.slots = f.Slots
			} else if status != nil {
				status("pedaleira.json inválido: usando a ordem padrão", true)
			}
		}
	}
	return b
}

// Custom informa se a pedaleira tem uma configuração própria (não a ordem padrão).
func (b *Pedalboard) Custom() bool { return b.slots != nil }

func (b *Pedalboard) numSlots() int {
	n := max(len(b.pb.list), len(b.slots), boardMinBanks*BoardSlots)
	return (n + BoardSlots - 1) / BoardSlots * BoardSlots
}

func (b *Pedalboard) banks() int { return b.numSlots() / BoardSlots }

// slotName devolve o nome do preset no footswitch k (índice global; "" = vazio).
func (b *Pedalboard) slotName(k int) string {
	if b.slots == nil {
		if k < len(b.pb.list) {
			return b.pb.list[k].Name
		}
		return ""
	}
	if k < len(b.slots) {
		return b.slots[k]
	}
	return ""
}

// slotPreset devolve o índice (na lista) do preset do footswitch k, ou −1 (vazio ou apagado).
func (b *Pedalboard) slotPreset(k int) int {
	name := b.slotName(k)
	if name == "" {
		return -1
	}
	for i, p := range b.pb.list {
		if strings.EqualFold(p.Name, name) {
			return i
		}
	}
	return -1
}

// Press aciona o footswitch i (0..5) do banco atual (no modo de edição, abre a escolha).
func (b *Pedalboard) Press(i int) {
	if i < 0 || i >= BoardSlots {
		return
	}
	k := b.bank*BoardSlots + i
	if b.editing {
		b.startPick(k)
		return
	}
	if idx := b.slotPreset(k); idx >= 0 {
		b.pb.Load(idx)
	}
}

// SetEditing liga ou desliga o modo de edição (clicar num footswitch escolhe o preset dele).
func (b *Pedalboard) SetEditing(on bool) { b.editing = on }

// Bank devolve o banco mostrado (0, 1, ...).
func (b *Pedalboard) Bank() int { return b.bank }

// SetBank muda o banco mostrado (dá a volta nas pontas).
func (b *Pedalboard) SetBank(n int) { b.bank = (n%b.banks() + b.banks()) % b.banks() }

// Assign põe o preset name no footswitch k (índice global; "" = vazio) e salva.
func (b *Pedalboard) Assign(k int, name string) error {
	if b.slots == nil { // a primeira mudança materializa a ordem padrão
		def := make([]string, b.numSlots())
		for i := range def {
			def[i] = b.slotName(i)
		}
		b.slots = def
	}
	for len(b.slots) <= k {
		b.slots = append(b.slots, "")
	}
	b.slots[k] = name
	return b.save()
}

// ResetConfig volta à ordem padrão e apaga a configuração salva.
func (b *Pedalboard) ResetConfig() error {
	b.slots = nil
	if b.path == "" {
		return nil
	}
	if err := os.Remove(b.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (b *Pedalboard) save() error {
	if b.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(boardFile{Slots: b.slots}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(b.path, append(data, '\n'), 0o644)
}

func (b *Pedalboard) startPick(k int) {
	b.pick = k
	if b.open != nil {
		b.open(PedalboardPickID)
	}
}

func (b *Pedalboard) report(err error, ok string) {
	if b.status == nil {
		return
	}
	if err != nil {
		b.status("pedaleira: "+err.Error(), true)
	} else if ok != "" {
		b.status(ok, false)
	}
}

// Layout desenha a pedaleira.
func (b *Pedalboard) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	// o banco acompanha o preset atual quando ele muda por outro caminho
	if cur := b.pb.cur; cur != b.lastCur {
		b.lastCur = cur
		for k := range b.numSlots() {
			if cur >= 0 && b.slotPreset(k) == cur {
				b.bank = k / BoardSlots
				break
			}
		}
	}
	b.bank = min(b.bank, b.banks()-1)
	if b.prev.Clicked(gtx) {
		b.SetBank(b.bank - 1)
	}
	if b.next.Clicked(gtx) {
		b.SetBank(b.bank + 1)
	}
	if b.edit.Clicked(gtx) {
		b.editing = !b.editing
		b.confirmReset = time.Time{}
	}
	if b.reset.Clicked(gtx) {
		if gtx.Now.Before(b.confirmReset) {
			b.report(b.ResetConfig(), "pedaleira na ordem padrão")
			b.confirmReset = time.Time{}
		} else {
			b.confirmReset = gtx.Now.Add(3 * time.Second)
		}
	}
	for i := range b.sw {
		if b.sw[i].Clicked(gtx) {
			b.Press(i)
		}
	}
	editLabel := "Editar"
	if b.editing {
		editLabel = "Concluir"
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
					hint := "teclas 1 a 6"
					if b.editing {
						hint = "clique num footswitch para escolher o preset"
					}
					return drawText(gtx, th, hint, textOpts{size: 10, color: withAlpha(th.Dim, 180)})
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
		layout.Rigid(spacer(12, 0)),
		// Editar / Concluir e Restaurar padrão, à direita
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					bg, fg := th.PanelHi, th.Text
					if b.editing {
						bg = darken(th.Gold, 0.35)
					}
					return coloredButton(gtx, th, &b.edit, editLabel, 0, bg, fg)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !b.editing {
						return layout.Dimensions{}
					}
					label := "Restaurar padrão"
					if gtx.Now.Before(b.confirmReset) {
						label = "Confirmar?"
						gtx.Execute(op.InvalidateCmd{At: b.confirmReset}) // volta a "Restaurar padrão" sozinho
					}
					return layout.Inset{Top: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return smallButton(gtx, th, &b.reset, label, 0)
					})
				}),
			)
		}),
	)
}

// slot desenha um footswitch: LED, botão, número e o nome do preset.
func (b *Pedalboard) slot(gtx layout.Context, th *Theme, i int) layout.Dimensions {
	k := b.bank*BoardSlots + i
	name, idx := b.slotName(k), b.slotPreset(k)
	on := idx >= 0 && idx == b.pb.cur
	label := name
	switch {
	case name == "":
		label = "—"
	case idx < 0:
		label = name + " (apagado)"
	}
	if b.editing {
		label += " ▾"
	}
	usable := idx >= 0 || b.editing
	w := gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(w, gtx.Dp(10))
			led(gtx.Ops, f32.Pt(float32(w)/2, float32(sz.Y)/2), float32(gtx.Dp(4)), on, th.LedOn, th.LedOff)
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(spacer(0, 1)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !usable {
				d := gtx.Dp(32)
				fillCircle(gtx.Ops, f32.Pt(float32(d)/2, float32(d)/2), float32(d)/2, withAlpha(rgb(0x000000), 60))
				return layout.Dimensions{Size: image.Pt(d, d)}
			}
			return footswitch(gtx, &b.sw[i], 32)
		}),
		layout.Rigid(spacer(0, 3)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			c := th.Text
			switch {
			case b.editing:
				c = th.Gold
			case on:
				c = th.Gold
			case idx < 0:
				c = th.Dim
			}
			return drawText(gtx, th, fmt.Sprintf("%d · %s", i+1, label), textOpts{size: 11, color: c, bold: on, width: w, align: text.Middle})
		}),
	)
}

// PickLayout desenha a escolha do preset de um footswitch (sobreposição).
func (b *Pedalboard) PickLayout(gtx layout.Context, th *Theme, close func()) layout.Dimensions {
	if b.pick < 0 {
		close()
		return layout.Dimensions{}
	}
	if len(b.rows) != len(b.pb.list) {
		b.rows = make([]widget.Clickable, len(b.pb.list))
	}
	for i := range b.rows {
		if b.rows[i].Clicked(gtx) && b.pick >= 0 {
			b.report(b.Assign(b.pick, b.pb.list[i].Name), "")
			close()
		}
	}
	if b.empty.Clicked(gtx) && b.pick >= 0 {
		b.report(b.Assign(b.pick, ""), "")
		close()
	}
	cur := b.slotName(b.pick)
	w := gtx.Dp(400)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return panel(gtx, th.Panel, 14, 16, func(gtx layout.Context) layout.Dimensions {
		title := fmt.Sprintf("FOOTSWITCH %d · BANCO %d", b.pick%BoardSlots+1, b.pick/BoardSlots+1)
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, title, textOpts{size: 18, color: th.Text, bold: true})
			}),
			layout.Rigid(spacer(0, 10)),
		}
		section := ""
		for i, p := range b.pb.list {
			s := "FÁBRICA"
			if !p.Factory {
				s = "MEUS PRESETS"
			}
			if s != section {
				section = s
				ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, s, textOpts{size: 11, color: th.Gold, bold: true})
					})
				}))
			}
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return choice(gtx, th, &b.rows[i], p.Name, strings.EqualFold(p.Name, cur), gtx.Constraints.Max.X)
				})
			}))
		}
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return choice(gtx, th, &b.empty, "Vazio (sem preset)", cur == "", gtx.Constraints.Max.X)
			})
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}
