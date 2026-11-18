// ampcli processa um arquivo WAV pela cadeia do ampsim (offline).
//
// Exemplos:
//
//	ampcli -in testdata/guitarra_di.wav -out saida/amp.wav
//	ampcli -in di.wav -out out.wav -on chorus,delay -set pre.gain=8,delay.time=420
//	ampcli -in di.wav -out out.wav -on delay -mover delay:depois:cab   # delay depois do gabinete
//	ampcli -in di.wav -out out.wav -ordem gate,chorus,pre,flanger,phaser,delay,reverb,power,cab
//	ampcli -in di.wav -out out.wav -preset clean.json
//	ampcli -listar
//	ampcli -salvar-preset meu.json -set pre.gain=3
package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
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
		in        = flag.String("in", "", "WAV de entrada (DI da guitarra)")
		out       = flag.String("out", "", "WAV de saída")
		ir        = flag.String("ir", "testdata/gabinete_2x12_sm57.wav", "WAV da IR do gabinete (vazio = sem gabinete)")
		preset    = flag.String("preset", "", "preset para carregar: arquivo .json ou nome (ex.: Lead)")
		savePre   = flag.String("salvar-preset", "", "grava o estado final (após -preset/-on/-off/-set) neste JSON")
		on        = flag.String("on", "", "módulos para ligar, separados por vírgula (ex.: chorus,delay)")
		off       = flag.String("off", "", "módulos para desligar, separados por vírgula")
		set       = flag.String("set", "", "parâmetros modulo.param=valor, separados por vírgula")
		tail      = flag.Float64("cauda", 2, "segundos de silêncio no fim para delay/reverb terminarem")
		block     = flag.Int("bloco", 256, "tamanho de bloco (simula o buffer da placa)")
		format    = flag.String("formato", "pcm16", "formato de saída: pcm16 ou float32")
		normalize = flag.Float64("normalizar", 0, "se > 0, normaliza o pico para este valor em dBFS negativo (ex.: 1 → -1 dBFS)")
		list      = flag.Bool("listar", false, "lista módulos e parâmetros e sai")
		ordem     = flag.String("ordem", "", "ordem completa da cadeia, separada por vírgula")
		mover     = flag.String("mover", "", "move módulos: id:antes|depois:ref, separados por vírgula (ex.: delay:depois:cab)")
	)
	flag.Parse()

	chain := engine.NewDefaultChain()
	if *list {
		listar(chain)
		return nil
	}
	if *ir != "" {
		if err := chain.Cab().LoadIRFile(*ir); err != nil {
			return err
		}
	}
	if *preset != "" {
		st, err := presets.Resolve(*preset)
		if err != nil {
			return err
		}
		if err := chain.ApplyState(st); err != nil {
			return err
		}
	}
	if err := aplicarFlags(chain, *on, *off, *set); err != nil {
		return err
	}
	if err := aplicarOrdem(chain, *ordem, *mover); err != nil {
		return err
	}
	if *savePre != "" {
		if err := chain.SaveState(*savePre); err != nil {
			return err
		}
		fmt.Println("preset gravado em", *savePre)
		if *in == "" {
			return nil
		}
	}

	if *in == "" || *out == "" {
		flag.Usage()
		return errors.New("informe -in e -out")
	}
	var wf wav.Format
	switch *format {
	case "pcm16":
		wf = wav.PCM16
	case "float32":
		wf = wav.Float32
	default:
		return fmt.Errorf("formato inválido: %q", *format)
	}

	fs, x, err := wav.ReadMonoFile(*in)
	if err != nil {
		return err
	}
	chain.Prepare(float64(fs), *block)
	lat := chain.Latency()

	// entrada + cauda + latência (para compensar o atraso no fim)
	n := len(x) + int(*tail*float64(fs))
	buf := make([]float64, n+lat)
	copy(buf, x)

	start := time.Now()
	for i := 0; i < len(buf); i += *block {
		chain.ProcessMono(buf[i:min(i+*block, len(buf))])
	}
	elapsed := time.Since(start)
	y := buf[lat:] // compensa a latência: saída alinhada com a entrada

	pk := 0.0
	for _, v := range y {
		pk = math.Max(pk, math.Abs(v))
	}
	if *normalize > 0 && pk > 0 {
		g := math.Pow(10, -*normalize/20) / pk
		for i := range y {
			y[i] *= g
		}
		pk *= g
	}

	if dir := filepath.Dir(*out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := wav.WriteMonoFile(*out, fs, y, wf); err != nil {
		return err
	}

	dur := float64(len(y)) / float64(fs)
	fmt.Printf("%s → %s\n", *in, *out)
	fmt.Printf("  %d Hz, %.2f s, latência %d amostras (%.2f ms, compensada)\n",
		fs, dur, lat, 1000*float64(lat)/float64(fs))
	fmt.Printf("  processado em %v (%.0fx tempo real)\n", elapsed.Round(time.Millisecond), dur/elapsed.Seconds())
	fmt.Printf("  cadeia: %s\n", chain)
	fmt.Printf("  pico: %.1f dBFS\n", 20*math.Log10(pk+1e-12))
	if pk > 1 && wf == wav.PCM16 {
		fmt.Println("  AVISO: pico acima de 0 dBFS, vai clipar em PCM16 (baixe power.volume ou use -normalizar 1)")
	}
	return nil
}

func aplicarFlags(c *engine.Chain, on, off, set string) error {
	if err := c.EnableList(on, true); err != nil {
		return err
	}
	if err := c.EnableList(off, false); err != nil {
		return err
	}
	if err := c.SetList(set); err != nil {
		return fmt.Errorf("-set: %w", err)
	}
	return nil
}

func aplicarOrdem(c *engine.Chain, ordem, mover string) error {
	if ordem != "" {
		if err := c.SetOrder(engine.SplitList(ordem)); err != nil {
			return fmt.Errorf("-ordem: %w", err)
		}
	}
	for _, mv := range engine.SplitList(mover) {
		p := strings.Split(mv, ":")
		if len(p) != 3 {
			return fmt.Errorf("-mover: use id:antes|depois:ref, veio %q", mv)
		}
		var err error
		switch p[1] {
		case "antes":
			err = c.MoveBefore(p[0], p[2])
		case "depois":
			err = c.MoveAfter(p[0], p[2])
		default:
			err = fmt.Errorf("use antes ou depois, veio %q", p[1])
		}
		if err != nil {
			return fmt.Errorf("-mover %s: %w", mv, err)
		}
	}
	return nil
}

func listar(c *engine.Chain) {
	fmt.Println("cadeia:", c)
	fmt.Println()
	for _, m := range c.Modules() {
		estado := "desligado"
		if c.Enabled(m.ID()) {
			estado = "ligado"
		}
		fmt.Printf("%s (%s) — %s\n", m.ID(), m.Name(), estado)
		for _, p := range m.Params() {
			in := p.Info()
			fmt.Printf("  %-10s %-10s %8.2f  [%g .. %g] %s\n", in.ID, in.Name, p.Get(), in.Min, in.Max, in.Unit)
		}
	}
}
