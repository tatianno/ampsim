//go:build !nogui

package main

import (
	"os"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"

	"ampsim/cmd/ampapp/audio"
	"ampsim/cmd/ampapp/source"
	"ampsim/cmd/ampapp/ui"
	"ampsim/engine"
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
	go func() {
		w := new(app.Window)
		w.Option(app.Title("ampsim"), app.Size(unit.Dp(1280), unit.Dp(760)), app.MinSize(unit.Dp(960), unit.Dp(640)))
		a.Invalidate = w.Invalidate
		var ops op.Ops
		code := 0
		for {
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				if e.Err != nil {
					os.Stderr.WriteString("erro: " + e.Err.Error() + "\n")
					code = 1
				}
				cleanup()
				os.Exit(code)
			case app.FrameEvent:
				gtx := app.NewContext(&ops, e)
				a.Layout(gtx)
				e.Frame(gtx.Ops)
			}
		}
	}()
	app.Main()
	return nil
}
