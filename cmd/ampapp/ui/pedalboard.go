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
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/tatianno/ampsim/presets"
)

// BoardSlots é o número de footswitches por banco (como num controlador MIDI de 6 botões).
const BoardSlots = 6

// Limites dos bancos: mínimo de 3 (18 footswitches), máximo de 10; nome com até 18 letras.
const (
	boardMinBanks = 3
	boardMaxBanks = 10
	boardNameMax  = 18
)

// PedalboardEditID é a sobreposição que edita a pedaleira (a matriz de bancos e footswitches).
const PedalboardEditID = "__pedaleira__"

// boardFile é o arquivo da configuração da pedaleira (na pasta de configuração do app).
type boardFile struct {
	Slots []string `json:"slots"`           // nome do preset de cada footswitch ("" = vazio), banco a banco
	Banks []string `json:"banks,omitempty"` // nome de cada banco ("" = sem nome)
}

// Pedalboard é a pedaleira de presets: footswitches de acesso rápido, em bancos de 6, com o
// LED aceso no preset atual. As teclas 1 a 6 acionam os footswitches.
//
// Por padrão os footswitches seguem a ordem da lista de presets e os bancos de fábrica têm os
// nomes dos temas (presets.FactoryBanks). "Editar" abre a matriz: um banco por linha, com o
// nome editável e os 6 footswitches; clicar numa célula escolhe o preset. A primeira mudança
// copia o padrão para a configuração (pedaleira.json), que daí em diante é toda do usuário.
type Pedalboard struct {
	pb      *PresetBar
	path    string   // pedaleira.json ("" = não salva)
	slots   []string // nil = ordem padrão (a da lista de presets)
	names   []string // nil = nomes padrão (os temas de fábrica)
	status  func(string, bool)
	open    func(id string)
	bank    int
	lastCur int

	sw               [BoardSlots]widget.Clickable
	prev, next, edit widget.Clickable

	// matriz de edição
	cells        []widget.Clickable
	nameEds      []widget.Editor
	addBank      widget.Clickable
	delBank      widget.Clickable
	reset        widget.Clickable
	confirmReset time.Time

	// escolha do preset de uma célula (dentro da mesma sobreposição)
	pick  int // footswitch sendo configurado (índice global); −1 = mostrando a matriz
	rows  []widget.Clickable
	empty widget.Clickable
	back  widget.Clickable
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
				b.names = f.Banks
				if b.names == nil {
					b.names = []string{} // configuração antiga, sem nomes: bancos sem nome
				}
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
	n := max(len(b.slots), len(b.names)*BoardSlots, boardMinBanks*BoardSlots)
	if b.slots == nil {
		n = max(n, len(b.pb.list))
	}
	return (n + BoardSlots - 1) / BoardSlots * BoardSlots
}

func (b *Pedalboard) banks() int { return b.numSlots() / BoardSlots }

// defaultBankName é o nome padrão do banco n: o tema de fábrica, nos bancos de fábrica.
func defaultBankName(n int) string {
	if BoardSlots == presets.FactoryBankSize && n >= 0 && n < len(presets.FactoryBanks) {
		return presets.FactoryBanks[n]
	}
	return ""
}

// BankName devolve o nome do banco n ("" = sem nome).
func (b *Pedalboard) BankName(n int) string {
	if b.names == nil {
		return defaultBankName(n)
	}
	if n >= 0 && n < len(b.names) {
		return b.names[n]
	}
	return ""
}

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

// Press aciona o footswitch i (0..5) do banco atual.
func (b *Pedalboard) Press(i int) {
	if i < 0 || i >= BoardSlots {
		return
	}
	if idx := b.slotPreset(b.bank*BoardSlots + i); idx >= 0 {
		b.pb.Load(idx)
	}
}

// Bank devolve o banco mostrado (0, 1, ...).
func (b *Pedalboard) Bank() int { return b.bank }

