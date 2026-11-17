// Pacote ui é a interface gráfica (Gio) do ampsim.
//
// Não importa gioui.org/app (a janela, que depende de bibliotecas do sistema): só layout,
// desenho e eventos. Assim compila e é testado em qualquer máquina; a janela fica em
// cmd/ampapp/gui.go.
//
// Os widgets leem e escrevem direto nos engine.Param (atomics): o engine é sempre a
// fonte da verdade, e a GUI só desenha o estado atual a cada quadro.
package ui

import (
	"image/color"

	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/widget/material"
)

// Theme reúne as cores e a tipografia.
type Theme struct {
	M *material.Theme

	Bg, Panel, PanelHi, Line, Text, Dim, Gold color.NRGBA
	LedOn, LedOff, Clip, Loop, Good, Warn     color.NRGBA
	Tolex, Faceplate, Grill                   color.NRGBA
}

// NewTheme cria o tema escuro padrão.
func NewTheme() *Theme {
	m := material.NewTheme()
	m.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	t := &Theme{
		M:         m,
		Bg:        rgb(0x15171a),
		Panel:     rgb(0x22262b),
		PanelHi:   rgb(0x2e333a),
		Line:      rgb(0x3a4048),
		Text:      rgb(0xe8e8e8),
		Dim:       rgb(0x8a9099),
		Gold:      rgb(0xd4af37),
		LedOn:     rgb(0xff3b30),
		LedOff:    rgb(0x4a2020),
		Clip:      rgb(0xff3b30),
		Loop:      rgb(0x1f2a36),
		Good:      rgb(0x34c759),
		Warn:      rgb(0xffcc00),
		Tolex:     rgb(0x1b1b1b),
		Faceplate: rgb(0x2b2720),
		Grill:     rgb(0x2a2420),
	}
	m.Palette = material.Palette{Fg: t.Text, Bg: t.Bg, ContrastBg: t.PanelHi, ContrastFg: t.Text}
	m.TextSize = 14
	return t
}

// PedalStyle é a aparência de um pedal.
type PedalStyle struct {
	Body, Text, Knob color.NRGBA
}

var pedalStyles = map[string]PedalStyle{
	"gate":    {Body: rgb(0x3a3f44), Text: rgb(0xf2f2f2), Knob: rgb(0x111111)},
	"chorus":  {Body: rgb(0x4aa3c9), Text: rgb(0x0d2633), Knob: rgb(0x1a1a1a)},
	"flanger": {Body: rgb(0x7b4fa8), Text: rgb(0xf4ecff), Knob: rgb(0x1a1a1a)},
	"phaser":  {Body: rgb(0xe07b24), Text: rgb(0x2b1503), Knob: rgb(0x1a1a1a)},
	"delay":   {Body: rgb(0x3f9d5a), Text: rgb(0x0c2414), Knob: rgb(0x1a1a1a)},
	"reverb":  {Body: rgb(0x2f5f8f), Text: rgb(0xe6f0fa), Knob: rgb(0x111111)},
	"pre":     {Body: rgb(0x1b1b1b), Text: rgb(0xd4af37), Knob: rgb(0x111111)},
	"power":   {Body: rgb(0x1b1b1b), Text: rgb(0xd4af37), Knob: rgb(0x111111)},
	"cab":     {Body: rgb(0x2a2420), Text: rgb(0xd9c7a3), Knob: rgb(0x111111)},
}

// StyleFor devolve o estilo do módulo (cinza se não houver um específico).
func StyleFor(id string) PedalStyle {
	if s, ok := pedalStyles[id]; ok {
		return s
	}
	return PedalStyle{Body: rgb(0x555b62), Text: rgb(0xf2f2f2), Knob: rgb(0x1a1a1a)}
}

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// mix interpola entre duas cores (t = 0 → a, t = 1 → b).
func mix(a, b color.NRGBA, t float32) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

func lighten(c color.NRGBA, t float32) color.NRGBA { return mix(c, rgb(0xffffff), t) }
func darken(c color.NRGBA, t float32) color.NRGBA  { return mix(c, rgb(0x000000), t) }
