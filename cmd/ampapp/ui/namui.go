package ui

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/engine"
)

// NAMListID é a sobreposição com a lista de modelos NAM.
const NAMListID = "__nam__"

// namLabel é o nome curto do modelo escolhido.
func namLabel(pre *engine.Pre) string {
	if info := pre.NAMInfo(); info != nil && info.Ref == pre.NAMRef() {
		return info.Name
	}
	ref := pre.NAMRef()
	if n, ok := strings.CutPrefix(ref, capturas.Prefix); ok {
		return n
	}
	return baseNoExt(ref)
}

func baseNoExt(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(strings.TrimSuffix(p, ".nam"), ".NAM")
}

// NAMList é a lista de modelos: as capturas incluídas e "abrir arquivo".
type NAMList struct {
	chain *engine.Chain
	names []string
	rows  []widget.Clickable
	open  widget.Clickable
	// Load carrega um modelo fora da thread da GUI (o app liga ao carregamento assíncrono).
	Load func(ref string)
	// OnOpenFile abre o seletor de arquivo .nam.
	OnOpenFile func()
}

// NewNAMList cria a lista.
func NewNAMList(chain *engine.Chain) *NAMList {
	names := capturas.Names()
	return &NAMList{chain: chain, names: names, rows: make([]widget.Clickable, len(names))}
}

// Layout desenha a lista (sobreposição).
func (l *NAMList) Layout(gtx layout.Context, th *Theme, close func()) layout.Dimensions {
	pre := l.chain.Pre()
	for i := range l.rows {
		if l.rows[i].Clicked(gtx) && l.Load != nil {
			l.Load(capturas.Prefix + l.names[i])
			close()
		}
	}
	if l.open.Clicked(gtx) && l.OnOpenFile != nil {
		close()
		l.OnOpenFile()
	}
	cur := pre.NAMRef()
	w := gtx.Dp(400)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return panel(gtx, th.Panel, 14, 16, func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "MODELOS NAM", textOpts{size: 18, color: th.Text, bold: true})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "INCLUÍDAS · só o amp (use o gabinete)", textOpts{size: 11, color: th.Gold, bold: true})
				})
			}),
		}
		for i, n := range l.names {
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return choice(gtx, th, &l.rows[i], n, cur == capturas.Prefix+n, gtx.Constraints.Max.X)
				})
			}))
		}
		if !strings.HasPrefix(cur, capturas.Prefix) {
			ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "ARQUIVO: "+baseNoExt(cur), textOpts{size: 11, color: th.Gold, bold: true})
				})
			}))
		}
		ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return smallButton(gtx, th, &l.open, "Abrir arquivo .nam…", gtx.Constraints.Max.X)
			})
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}

// namSection é a seção NAM do cabeçote: modelo, tamanho, informações e o knob de entrada.
func (h *Head) namSection(gtx layout.Context, th *Theme) layout.Dimensions {
	if h.pickNAM.Clicked(gtx) && h.OnPickNAM != nil {
		h.OnPickNAM()
	}
	pre := h.chain.Pre()
	on := h.chain.Enabled(engine.PreID)
	info := pre.NAMInfo()
	detail := ""
	if info != nil {
		detail = fmt.Sprintf("%d canais", info.Channels)
		if info.Size != "" {
			detail += " · " + info.Size
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, "NAM", textOpts{size: 11, color: withAlpha(th.Gold, 200), bold: true})
				}),
				layout.Rigid(spacer(8, 0)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return toggle(gtx, th, &h.preSw, on, "ON")
				}),
				layout.Rigid(spacer(12, 0)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if h.sizeSel == nil {
						return layout.Dimensions{}
					}
					return h.sizeSel.Layout(gtx, th, withAlpha(th.Gold, 220), th.Gold)
				}),
			)
		}),
		layout.Rigid(spacer(0, 6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return smallButton(gtx, th, &h.pickNAM, "Modelo: "+namLabel(pre)+"  ▾", gtx.Constraints.Max.X)
						}),
						layout.Rigid(spacer(0, 6)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return drawText(gtx, th, detail, textOpts{size: 11, color: withAlpha(th.Gold, 170)})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if info == nil || info.Warning == "" {
								return layout.Dimensions{}
							}
							return drawText(gtx, th, info.Warning, textOpts{size: 11, color: th.Warn})
						}),
					)
				}),
				layout.Rigid(spacer(12, 0)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if h.namKnob == nil {
						return layout.Dimensions{}
					}
					return h.namKnob.Layout(gtx, th, 56, rgb(0x161616), th.Gold, th.Gold)
				}),
			)
		}),
	)
}