// SetBank muda o banco mostrado (dá a volta nas pontas).
func (b *Pedalboard) SetBank(n int) { b.bank = (n%b.banks() + b.banks()) % b.banks() }

// materialize copia o padrão (ordem e nomes) para a configuração, antes da primeira mudança.
func (b *Pedalboard) materialize() {
	n := b.numSlots()
	if b.slots == nil {
		def := make([]string, n)
		for i := range def {
			def[i] = b.slotName(i)
		}
		b.slots = def
	}
	if b.names == nil {
		def := make([]string, n/BoardSlots)
		for i := range def {
			def[i] = defaultBankName(i)
		}
		b.names = def
	}
}

// Assign põe o preset name no footswitch k (índice global; "" = vazio) e salva.
func (b *Pedalboard) Assign(k int, name string) error {
	b.materialize()
	for len(b.slots) <= k {
		b.slots = append(b.slots, "")
	}
	b.slots[k] = name
	return b.save()
}

// SetBankName dá nome ao banco n (até boardNameMax letras; "" = sem nome) e salva.
func (b *Pedalboard) SetBankName(n int, name string) error {
	if n < 0 || n >= b.banks() {
		return fmt.Errorf("banco %d não existe", n+1)
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > boardNameMax {
		name = string([]rune(name)[:boardNameMax])
	}
	if name == b.BankName(n) {
		return nil
	}
	b.materialize()
	for len(b.names) <= n {
		b.names = append(b.names, "")
	}
	b.names[n] = name
	return b.save()
}

// AddBank acrescenta um banco vazio no fim (até boardMaxBanks) e salva.
func (b *Pedalboard) AddBank() error {
	if b.banks() >= boardMaxBanks {
		return fmt.Errorf("no máximo %d bancos", boardMaxBanks)
	}
	b.materialize()
	n := b.banks() + 1
	for len(b.slots) < n*BoardSlots {
		b.slots = append(b.slots, "")
	}
	for len(b.names) < n {
		b.names = append(b.names, "")
	}
	return b.save()
}

// lastBankEmpty diz se o último banco não tem preset em nenhum footswitch.
func (b *Pedalboard) lastBankEmpty() bool {
	last := b.banks() - 1
	for i := range BoardSlots {
		if b.slotName(last*BoardSlots+i) != "" {
			return false
		}
	}
	return true
}

// CanRemoveBank diz se o último banco pode ser removido (vazio e acima do mínimo).
func (b *Pedalboard) CanRemoveBank() bool { return b.banks() > boardMinBanks && b.lastBankEmpty() }

// RemoveLastBank remove o último banco, se estiver vazio, e salva.
func (b *Pedalboard) RemoveLastBank() error {
	if !b.CanRemoveBank() {
		return errors.New("só dá para remover o último banco, vazio, acima de 3")
	}
	b.materialize()
	n := b.banks() - 1
	b.slots = b.slots[:min(len(b.slots), n*BoardSlots)]
	b.names = b.names[:min(len(b.names), n)]
	b.bank = min(b.bank, n-1)
	return b.save()
}

// ResetConfig volta à ordem e aos nomes padrão e apaga a configuração salva.
func (b *Pedalboard) ResetConfig() error {
	b.slots, b.names = nil, nil
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
	data, err := json.MarshalIndent(boardFile{Slots: b.slots, Banks: b.names}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(b.path, append(data, '\n'), 0o644)
}

// OpenEditor abre a matriz de edição.
func (b *Pedalboard) OpenEditor() {
	b.pick = -1
	b.confirmReset = time.Time{}
	if b.open != nil {
		b.open(PedalboardEditID)
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

// bankLabel é o que aparece entre ◀ e ▶: o nome do banco ou "BANCO n/N".
func (b *Pedalboard) bankLabel() string {
	if name := b.BankName(b.bank); name != "" {
		return strings.ToUpper(name)
	}
	return fmt.Sprintf("BANCO %d/%d", b.bank+1, b.banks())
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
		b.OpenEditor()
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
							return drawText(gtx, th, b.bankLabel(),
								textOpts{size: 11, color: th.Gold, bold: true, width: gtx.Dp(136), align: text.Middle})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &b.next, "▶", 0) }),
					)
				}),
				layout.Rigid(spacer(0, 4)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					hint := "teclas 1 a 6"
					if b.BankName(b.bank) != "" { // o nome tomou o lugar do número: a posição vem aqui
						hint = fmt.Sprintf("banco %d de %d  ·  teclas 1 a 6", b.bank+1, b.banks())
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
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &b.edit, "Editar", 0) }),
	)
}

