// namcli mostra informações de um modelo .nam e processa um WAV por ele (offline).
//
// Serve para validar a inferência em Go contra a oficial (NeuralAmpModelerCore `render`):
// os dois recebem a mesma entrada a 48 kHz e as saídas são comparadas.
//
//	namcli -modelo NAM/captura.nam                                  # informações
//	namcli -modelo NAM/captura.nam -in di.wav -out saida.wav        # processa (tamanho completo)
//	namcli -modelo NAM/captura.nam -tamanho 0 -in di.wav -out lite.wav
//	namcli -in di.wav -salvar-entrada di48.wav                      # só converte (mono, taxa do modelo)
package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/nam"
	"github.com/tatianno/ampsim/wav"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		modelo  = flag.String("modelo", "", "arquivo .nam")
		in      = flag.String("in", "", "WAV de entrada (DI)")
		out     = flag.String("out", "", "WAV de saída (float32, na taxa do modelo)")
		tamanho = flag.Float64("tamanho", 1, "tamanho 0..1 (escolhe o submodelo de um SlimmableContainer)")
		bloco   = flag.Int("bloco", 64, "tamanho de bloco")
		ganho   = flag.Float64("ganho", 0, "ganho de entrada em dB")
		salvar  = flag.String("salvar-entrada", "", "grava a entrada já convertida (mono, taxa do modelo, float32)")
		taxa    = flag.Int("taxa", 48000, "taxa de saída quando não há modelo (com -salvar-entrada)")
	)
	flag.Parse()

	var m *nam.Model
	fs := *taxa
	if *modelo != "" {
		var err error
		if m, err = nam.Load(*modelo); err != nil {
			return err
		}
		info(m, *modelo)
		if m.SampleRate > 0 {
			fs = int(m.SampleRate)
		}
	}
	if *in == "" {
		if m == nil {
			flag.Usage()
			return errors.New("informe -modelo e/ou -in")
		}
		return nil
	}

	a, err := wav.ReadFile(*in)
	if err != nil {
		return err
	}
	x := a.Mono()
	if a.SampleRate != fs {
		fmt.Printf("reamostrando %d → %d Hz\n", a.SampleRate, fs)
		x = dsp.ResampleRate(x, a.SampleRate, fs)
	}
	if *ganho != 0 {
		g := math.Pow(10, *ganho/20)
		for i := range x {
			x[i] *= g
		}
	}
	x32 := make([]float32, len(x))
	for i, v := range x {
		x32[i] = float32(v)
	}
	if *salvar != "" {
		if err := writeF32(*salvar, fs, x32); err != nil {
			return err
		}
		fmt.Println("entrada gravada em", *salvar)
	}
	if m == nil || *out == "" {
		return nil
	}

	idx := m.SubmodelFor(*tamanho)
	net := nam.NewNet(m.Submodels[idx].Spec, *bloco)
	t0 := time.Now()
	net.Prewarm()
	tw := time.Since(t0)
	y := make([]float32, len(x32))
	t0 = time.Now()
	for i := 0; i < len(x32); i += *bloco {
		j := min(i+*bloco, len(x32))
		net.Process(x32[i:j], y[i:j])
	}
	el := time.Since(t0)
	dur := float64(len(x32)) / float64(fs)
	fmt.Printf("submodelo %d: %.2f s de áudio em %v (%.1f%% do tempo real; prewarm %v)\n",
		idx, dur, el.Round(time.Millisecond), 100*el.Seconds()/dur, tw.Round(time.Millisecond))
	if err := writeF32(*out, fs, y); err != nil {
		return err
	}
	fmt.Println("saída gravada em", *out)
	return nil
}

func info(m *nam.Model, path string) {
	md := m.Metadata
	fmt.Printf("%s\n  arquitetura %s (versão %s), %g Hz\n", filepath.Base(path), m.Architecture, m.Version, m.SampleRate)
	if md.Name != "" {
		fmt.Printf("  nome: %s\n", md.Name)
	}
	if md.GearMake != "" || md.GearModel != "" {
		fmt.Printf("  equipamento: %s (%s, %s)\n", md.GearModel, md.GearType, md.ToneType)
	}
	if md.ModeledBy != "" {
		fmt.Printf("  capturado por: %s\n", md.ModeledBy)
	}
	opt := func(name string, v *float64, unit string) {
		if v != nil {
			fmt.Printf("  %s: %.2f%s\n", name, *v, unit)
		}
	}
	opt("loudness", md.Loudness, " dB")
	opt("gain", md.Gain, "")
	opt("nível de entrada", md.InputLevelDBu, " dBu = 0 dBFS")
	opt("nível de saída", md.OutputLevelDBu, " dBu = 0 dBFS")
	for i, sm := range m.Submodels {
		s := sm.Spec
		fmt.Printf("  [%d] tamanho < %.2f: %d canais, %d camadas, %d pesos, campo receptivo %d amostras, ~%d mult./amostra\n",
			i, sm.MaxValue, s.Channels, s.Layers, s.Weights, s.ReceptiveField(), s.MACs())
	}
}

func writeF32(path string, fs int, x []float32) error {
	y := make([]float64, len(x))
	for i, v := range x {
		y[i] = float64(v)
	}
	return wav.WriteMonoFile(path, fs, y, wav.Float32)
}
