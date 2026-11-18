package ui

import (
	"fmt"
	"image"
	"path/filepath"
	"sync"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/engine"
)

// App é a tela principal.
type App struct {
	Session  *audio.Session
	Chain    *engine.Chain
	Player   *source.FilePlayer
	Renderer *audio.Renderer
	Monitor  *audio.Monitor

	// Invalidate pede um novo quadro (a janela define; usado por callbacks de outras goroutines).
	Invalidate func()
	// Pick abre o seletor de arquivo; por padrão PickWAV (os testes trocam).
	Pick func(title string) (string, error)

	th        *Theme
	transport *Transport
	head      *Head
	cab       *CabPanel
	board     *Board
	input     *InputPanel
	audioSet  *AudioPanel
	meters    Meters
	pedals    map[string]*Pedal
	bypass    widget.Clickable
	closeBtn  widget.Clickable

	open     string // pedal aberto na sobreposição ("" = nenhum)
	scrim    int    // tags de evento da sobreposição
	panelTag int

	status      string
	statusErr   bool
	statusUntil time.Time

	mu      sync.Mutex
	pending []func()
}

// NewApp monta a tela sobre a sessão de áudio.
func NewApp(s *audio.Session) *App {
	chain := s.Chain
	a := &App{
		Session: s, Chain: chain, Player: s.Player, Renderer: s.Renderer, Monitor: s.Monitor,
		Pick:   PickWAV,
		th:     NewTheme(),
		pedals: map[string]*Pedal{},
	}
	a.transport = NewTransport(s.Player)
	a.input = NewInputPanel(s)
	a.audioSet = NewAudioPanel(s, a.Post)
	a.input.OnAudio = func() {
		a.audioSet.Refresh()
		a.open = AudioID
	}
	a.head = NewHead(chain)
	a.cab = NewCabPanel(chain)
	a.board = NewBoard(chain)
	for _, m := range chain.Modules() {
		if !isAmpBlock(m.ID()) {
			a.pedals[m.ID()] = NewPedal(chain, m)
		}
	}
	a.board.OnOpen = func(id string) {
		if _, ok := a.pedals[id]; ok {
			a.open = id
		}
	}
	a.transport.OnOpen = func() {
		a.pickAsync("Abrir WAV para tocar", func(path string) error { return a.Player.Load(path) })
	}
	a.cab.OnPick = func() {
		a.pickAsync("Escolher IR do gabinete", func(path string) error { return a.Chain.Cab().LoadIRFile(path) })
	}
	return a
}

// Open abre na sobreposição o pedal id, ou as configurações de áudio (AudioID).
func (a *App) Open(id string) {
	if id == AudioID {
		a.input.OnAudio()
		return
	}
	a.board.OnOpen(id)
}

// Opened devolve o pedal aberto ("" se nenhum).
func (a *App) Opened() string { return a.open }

// Post agenda f para rodar na goroutine da GUI no próximo quadro (seguro de qualquer goroutine).
func (a *App) Post(f func()) {
	a.mu.Lock()
	a.pending = append(a.pending, f)
	a.mu.Unlock()
	if a.Invalidate != nil {
		a.Invalidate()
	}
}

func (a *App) runPending() {
	a.mu.Lock()
	fs := a.pending
	a.pending = nil
	a.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

// pickAsync abre o seletor numa goroutine e aplica o arquivo escolhido.
func (a *App) pickAsync(title string, apply func(path string) error) {
	go func() {
		path, err := a.Pick(title)
		if err == nil && path != "" {
			err = apply(path) // carregar e preparar acontece fora da thread de áudio
		}
		a.Post(func() {
			switch {
			case err != nil:
				a.setStatus(err.Error(), true)
			case path != "":
				a.setStatus("carregado: "+filepath.Base(path), false)
			}
		})
	}()
}

func (a *App) setStatus(s string, isErr bool) {
	a.status, a.statusErr = s, isErr
	a.statusUntil = time.Now().Add(6 * time.Second)
}

// FrameInterval é o intervalo entre quadros enquanto há medidores animando (~30 fps).
const FrameInterval = 33 * time.Millisecond

// Layout desenha a tela inteira.
func (a *App) Layout(gtx layout.Context) layout.Dimensions {
	a.runPending()
	if a.Monitor != nil {
		a.meters.Update(a.Monitor.Take(), gtx.Now)
	}
	a.keys(gtx)
	if a.bypass.Clicked(gtx) && a.Renderer != nil {
		a.Renderer.SetBypass(!a.Renderer.Bypass())
	}
	th := a.th
	paint.Fill(gtx.Ops, th.Bg)

	layout.UniformInset(14).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(a.topBar),
			layout.Rigid(spacer(0, 10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th.Panel, 10, 10, func(gtx layout.Context) layout.Dimensions {
					return a.transport.Layout(gtx, th)
				})
			}),
			layout.Rigid(spacer(0, 8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th.Panel, 10, 8, func(gtx layout.Context) layout.Dimensions {
					return a.input.Layout(gtx, th, &a.meters)
				})
			}),
			layout.Rigid(spacer(0, 12)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return a.head.Layout(gtx, th) }),
					layout.Rigid(spacer(12, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.cab.Layout(gtx, th) }),
				)
			}),
			layout.Rigid(spacer(0, 12)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th.Panel, 10, 12, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return drawText(gtx, th, "CADEIA DE SINAL", textOpts{size: 12, color: th.Dim, bold: true})
						}),
						layout.Rigid(spacer(0, 6)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.board.Layout(gtx, th) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return drawText(gtx, th, "clique: abrir o pedal  ·  LED: liga/desliga  ·  arraste: muda a ordem (entre PRÉ e POWER = loop de efeitos)",
								textOpts{size: 11, color: withAlpha(th.Dim, 200)})
						}),
					)
				})
			}),
			layout.Flexed(1, vfill), // empurra o rodapé para baixo
			layout.Rigid(a.footer),
		)
	})

	if a.open != "" {
		a.overlay(gtx)
	}
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(FrameInterval)})
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

