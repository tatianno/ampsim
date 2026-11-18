package ui

import (
	"fmt"
	"image"
	"os/exec"
	"path/filepath"

	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
)

// RecordingsID é a sobreposição das gravações.
const RecordingsID = "__gravacoes__"

// maxRecordingsShown é quantas gravações a lista mostra.
const maxRecordingsShown = 10

// recRow são os botões de uma gravação da lista.
type recRow struct{ play, di widget.Clickable }

// RecordingsPanel mostra a gravação que acabou de terminar (se houver) e as anteriores,
// com atalhos para ouvir, reamplificar (abrir a DI) e abrir a pasta.
type RecordingsPanel struct {
	dir    string
	list   []audio.Recording
	err    error
	last   *audio.Take // gravação recém-terminada (nil se o painel foi aberto pelo botão)
	rows   []recRow
	folder widget.Clickable
	// Load abre um arquivo no player (fonte Arquivo). processed = true para a gravação com
	// efeitos (toca como foi gravada, sem passar pela cadeia de novo); false para a DI
	// (passa pela cadeia: reamplificar). OpenFolder abre a pasta.
	Load       func(path string, processed bool)
	OpenFolder func(dir string)
}

// NewRecordingsPanel cria o painel para a pasta das gravações.
func NewRecordingsPanel(dir string) *RecordingsPanel {
	return &RecordingsPanel{dir: dir, OpenFolder: openFolder}
}

// Show relê a pasta. take é a gravação recém-terminada (nil = só a lista).
func (p *RecordingsPanel) Show(take *audio.Take) {
	p.last = take
	p.list, p.err = audio.ListRecordings(p.dir)
	if len(p.list) > maxRecordingsShown {
		p.list = p.list[:maxRecordingsShown]
	}
	p.rows = make([]recRow, len(p.list))
}

func openFolder(dir string) {
	if cmd := exec.Command("xdg-open", dir); cmd.Start() == nil {
		go cmd.Wait() // não deixa processo zumbi
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
}

func humanDuration(s float64) string {
	return fmt.Sprintf("%d:%02d", int(s)/60, int(s)%60)
}

// RecordingsWidthDp é a largura do painel.
const RecordingsWidthDp = 620

// Layout desenha o painel.
func (p *RecordingsPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	for i := range p.rows {
		if p.rows[i].play.Clicked(gtx) && p.Load != nil {
			p.Load(p.list[i].Out, true)
		}
		if p.rows[i].di.Clicked(gtx) && p.Load != nil && p.list[i].DI != "" {
			p.Load(p.list[i].DI, false)
		}
	}
	if p.folder.Clicked(gtx) && p.OpenFolder != nil {
		p.OpenFolder(p.dir)
	}
	w := gtx.Dp(RecordingsWidthDp)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}

	var ch []layout.FlexChild
	title := "GRAVAÇÕES"
	if p.last != nil {
		title = "GRAVAÇÃO SALVA"
	}
	ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return drawText(gtx, th, title, textOpts{size: 20, color: th.Text, bold: true})
	}))
	if p.last != nil {
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 8, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.takeBox(gtx, th)
			})
		}))
	}
	ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "NA PASTA (mais novas primeiro)", textOpts{size: 11, color: th.Gold, bold: true})
		})
	}))
	if p.err != nil {
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, p.err.Error(), textOpts{size: 12, color: th.Clip})
		}))
	}
	if len(p.list) == 0 {
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "nenhuma gravação ainda: use ● Gravar no rodapé", textOpts{size: 12, color: th.Dim})
		}))
	}
	for i, r := range p.list {
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.row(gtx, th, i, r)
			})
		}))
	}
	ch = append(ch,
		layout.Rigid(spacer(0, 10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &p.folder, "Abrir pasta", 0) }),
				layout.Rigid(spacer(10, 0)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, p.dir, textOpts{size: 11, color: th.Dim, width: gtx.Constraints.Max.X})
				}),
			)
		}),
	)
	return panel(gtx, th.Panel, 14, 18, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}

// takeBox resume a gravação recém-terminada.
func (p *RecordingsPanel) takeBox(gtx layout.Context, th *Theme) layout.Dimensions {
	t := p.last
	lines := []string{
		fmt.Sprintf("Duração: %s", humanDuration(t.Seconds)),
		"Com efeitos (o que você ouviu):  " + filepath.Base(t.Out),
	}
	if t.DI != "" {
		lines = append(lines, "Sem efeitos, DI (para trocar o som depois):  "+filepath.Base(t.DI))
	} else {
		lines = append(lines, "DI: não gravada (chave DI desligada)")
	}
	warn := 0
	if t.Silent() {
		lines = append(lines, fmt.Sprintf("ATENÇÃO: a gravação ficou em silêncio (pico %.0f dBFS). A guitarra estava tocando e com sinal no medidor da ENTRADA?", t.PeakDB))
		warn++
	}
	if t.Dropped > 0 {
		lines = append(lines, fmt.Sprintf("ATENÇÃO: %d amostras perdidas (o disco não acompanhou)", t.Dropped))
		warn++
	}
	box := darken(th.Good, 0.7)
	if t.Silent() {
		box = darken(th.Warn, 0.75)
	}
	return panel(gtx, box, 8, 10, func(gtx layout.Context) layout.Dimensions {
		var ch []layout.FlexChild
		for i, l := range lines {
			c := th.Text
			if i == 0 {
				c = th.Good
			}
			if i >= len(lines)-warn {
				c = th.Warn
			}
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, l, textOpts{size: 12, color: c, bold: i == 0})
				})
			}))
		}
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "► Ouvir toca como foi gravada; DI (reamp) passa a guitarra limpa pelo som atual.", textOpts{size: 11, color: th.Dim})
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}

// row é uma gravação da lista: data, duração, tamanho e os botões.
func (p *RecordingsPanel) row(gtx layout.Context, th *Theme, i int, r audio.Recording) layout.Dimensions {
	bg := th.PanelHi
	if i == 0 && p.last != nil {
		bg = darken(th.Good, 0.6) // a recém-gravada
	}
	return panel(gtx, bg, 6, 6, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, r.Name(), textOpts{size: 13, color: th.Text, bold: true, width: gtx.Dp(130)})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, humanDuration(r.Seconds), textOpts{size: 12, color: th.Dim, width: gtx.Dp(50), align: text.End})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, humanBytes(r.Bytes), textOpts{size: 12, color: th.Dim, width: gtx.Dp(80), align: text.End})
			}),
			layout.Flexed(1, hfill),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return smallButton(gtx, th, &p.rows[i].play, "► Ouvir", 0)
			}),
			layout.Rigid(spacer(6, 0)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if r.DI == "" {
					return drawText(gtx, th, "sem DI", textOpts{size: 11, color: th.Dim, width: gtx.Dp(92), align: text.Middle})
				}
				return smallButton(gtx, th, &p.rows[i].di, "DI (reamp)", gtx.Dp(92))
			}),
		)
	})
}
