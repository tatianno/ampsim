//go:build !nogui

package main

import (
	"os"

	gioapp "gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/cmd/ampapp/ui"
	"github.com/tatianno/ampsim/engine"
)

// runGUI abre a janela. Não retorna: app.Main assume a goroutine principal e o
// processo termina quando a janela fecha.
func runGUI(chain *engine.Chain, out *audio.Output, player *source.FilePlayer, mon *audio.Monitor, cleanup func()) error {
	a := ui.NewApp(chain, player, out.Renderer(), mon, ui.Info{
		Device:     out.Name(),
		SampleRate: out.SampleRate(),
		Buffer:     int(out.Buffer()),
		LatencyMs:  out.OutputLatencyMs(chain.Latency()),
	})
	// AMPSIM_ABRIR=<pedal> abre um pedal ao iniciar (útil para conferir o visual / capturas de tela).
	if id := os.Getenv("AMPSIM_ABRIR"); id != "" {
		a.Open(id)
	}
	go func() {
		w := new(gioapp.Window)
		w.Option(gioapp.Title("ampsim"), gioapp.Size(unit.Dp(1280), unit.Dp(760)), gioapp.MinSize(unit.Dp(960), unit.Dp(640)))
		a.Invalidate = w.Invalidate
		var ops op.Ops
		code := 0
		for {
			switch e := w.Event().(type) {
			case gioapp.DestroyEvent:
				if e.Err != nil {
					os.Stderr.WriteString("erro: " + e.Err.Error() + "\n")
					code = 1
				}
				cleanup()
				os.Exit(code)
			case gioapp.FrameEvent:
				gtx := gioapp.NewContext(&ops, e)
				a.Layout(gtx)
				e.Frame(gtx.Ops)
			}
		}
	}()
	gioapp.Main()
	return nil
}