func (a *App) keys(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameSpace})
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameEscape:
			a.open = ""
		case key.NameSpace:
			a.transport.TogglePlay()
		}
	}
}

func (a *App) topBar(gtx layout.Context) layout.Dimensions {
	th := a.th
	info := "placa fechada"
	if st := a.Session.Host.State(); st.Open {
		in := "nenhuma"
		if st.Duplex {
			in = st.InputName
		}
		lat := fmt.Sprintf("latência ~%.1f ms", a.Session.Host.EstimatedLatencyMs(a.Chain.Latency()))
		if m := a.audioSet.Measured(); m > 0 {
			lat = fmt.Sprintf("latência medida %.1f ms", m)
		}
		info = fmt.Sprintf("%s  ·  saída: %s  ·  entrada: %s  ·  %.0f Hz  ·  buffer %d  ·  %s",
			st.Backend, st.OutputName, in, st.SampleRate, st.Buffer, lat)
	}
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "ampsim", textOpts{size: 22, color: th.Gold, bold: true})
		}),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "simulador de amp e pedais", textOpts{size: 12, color: th.Dim})
		}),
		layout.Rigid(spacer(16, 0)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, info, textOpts{size: 12, color: th.Dim, align: text.End, width: gtx.Constraints.Max.X})
		}),
	)
}

func (a *App) footer(gtx layout.Context) layout.Dimensions {
	th := a.th
	byp := a.Renderer != nil && a.Renderer.Bypass()
	label, bg := "A/B: processado", th.PanelHi
	if byp {
		label, bg = "A/B: SECO (bypass)", darken(th.Warn, 0.45)
	}
	return panel(gtx, th.Panel, 10, 10, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return coloredButton(gtx, th, &a.bypass, label, gtx.Dp(170), bg, th.Text)
			}),
			layout.Rigid(spacer(16, 0)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.meters.Layout(gtx, th) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if a.status == "" || gtx.Now.After(a.statusUntil) {
					return layout.Dimensions{}
				}
				c := th.Dim
				if a.statusErr {
					c = th.Clip
				}
				return drawText(gtx, th, a.status, textOpts{size: 12, color: c, align: text.End, width: gtx.Constraints.Max.X})
			}),
		)
	})
}

// overlay mostra o pedal aberto por cima da tela; clicar fora, Esc ou "×" fecha.
func (a *App) overlay(gtx layout.Context) {
	th := a.th
	var content func(gtx layout.Context) layout.Dimensions
	if a.open == AudioID {
		content = func(gtx layout.Context) layout.Dimensions { return a.audioSet.Layout(gtx, th) }
	} else if p := a.pedals[a.open]; p != nil {
		content = func(gtx layout.Context) layout.Dimensions { return p.Layout(gtx, th) }
	} else {
		a.open = ""
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &a.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			a.open = ""
		}
	}
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &a.panelTag, Kinds: pointer.Press}); !ok {
			break
		}
	}
	if a.closeBtn.Clicked(gtx) {
		a.open = ""
	}

	full := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	paint.ColorOp{Color: withAlpha(rgb(0x000000), 170)}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	event.Op(gtx.Ops, &a.scrim)
	full.Pop()

	gtx.Constraints.Min = image.Point{}
	m := op.Record(gtx.Ops)
	dims := content(gtx)
	call := m.Stop()
	x := (gtx.Constraints.Max.X - dims.Size.X) / 2
	y := (gtx.Constraints.Max.Y - dims.Size.Y) / 2
	defer offset(gtx.Ops, x, y).Pop()
	// absorve os cliques no corpo do pedal (para não fechar)
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &a.panelTag)
	area.Pop()
	call.Add(gtx.Ops)
	// botão de fechar
	func() {
		defer offset(gtx.Ops, dims.Size.X-gtx.Dp(34), gtx.Dp(8)).Pop()
		closeButton(gtx, &a.closeBtn, 26)
	}()
}
