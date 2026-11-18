package ui

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
)

// AudioID é o "pedal" da sobreposição que mostra as configurações de áudio.
const AudioID = "__audio__"

// BufferSizes são os tamanhos de buffer oferecidos.
var BufferSizes = []uint32{64, 128, 256, 512, 1024}

// AudioPanel escolhe saída, entrada e buffer (reabre a placa) e roda o teste de latência.
type AudioPanel struct {
	s    *audio.Session
	post func(func())

	outs, ins []audio.DeviceInfo
	listErr   error

	outSel, inSel int // índices; -1 = padrão; inSel == audio.NoInput = nenhuma
	bufSel        uint32

	outBtns, inBtns []widget.Clickable
	outDefault      widget.Clickable
	inDefault       widget.Clickable
	inNone          widget.Clickable
	bufBtns         [8]widget.Clickable
	apply, measure  widget.Clickable

	applying   bool
	msg        string
	msgErr     bool
	measured   float64 // última latência medida (ms); 0 = nenhuma
	spread     float64 // variação entre as medições (ms)
	consistent bool
}

// NewAudioPanel cria o painel. post agenda funções na goroutine da GUI.
func NewAudioPanel(s *audio.Session, post func(func())) *AudioPanel {
	return &AudioPanel{s: s, post: post}
}

// Refresh relê os dispositivos e a configuração atual (ao abrir o painel).
func (p *AudioPanel) Refresh() {
	p.outs, p.ins, p.listErr = p.s.Host.Devices()
	p.outBtns = make([]widget.Clickable, len(p.outs))
	p.inBtns = make([]widget.Clickable, len(p.ins))
	cfg := p.s.Host.Config()
	p.outSel, p.inSel, p.bufSel = cfg.Output, cfg.Input, cfg.Buffer
	if p.bufSel == 0 {
		p.bufSel = 256
	}
	p.msg = ""
}

// Measured devolve a última latência medida em ms (0 se nenhuma).
func (p *AudioPanel) Measured() float64 { return p.measured }

func (p *AudioPanel) config() audio.Config {
	cfg := p.s.Host.Config()
	cfg.Output, cfg.Input, cfg.Buffer = p.outSel, p.inSel, p.bufSel
	return cfg
}

func (p *AudioPanel) update(gtx layout.Context) {
	for i := range p.outBtns {
		if p.outBtns[i].Clicked(gtx) {
			p.outSel = i
		}
	}
	for i := range p.inBtns {
		if p.inBtns[i].Clicked(gtx) {
			p.inSel = i
		}
	}
	if p.outDefault.Clicked(gtx) {
		p.outSel = -1
	}
	if p.inDefault.Clicked(gtx) {
		p.inSel = -1
	}
	if p.inNone.Clicked(gtx) {
		p.inSel = audio.NoInput
	}
	for i, b := range BufferSizes {
		if p.bufBtns[i].Clicked(gtx) {
			p.bufSel = b
		}
	}
	if p.apply.Clicked(gtx) && !p.applying {
		p.applying = true
		p.msg, p.msgErr = "reabrindo a placa…", false
		cfg := p.config()
		go func() { // abrir a placa pode demorar; não trava a janela
			err := p.s.Host.Open(cfg)
			p.post(func() {
				p.applying = false
				switch {
				case err != nil:
					p.msg, p.msgErr = "erro: "+err.Error(), true
				case p.s.Host.State().Warning != "":
					p.msg, p.msgErr = p.s.Host.State().Warning, true
				default:
					p.msg, p.msgErr = "placa reaberta", false
				}
			})
		}()
	}
	if p.measure.Clicked(gtx) {
		if err := p.s.Renderer.StartLatencyTest(); err != nil {
			p.msg, p.msgErr = err.Error(), true
		} else {
			p.msg, p.msgErr = "medindo… (5 cliques curtos na saída)", false
		}
	}
	if res := p.s.Renderer.LatencyResult(); res.Done && res.OK {
		p.measured = res.Ms
		p.spread, p.consistent = res.SpreadMs, res.Consistent
	}
}

// AudioPanelWidthDp é a largura do painel.
const AudioPanelWidthDp = 640

