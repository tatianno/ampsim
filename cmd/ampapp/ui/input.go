package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/source"
)

// Faixa ideal do nível de entrada (pico, dBFS): o ganho do amp foi calibrado com uma DI
// gravada nessa faixa. Acima de −3 dBFS o conversor está perto de clipar.
const (
	idealLowDB  = -18.0
	idealHighDB = -6.0
)

// MaxInputChannels é quantos botões de canal mostramos no máximo.
const MaxInputChannels = 8

// InputPanel escolhe a fonte (arquivo ou guitarra), o canal e o trim, e mostra o nível.
type InputPanel struct {
	s         *audio.Session
	fileBtn   widget.Clickable
	guitarBtn widget.Clickable
	chBtns    [MaxInputChannels]widget.Clickable
	mixBtn    widget.Clickable
	audioBtn  widget.Clickable
	trim      *Knob
	OnAudio   func() // abre as configurações de áudio
}

// NewInputPanel cria o painel de entrada.
func NewInputPanel(s *audio.Session) *InputPanel {
	return &InputPanel{s: s, trim: NewKnob(s.Live.Trim())}
}

func (p *InputPanel) update(gtx layout.Context) {
	if p.fileBtn.Clicked(gtx) {
		p.s.SetSource(audio.SourceFile)
	}
	if p.guitarBtn.Clicked(gtx) {
		p.s.SetSource(audio.SourceGuitar)
	}
	for i := range p.chBtns {
		if p.chBtns[i].Clicked(gtx) {
			p.s.Live.SetChannel(i)
		}
	}
	if p.mixBtn.Clicked(gtx) {
		p.s.Live.SetChannel(source.ChannelMix)
	}
	if p.audioBtn.Clicked(gtx) && p.OnAudio != nil {
		p.OnAudio()
	}
}

// Layout desenha o painel. m traz os níveis já suavizados.
func (p *InputPanel) Layout(gtx layout.Context, th *Theme, m *Meters) layout.Dimensions {
	p.update(gtx)
	st := p.s.Host.State()
	guitar := p.s.Source() == audio.SourceGuitar
	sel := func(on bool) color.NRGBA {
		if on {
			return darken(th.Gold, 0.35)
		}
		return th.PanelHi
	}
	label := func(s string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, s, textOpts{size: 11, color: th.Dim, bold: true})
		})
	}

	children := []layout.FlexChild{
		label("ENTRADA"),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return coloredButton(gtx, th, &p.fileBtn, "Arquivo", 0, sel(!guitar), th.Text)
		}),
		layout.Rigid(spacer(4, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return coloredButton(gtx, th, &p.guitarBtn, "Guitarra", 0, sel(guitar), th.Text)
		}),
		layout.Rigid(spacer(16, 0)),
		label("CANAL"),
		layout.Rigid(spacer(8, 0)),
	}
	if !st.Duplex {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "sem entrada aberta", textOpts{size: 12, color: th.Warn})
		}))
	} else {
		ch := p.s.Live.Channel()
		for i := 0; i < min(st.InChannels, MaxInputChannels); i++ {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return coloredButton(gtx, th, &p.chBtns[i], strconv.Itoa(i+1), gtx.Dp(30), sel(ch == i), th.Text)
			}), layout.Rigid(spacer(4, 0)))
		}
		if st.InChannels > 1 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return coloredButton(gtx, th, &p.mixBtn, "Média", 0, sel(ch == source.ChannelMix), th.Text)
			}))
		}
	}
	children = append(children,
		layout.Rigid(spacer(16, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.trim.Layout(gtx, th, 40, rgb(0x161616), th.Text, th.Gold)
		}),
		layout.Rigid(spacer(12, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// nível da guitarra depois do trim, mesmo com a fonte em Arquivo (para regular antes de tocar)
			return inputLevel(gtx, th, toDB(m.inRaw)+p.s.Live.Trim().Get(), st.Duplex)
		}),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(ledLabel(th, "CLIP", m.InputClipping(gtx.Now), th.LedOn, th.LedOff)),
		layout.Flexed(1, hfill),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return smallButton(gtx, th, &p.audioBtn, "Áudio…", 0)
		}),
	)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// inputLevel desenha o nível da guitarra depois do trim (dBFS), com a faixa ideal marcada.
func inputLevel(gtx layout.Context, th *Theme, db float64, active bool) layout.Dimensions {
	if !active {
		db = math.Inf(-1)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			w, h := gtx.Dp(180), gtx.Dp(12)
			x := func(db float64) int { return int(clamp01((db-meterFloorDB)/-meterFloorDB) * float64(w)) }
			fillRRect(gtx.Ops, image.Rect(0, 0, w, h), gtx.Dp(3), th.Line)
			// faixa ideal
			fillRRect(gtx.Ops, image.Rect(x(idealLowDB), 0, x(idealHighDB), h), 0, withAlpha(th.Good, 60))
			c := th.Good
			switch {
			case db > meterRed:
				c = th.Clip
			case db > idealHighDB:
				c = th.Warn
			}
			if lv := x(db); lv > 0 {
				fillRRect(gtx.Ops, image.Rect(0, gtx.Dp(3), lv, h-gtx.Dp(3)), gtx.Dp(2), c)
			}
			for _, d := range []float64{idealLowDB, idealHighDB} {
				strokeLine(gtx.Ops, f32.Pt(float32(x(d)), 0), f32.Pt(float32(x(d)), float32(h)), 1, withAlpha(th.Good, 200))
			}
			return layout.Dimensions{Size: image.Pt(w, h)}
		}),
		layout.Rigid(spacer(0, 3)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			s := "nível −∞"
			if db > -90 {
				s = fmt.Sprintf("nível %.0f dBFS", db)
			}
			s += fmt.Sprintf("  ·  ideal %.0f a %.0f", idealLowDB, idealHighDB)
			return drawText(gtx, th, s, textOpts{size: 10, color: th.Dim, width: gtx.Dp(180), align: text.Start})
		}),
	)
}