// slotLabel é o nome mostrado no footswitch k ("—" vazio; "(apagado)" se o preset sumiu).
func (b *Pedalboard) slotLabel(k int) string {
	name, idx := b.slotName(k), b.slotPreset(k)
	switch {
	case name == "":
		return "—"
	case idx < 0:
		return name + " (apagado)"
	}
	return name
}

// slot desenha um footswitch: LED, botão, número e o nome do preset.
func (b *Pedalboard) slot(gtx layout.Context, th *Theme, i int) layout.Dimensions {
	k := b.bank*BoardSlots + i
	idx := b.slotPreset(k)
	on := idx >= 0 && idx == b.pb.cur
	w := gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(w, gtx.Dp(10))
			led(gtx.Ops, f32.Pt(float32(w)/2, float32(sz.Y)/2), float32(gtx.Dp(4)), on, th.LedOn, th.LedOff)
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(spacer(0, 1)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if idx < 0 {
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
			case on:
				c = th.Gold
			case idx < 0:
				c = th.Dim
			}
			return drawText(gtx, th, fmt.Sprintf("%d · %s", i+1, b.slotLabel(k)), textOpts{size: 11, color: c, bold: on, width: w, align: text.Middle})
		}),
	)
}

// Typing diz se um nome de banco está sendo digitado (o app desliga os atalhos de teclado).
func (b *Pedalboard) Typing(gtx layout.Context) bool {
	for i := range b.nameEds {
		if gtx.Focused(&b.nameEds[i]) {
			return true
		}
	}
	return false
}

// EditLayout desenha a sobreposição de edição: a matriz ou, depois de clicar numa célula, a
// escolha do preset dela.
func (b *Pedalboard) EditLayout(gtx layout.Context, th *Theme) layout.Dimensions {
	if b.pick >= 0 {
		return b.pickLayout(gtx, th)
	}
	return b.matrixLayout(gtx, th)
}

// EditorWidthDp é a largura máxima da matriz de edição.
const EditorWidthDp = 940