// Layout desenha o painel.
func (p *AudioPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	p.update(gtx)
	w := gtx.Dp(AudioPanelWidthDp)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
	st := p.s.Host.State()

	title := func(s string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, s, textOpts{size: 12, color: th.Gold, bold: true})
			})
		})
	}
	row := func(c *widget.Clickable, label string, on bool) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return choice(gtx, th, c, label, on, gtx.Constraints.Max.X)
			})
		})
	}
	list := func(sel int, def *widget.Clickable, btns []widget.Clickable, devs []audio.DeviceInfo, extra ...layout.FlexChild) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			ch := []layout.FlexChild{row(def, "Padrão do sistema", sel == -1)}
			for i, d := range devs {
				ch = append(ch, row(&btns[i], d.Name, sel == i))
			}
			ch = append(ch, extra...)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}
	}

	lat := p.s.Host.EstimatedLatencyMs(p.s.Chain.Latency())
	latText := fmt.Sprintf("estimada pelos buffers: %.1f ms", lat)
	if res := p.s.Renderer.LatencyResult(); res.Running {
		latText = "medindo…"
	} else if res.Done && !res.OK {
		latText = fmt.Sprintf("o pulso não voltou (%d de %d; pico %.0f dBFS, ruído %.0f dBFS): confira o cabo, o canal e o volume",
			res.Successes, audio.LatencyAttempts, res.PeakDB, res.NoiseDB)
	} else if p.measured > 0 {
		cadeia := 1000 * float64(p.s.Chain.Latency()) / p.s.Renderer.SampleRate()
		latText = fmt.Sprintf("medida: %.1f ms ida e volta (variação %.1f ms)  ·  com a cadeia: %.1f ms  ·  estimada: %.1f ms",
			p.measured, p.spread, p.measured+cadeia, lat)
		if !p.consistent {
			latText += "  ·  ATENÇÃO: medições inconsistentes (som entrou junto?); meça de novo em silêncio"
		}
		if p.measured > 20 && p.s.Host.Config().Backend != "jack" {
			latText += "  ·  dica: com o PipeWire, o backend JACK (pipewire-jack) dá latência bem menor"
		}
	}

	return panel(gtx, th.Panel, 14, 18, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "ÁUDIO", textOpts{size: 20, color: th.Text, bold: true})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				in := "nenhuma"
				if st.Duplex {
					in = fmt.Sprintf("%s (%d canais)", st.InputName, st.InChannels)
				}
				s := fmt.Sprintf("aberto (%s): saída %s · entrada %s · %.0f Hz · buffer %d", st.Backend, st.OutputName, in, st.SampleRate, st.Buffer)
				if !st.Open {
					s = "placa fechada"
				}
				return drawText(gtx, th, s, textOpts{size: 11, color: th.Dim})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.listErr == nil {
					return layout.Dimensions{}
				}
				return drawText(gtx, th, "erro ao listar: "+p.listErr.Error(), textOpts{size: 12, color: th.Clip})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, title("SAÍDA"),
							layout.Rigid(list(p.outSel, &p.outDefault, p.outBtns, p.outs)))
					}),
					layout.Rigid(spacer(16, 0)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, title("ENTRADA (guitarra)"),
							layout.Rigid(list(p.inSel, &p.inDefault, p.inBtns, p.ins,
								row(&p.inNone, "Nenhuma (só tocar arquivos)", p.inSel == audio.NoInput))))
					}),
				)
			}),
			title("BUFFER (quadros por período)"),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				var ch []layout.FlexChild
				for i, b := range BufferSizes {
					ms := 1000 * float64(b) / p.s.Renderer.SampleRate()
					ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return choice(gtx, th, &p.bufBtns[i], fmt.Sprintf("%d (%.1f ms)", b, ms), p.bufSel == b, gtx.Dp(108))
					}), layout.Rigid(spacer(6, 0)))
				}
				return layout.Flex{}.Layout(gtx, ch...)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "menor = menos atraso ao tocar; se aparecerem estalos, aumente", textOpts{size: 11, color: th.Dim})
			}),
			layout.Rigid(spacer(0, 10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := "Aplicar (reabre a placa)"
				if p.applying {
					label = "Reabrindo…"
				}
				return coloredButton(gtx, th, &p.apply, label, 0, darken(th.Good, 0.45), th.Text)
			}),
			title("LATÊNCIA"),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "Para medir: ligue um cabo da saída da placa numa entrada (canal escolhido) e baixe o volume.",
					textOpts{size: 11, color: th.Dim})
			}),
			layout.Rigid(spacer(0, 6)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return smallButton(gtx, th, &p.measure, "Medir latência", 0)
					}),
					layout.Rigid(spacer(10, 0)),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return drawText(gtx, th, latText, textOpts{size: 12, color: th.Text})
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.msg == "" {
					return layout.Dimensions{}
				}
				c := th.Dim
				if p.msgErr {
					c = th.Clip
				}
				return layout.Inset{Top: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return drawText(gtx, th, p.msg, textOpts{size: 12, color: c})
				})
			}),
		)
	})
}

// choice é uma opção selecionável (linha com marcador).
func choice(gtx layout.Context, th *Theme, c *widget.Clickable, label string, on bool, width int) layout.Dimensions {
	bg := th.PanelHi
	fg := th.Text
	if on {
		bg = darken(th.Gold, 0.35)
	}
	return coloredButton(gtx, th, c, label, width, bg, fg)
}
