// Comando ref gera a referência do check.sh: o render offline da app (engine.RenderOffline,
// o mesmo do "Exportar…") de um arquivo float32 cru, com um preset, na taxa dada.
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
	y, err := engine.RenderOffline(st, x, fs, 0, 512, nil)
	if err != nil {
		log.Fatal(err)
	}
	out := make([]byte, 4*len(y))
	for i, v := range y {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(float32(v)))
	}
	if err := os.WriteFile(flag.Arg(2), out, 0o644); err != nil {
		log.Fatal(err)
	}
}