func (b *Pedalboard) matrixLayout(gtx layout.Context, th *Theme) layout.Dimensions {
	nb := b.banks()
	if len(b.cells) < nb*BoardSlots {
		b.cells = append(b.cells, make([]widget.Clickable, nb*BoardSlots-len(b.cells))...)
	}
	for len(b.nameEds) < nb {
		b.nameEds = append(b.nameEds, widget.Editor{SingleLine: true, Submit: true, MaxLen: boardNameMax})
	}
	// eventos: nomes, células e botões
	for n := range nb {
		ed := &b.nameEds[n]
		for {
			e, ok := ed.Update(gtx)
			if !ok {
				break
			}
			switch e.(type) {
			case widget.ChangeEvent:
				b.report(b.SetBankName(n, ed.Text()), "")
			case widget.SubmitEvent:
				gtx.Execute(key.FocusCmd{}) // Enter conclui o nome
			}
		}
		if !gtx.Focused(ed) && ed.Text() != b.BankName(n) {
			ed.SetText(b.BankName(n)) // acompanha restaurar padrão, bancos novos etc.
		}
	}
	for k := range nb * BoardSlots {
		if b.cells[k].Clicked(gtx) {
			b.pick = k
		}
	}
	if b.addBank.Clicked(gtx) {
		b.report(b.AddBank(), "")
	}
	if b.delBank.Clicked(gtx) {
		b.report(b.RemoveLastBank(), "")
	}
	if b.reset.Clicked(gtx) {
		if gtx.Now.Before(b.confirmReset) {
			b.report(b.ResetConfig(), "pedaleira na ordem e com os nomes padrão")
			b.confirmReset = time.Time{}
		} else {
			b.confirmReset = gtx.Now.Add(3 * time.Second)
		}
	}
	nb = b.banks() // pode ter mudado
	if len(b.cells) < nb*BoardSlots {
		b.cells = append(b.cells, make([]widget.Clickable, nb*BoardSlots-len(b.cells))...)
	}
	for len(b.nameEds) < nb {
		b.nameEds = append(b.nameEds, widget.Editor{SingleLine: true, Submit: true, MaxLen: boardNameMax})
	}

	w := min(gtx.Dp(EditorWidthDp), gtx.Constraints.Max.X-gtx.Dp(40))
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	row := func(n int) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				ch := []layout.FlexChild{
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(170), gtx.Dp(28)))
						return panel(gtx, th.Bg, 5, 5, func(gtx layout.Context) layout.Dimensions {
							e := material.Editor(th.M, &b.nameEds[n], fmt.Sprintf("Banco %d (sem nome)", n+1))
							e.Color, e.HintColor, e.TextSize = th.Gold, withAlpha(th.Dim, 150), 13
							return e.Layout(gtx)
						})
					}),
					layout.Rigid(spacer(8, 0)),
				}
				for i := range BoardSlots {
					k := n*BoardSlots + i
					ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: 2, Right: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							idx := b.slotPreset(k)
							bg, fg := th.PanelHi, th.Text
							switch {
							case idx >= 0 && idx == b.pb.cur:
								bg = darken(th.Gold, 0.35)
							case idx < 0:
								fg = th.Dim
							}
							return coloredButton(gtx, th, &b.cells[k], b.slotLabel(k), gtx.Constraints.Max.X, bg, fg)
						})
					}))
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
			})
		})
	}
	return panel(gtx, th.Panel, 14, 16, func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "EDITAR PEDALEIRA", textOpts{size: 18, color: th.Text, bold: true})
			}),
			layout.Rigid(spacer(0, 2)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "clique numa célula para escolher o preset  ·  o nome do banco aparece na pedaleira (vazio: \"BANCO n\")",
					textOpts{size: 11, color: th.Dim})
			}),
			layout.Rigid(spacer(0, 12)),
			// cabeçalho das colunas: os footswitches (e as teclas) 1 a 6
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				hd := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "BANCO", textOpts{size: 11, color: th.Dim, bold: true, width: gtx.Dp(178)})
				})}
				for i := range BoardSlots {
					hd = append(hd, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, fmt.Sprintf("FOOTSWITCH %d", i+1), textOpts{size: 11, color: th.Dim, bold: true,
							width: gtx.Constraints.Max.X, align: text.Middle})
					}))
				}
				return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx, hd...)
				})
			}),
		}
		for n := range nb {
			ch = append(ch, row(n))
		}
		ch = append(ch, layout.Rigid(spacer(0, 6)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := "Restaurar padrão"
			if gtx.Now.Before(b.confirmReset) {
				label = "Confirmar?"
				gtx.Execute(op.InvalidateCmd{At: b.confirmReset}) // volta a "Restaurar padrão" sozinho
			}
			fgDel := th.Text
			if !b.CanRemoveBank() {
				fgDel = withAlpha(th.Text, 80)
			}
			fgAdd := th.Text
			if nb >= boardMaxBanks {
				fgAdd = withAlpha(th.Text, 80)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return coloredButton(gtx, th, &b.addBank, "+ Banco", 0, th.PanelHi, fgAdd)
				}),
				layout.Rigid(spacer(6, 0)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return coloredButton(gtx, th, &b.delBank, "− Último banco", 0, th.PanelHi, fgDel)
				}),
				layout.Flexed(1, hfill),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &b.reset, label, 0) }),
			)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}

