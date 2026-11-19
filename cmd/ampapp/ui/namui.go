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

// Sobreposições com as listas de modelos NAM (amp e pedal) e de IRs.
const (
	NAMListID      = "__nam__"
	DriveNAMListID = "__namdrive__"
	IRListID       = "__ir__"
)

// namHolder é um bloco com modelo NAM (o pré-amp ou o drive).
type namHolder interface {
	NAMRef() string
	NAMInfo() *engine.NAMInfo
}

// namLabel é o nome curto do modelo escolhido.
func namLabel(h namHolder) string {
	if info := h.NAMInfo(); info != nil && info.Ref == h.NAMRef() {
		return info.Name
	}
	ref := h.NAMRef()
	if n, ok := strings.CutPrefix(ref, capturas.Prefix); ok {
		return n
	}
	return baseNoExt(ref)
}

func baseNoExt(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndex(p, "."); i > 0 {
		p = p[:i]
	}
	return p
}

// AssetList é uma lista de escolha (sobreposição): itens incluídos no app e "abrir arquivo".
// Serve para os modelos NAM e para as IRs do gabinete.
type AssetList struct {
	title, section, openLabel string
	names                     []string
	rows                      []widget.Clickable
	open                      widget.Clickable
	current                   func() string // ref atual: capturas.Prefix+nome ou caminho
	// Load carrega um item incluído (ref = capturas.Prefix + nome), fora da thread da GUI.
	Load func(ref string)
	// OnOpenFile abre o seletor de arquivo.
	OnOpenFile func()
}

// NAMList é a lista de modelos NAM.
type NAMList = AssetList

// NewNAMList cria a lista de modelos NAM.
func NewNAMList(chain *engine.Chain) *AssetList {
	return newAssetList("MODELOS NAM", "INCLUÍDAS · só o amp (use o gabinete)", "Abrir arquivo .nam…",
		capturas.Names(), func() string { return chain.Pre().NAMRef() })
}

// NewDriveNAMList cria a lista de capturas de pedal do drive.
func NewDriveNAMList(chain *engine.Chain) *AssetList {
	return newAssetList("CAPTURAS DE PEDAL", "INCLUÍDAS · só o pedal (vai na frente do amp)", "Abrir arquivo .nam…",
		capturas.PedalNames(), func() string { return chain.Drive().NAMRef() })
}

// namDetail descreve o modelo em uso: canais e tamanho.
func namDetail(info *engine.NAMInfo) string {
	if info == nil {
		return ""
	}
	d := fmt.Sprintf("%d canais", info.Channels)
	if info.Size != "" {
		d += " · " + info.Size
	}
	return d
}

// NewIRList cria a lista de IRs do gabinete.
func NewIRList(chain *engine.Chain) *AssetList {
	return newAssetList("GABINETE (IR)", "INCLUÍDAS", "Abrir arquivo WAV…", capturas.IRNames(), func() string {
		if p := chain.Cab().IRPath(); p != "" {
			return p
		}
		return capturas.Prefix + capturas.DefaultIRName
	})
}

func newAssetList(title, section, openLabel string, names []string, current func() string) *AssetList {
	return &AssetList{title: title, section: section, openLabel: openLabel, names: names,
		rows: make([]widget.Clickable, len(names)), current: current}
}

// Layout desenha a lista (sobreposição).
func (l *AssetList) Layout(gtx layout.Context, th *Theme, close func()) layout.Dimensions {
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
	cur := l.current()
	w := gtx.Dp(400)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	return panel(gtx, th.Panel, 14, 16, func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, l.title, textOpts{size: 18, color: th.Text, bold: true})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, l.section, textOpts{size: 11, color: th.Gold, bold: true})
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
				return smallButton(gtx, th, &l.open, l.openLabel, gtx.Constraints.Max.X)
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
	detail := namDetail(info)
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
