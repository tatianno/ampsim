// Comando ref gera a referência do check.sh: o render offline da app (engine.RenderOffline,
// o mesmo do "Exportar…") de um arquivo float32 cru, com um preset, na taxa dada (com
// -estereo, o canal L do render estéreo).
package main

import (
	"encoding/binary"
	"flag"
	"log"
	"math"
	"os"
	"strconv"

	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
)

func main() {
	preset := flag.String("preset", "Clean", "nome do preset")
	stereo := flag.Bool("estereo", false, "render estéreo (grava o canal L)")
	fixed := flag.Bool("fixa", false, "latência fixa (Chain.PadLatency), como o plugin CLAP")
	flag.Parse()
	if flag.NArg() != 3 {
		log.Fatal("uso: ref -preset NOME taxa entrada.f32 saida.f32")
	}
	fs, err := strconv.ParseFloat(flag.Arg(0), 64)
	if err != nil {
		log.Fatal(err)
	}
	st, err := presets.Resolve(*preset)
	if err != nil {
		log.Fatal(err)
	}
	raw, err := os.ReadFile(flag.Arg(1))
	if err != nil {
		log.Fatal(err)
	}
	x := make([]float64, len(raw)/4)
	for i := range x {
		x[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:])))
	}
	var y []float64
	if *fixed {
		y = renderFixed(st, x, fs, *stereo)
	} else {
		ys, err := engine.RenderOfflineChannels(st, x, fs, 0, 512, engine.Output{Stereo: *stereo}, nil)
		if err != nil {
			log.Fatal(err)
		}
		y = ys[0]
	}
	out := make([]byte, 4*len(y))
	for i, v := range y {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(float32(v)))
	}
	if err := os.WriteFile(flag.Arg(2), out, 0o644); err != nil {
		log.Fatal(err)
	}
}

// renderFixed é o RenderOffline com a cadeia de latência fixa (os módulos com latência
// desligados na cadeia, como no plugin CLAP): devolve o canal L, sem a latência.
func renderFixed(st engine.State, x []float64, fs float64, stereo bool) []float64 {
	c := engine.NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		log.Fatal(err)
	}
	if err := c.PadLatency(); err != nil {
		log.Fatal(err)
	}
	c.SetStereo(stereo)
	c.Prepare(fs, 512)
	lat := c.Latency()
	l := append(append([]float64(nil), x...), make([]float64, lat)...)
	r := make([]float64, len(l))
	for i := 0; i < len(l); i += 512 {
		j := min(i+512, len(l))
		if stereo {
			c.ProcessStereo(l[i:j], r[i:j])
		} else {
			c.ProcessMono(l[i:j])
		}
	}
	return l[lat:]
}