// pickLayout escolhe o preset da célula b.pick: os de fábrica em colunas por banco temático,
// os do usuário embaixo, e "Vazio". "Voltar" retorna à matriz.
func (b *Pedalboard) pickLayout(gtx layout.Context, th *Theme) layout.Dimensions {
	if len(b.rows) != len(b.pb.list) {
		b.rows = make([]widget.Clickable, len(b.pb.list))
	}
	for i := range b.rows {
		if b.rows[i].Clicked(gtx) {
			b.report(b.Assign(b.pick, b.pb.list[i].Name), "")
			b.pick = -1
		}
	}
	if b.empty.Clicked(gtx) {
		b.report(b.Assign(b.pick, ""), "")
		b.pick = -1
	}
	if b.back.Clicked(gtx) {
		b.pick = -1
	}
	if b.pick < 0 {
		return b.matrixLayout(gtx, th)
	}
	cur := b.slotName(b.pick)
	var factory, user []int
	for i, p := range b.pb.list {
		if p.Factory {
			factory = append(factory, i)
		} else {
			user = append(user, i)
		}
	}
	const cols = 3
	w := min(gtx.Dp(720), gtx.Constraints.Max.X-gtx.Dp(40))
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	item := func(i int) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return choice(gtx, th, &b.rows[i], b.pb.list[i].Name, strings.EqualFold(b.pb.list[i].Name, cur), gtx.Constraints.Max.X)
			})
		})
	}
	heading := func(s string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, s, textOpts{size: 11, color: th.Gold, bold: true})
			})
		})
	}
	// grade de colunas: cada coluna é uma lista vertical
	grid := func(columns [][]layout.FlexChild) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			var ch []layout.FlexChild
			for c, col := range columns {
				if c > 0 {
					ch = append(ch, layout.Rigid(spacer(8, 0)))
				}
				ch = append(ch, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, col...)
				}))
			}
			return layout.Flex{Alignment: layout.Start}.Layout(gtx, ch...)
		})
	}
	// fábrica: uma coluna por banco temático (se a lista bater com os temas), senão 3 colunas
	fcols := make([][]layout.FlexChild, cols)
	perCol := (len(factory) + cols - 1) / cols
	themed := len(factory) == presets.FactoryBankSize*len(presets.FactoryBanks) && len(presets.FactoryBanks) == cols
	for j, i := range factory {
		c := min(j/max(1, perCol), cols-1)
		if themed {
			c = j / presets.FactoryBankSize
			if j%presets.FactoryBankSize == 0 {
				fcols[c] = append(fcols[c], heading(strings.ToUpper(presets.FactoryBanks[c])))
			}
		}
		fcols[c] = append(fcols[c], item(i))
	}
	ucols := make([][]layout.FlexChild, cols)
	for j, i := range user {
		ucols[j%cols] = append(ucols[j%cols], item(i))
	}
	return panel(gtx, th.Panel, 14, 16, func(gtx layout.Context) layout.Dimensions {
		title := fmt.Sprintf("FOOTSWITCH %d · ", b.pick%BoardSlots+1)
		if name := b.BankName(b.pick / BoardSlots); name != "" {
			title += strings.ToUpper(name)
		} else {
			title += fmt.Sprintf("BANCO %d", b.pick/BoardSlots+1)
		}
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &b.back, "◀ Voltar", 0) }),
					layout.Rigid(spacer(12, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, title, textOpts{size: 16, color: th.Text, bold: true})
					}),
				)
			}),
			layout.Rigid(spacer(0, 10)),
		}
		if !themed && len(factory) > 0 {
			ch = append(ch, heading("FÁBRICA"))
		}
		ch = append(ch, grid(fcols))
		if len(user) > 0 {
			ch = append(ch, layout.Rigid(spacer(0, 6)), heading("MEUS PRESETS"), grid(ucols))
		}
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return choice(gtx, th, &b.empty, "Vazio (sem preset)", cur == "", gtx.Constraints.Max.X)
			})
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}
