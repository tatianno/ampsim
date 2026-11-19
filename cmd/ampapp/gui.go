//go:build !nogui

package main

import (
	"fmt"
	"os"

	gioapp "gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/ui"
	"github.com/tatianno/ampsim/plugin"
	"github.com/tatianno/ampsim/plugin/lv2/ttl"
	"github.com/tatianno/ampsim/presets"
)

// runGUI abre a janela. Não retorna: gioapp.Main assume a goroutine principal e o
// processo termina quando a janela fecha.
func runGUI(s *audio.Session, cleanup func(), version, aviso string) error {
	a := ui.NewApp(s)
	a.SetVersion(version)
	if aviso != "" {
		a.SetStatus(aviso, true)
	}
	// plugin LV2: um plugin por preset; a lista no ~/.lv2/ampsim.lv2 acompanha os presets
	a.OnPresetsChanged(func(list []presets.Preset) {
		go func() {
			if err := ttl.Sync(plugin.FromList(list)); err != nil {
				fmt.Fprintln(os.Stderr, "plugin LV2:", err)
			}
		}()
	})
	// AMPSIM_ABRIR=<pedal> abre um pedal ao iniciar (útil para conferir o visual / capturas de tela).
	if id := os.Getenv("AMPSIM_ABRIR"); id != "" {
		a.Open(id)
	}
	if os.Getenv("AMPSIM_PEDALEIRA_EDITAR") != "" { // também para conferir o visual
		a.Pedalboard().OpenEditor()
	}
	go func() {
		w := new(gioapp.Window)
		w.Option(gioapp.Title("AmpSim "+version), gioapp.Size(unit.Dp(1280), unit.Dp(760)), gioapp.MinSize(unit.Dp(960), unit.Dp(640)))
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
