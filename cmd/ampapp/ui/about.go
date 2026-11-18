package ui

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"os/exec"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// AboutID é a sobreposição "Sobre".
const AboutID = "__sobre__"

// Dados da área Sobre.
const (
	RepoURL = "https://github.com/tatianno/ampsim"
	Contact = "tferreiraalves@gmail.com"
)

//go:embed assets/ampsim-128.png
var iconPNG []byte

// openURL abre um link no navegador ou no programa de e-mail do sistema.
func openURL(url string) error { return exec.Command("xdg-open", url).Start() }

// About é a área "Sobre": ícone, versão, repositório e contato.
type About struct {
	version     string
	icon        paint.ImageOp
	repo, email widget.Clickable
	// OpenURL abre um link (padrão: xdg-open; os testes trocam).
	OpenURL func(url string) error
	status  func(string, bool)
}

// NewAbout cria a área Sobre.
func NewAbout(version string, status func(string, bool)) *About {
	ab := &About{version: version, OpenURL: openURL, status: status}
	if img, err := png.Decode(bytes.NewReader(iconPNG)); err == nil {
		ab.icon = paint.NewImageOp(img)
	}
	return ab
}

func (ab *About) open(url string) {
	if err := ab.OpenURL(url); err != nil && ab.status != nil {
		ab.status("não consegui abrir "+url+": "+err.Error(), true)
	}
}

// Layout desenha a área Sobre (sobreposição).
func (ab *About) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	if ab.repo.Clicked(gtx) {
		ab.open(RepoURL)
	}
	if ab.email.Clicked(gtx) {
		ab.open("mailto:" + Contact + "?subject=ampsim")
	}
	w := gtx.Dp(460)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	version := ab.version
	if version == "" {
		version = "dev"
	}
	row := func(label, value string, btn *widget.Clickable, action string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, label, textOpts{size: 12, color: th.Dim, bold: true, width: gtx.Dp(100)})
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, value, textOpts{size: 13, color: th.Text})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return smallButton(gtx, th, btn, action, 0)
					}),
				)
			})
		})
	}
	return panel(gtx, th.Panel, 14, 18, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if ab.icon.Size().X == 0 {
							return layout.Dimensions{}
						}
						img := widget.Image{Src: ab.icon, Fit: widget.Contain}
						sz := gtx.Dp(88)
						gtx.Constraints = layout.Exact(image.Pt(sz, sz))
						return img.Layout(gtx)
					}),
					layout.Rigid(spacer(14, 0)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return drawText(gtx, th, "AmpSim", textOpts{size: 26, color: th.Gold, bold: true})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return drawText(gtx, th, "versão "+version, textOpts{size: 13, color: th.Text})
							}),
							layout.Rigid(spacer(0, 4)),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return drawText(gtx, th, "simulador de amplificador de guitarra e pedais", textOpts{size: 12, color: th.Dim})
							}),
						)
					}),
				)
			}),
			layout.Rigid(spacer(0, 8)),
			row("Repositório", "github.com/tatianno/ampsim", &ab.repo, "Abrir"),
			row("Contato", Contact, &ab.email, "Escrever"),
			layout.Rigid(spacer(0, 14)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "Licença MIT. Modelos NAM: formato e referência do Neural Amp Modeler",
					textOpts{size: 11, color: th.Dim})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "(Steven Atkinson, MIT), executados aqui em Go puro.",
					textOpts{size: 11, color: th.Dim})
			}),
		)
	})
}
