package ui

import (
	"image"

	"gioui.org/layout"
)

// ShortcutsID é a sobreposição "Atalhos" (aberta pelo Menu).
const ShortcutsID = "__atalhos__"

// shortcut é uma linha da lista de atalhos.
type shortcut struct{ keys, what string }

// Atalhos do teclado (tratados em App.keys) e do mouse. Ao mudar App.keys, mude aqui também
// (TestAtalhosListados confere as teclas).
var (
	keyShortcuts = []shortcut{
		{"T", "Tap tempo no metrônomo ou no pedal aberto; sem nada aberto, no Delay, Tremolo e Synth"},
		{"A", "Abre e fecha o afinador"},
		{"M", "Liga e desliga o metrônomo (o clique não entra na gravação)"},
		{"1 a 6", "Footswitches da pedaleira (presets do banco atual)"},
		{"Espaço", "Play / pause do arquivo (com a fonte em Arquivo)"},
		{"Esc", "Fecha o menu ou o painel aberto"},
	}
	mouseShortcuts = []shortcut{
		{"Shift + arrastar", "Nos knobs: ajuste fino (5x mais preciso)"},
		{"Duplo clique", "Nos knobs: volta ao valor padrão; no ponto do EQ: zera o ganho"},
		{"Roda do mouse", "Nos knobs: ajusta o valor; sobre um ponto do EQ: muda o Q (médios)"},
		{"Shift + arrastar", "Na barra do arquivo: escolhe o trecho do loop (duplo clique: arquivo inteiro)"},
		{"Arrastar", "Na cadeia de sinal: muda a ordem dos pedais"},
	}
)

// shortcutsLayout desenha a lista de atalhos.
func shortcutsLayout(gtx layout.Context, th *Theme) layout.Dimensions {
	w := min(gtx.Dp(620), gtx.Constraints.Max.X-gtx.Dp(40))
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	section := func(title string, list []shortcut) []layout.FlexChild {
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, title, textOpts{size: 12, color: th.Dim, bold: true})
			}),
			layout.Rigid(spacer(0, 4)),
		}
		for _, s := range list {
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 5}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return drawText(gtx, th, s.keys, textOpts{size: 13, color: th.Gold, bold: true, width: gtx.Dp(140)})
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return drawText(gtx, th, s.what, textOpts{size: 13, color: th.Text, width: gtx.Constraints.Max.X})
						}),
					)
				})
			}))
		}
		return ch
	}
	return panel(gtx, th.Panel, 14, 18, func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "Atalhos", textOpts{size: 20, color: th.Gold, bold: true})
			}),
			layout.Rigid(spacer(0, 12)),
		}
		ch = append(ch, section("TECLADO", keyShortcuts)...)
		ch = append(ch, layout.Rigid(spacer(0, 16)))
		ch = append(ch, section("MOUSE", mouseShortcuts)...)
		ch = append(ch, layout.Rigid(spacer(0, 12)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "As teclas não valem enquanto você digita o nome de um preset ou de um banco.",
				textOpts{size: 11, color: th.Dim})
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}
