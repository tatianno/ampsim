// ampcli processa um arquivo WAV pela cadeia do ampsim (offline).
//
// Exemplos:
//
//	ampcli -in testdata/guitarra_di.wav -out saida/amp.wav
//	ampcli -in di.wav -out out.wav -on chorus,delay -set pre.gain=8,delay.time=420
//	ampcli -in di.wav -out out.wav -on delay -mover delay:depois:cab   # delay depois do gabinete
//	ampcli -in di.wav -out out.wav -ordem gate,chorus,pre,flanger,phaser,delay,reverb,power,cab
//	ampcli -in di.wav -out out.wav -preset clean.json
//	ampcli -in di.wav -out out.wav -nam "Clean V10 B5 M5 T5 P5"          # amp NAM (captura incluída)
//	ampcli -in di.wav -out out.wav -nam captura.nam -set pre.nam_size=1  # arquivo .nam, modelo completo
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

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
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
		ir        = flag.String("ir", "", "WAV da IR do gabinete (vazio = a interna, 2x12 SM57)")
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
		namModel  = flag.String("nam", "", "liga o amp NAM com este modelo: arquivo .nam ou nome de uma captura incluída (veja -listar)")
	)
	flag.Parse()

	chain := engine.NewDefaultChain()
	if *list {
		listar(chain)
		return nil
	}
	if *ir != "" { // sem IR: o gabinete usa a IR interna (2x12 SM57)
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
	if *namModel != "" {
		ref := *namModel
		if _, err := os.Stat(ref); err != nil {
			ref = capturas.Prefix + ref // não é arquivo: captura incluída
		}
		if err := chain.Pre().LoadNAM(ref); err != nil {
			return err
		}
		chain.Pre().Param("amp").Set(engine.AmpNAM)
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
	// os modelos NAM foram treinados a 48 kHz: no modo NAM, processa (e grava) a 48 kHz
	if chain.Pre().Mode() == engine.AmpNAM && fs != namRate {
		fmt.Printf("modo NAM: reamostrando %d → %d Hz\n", fs, namRate)
		x = dsp.ResampleRate(x, fs, namRate)
		fs = namRate
	}
	// mesmas rampas de borda do app (um corte abrupto no fim da gravação vira estalo no amp)
	dsp.FadeEdges(x, float64(fs), dsp.FileFadeInMs, dsp.FileFadeOutMs)
	lat := chain.Latency()
	start := time.Now()
	// mesma renderização offline da GUI ("Exportar…"): cadeia nova com o estado atual
	y, err := engine.RenderOffline(chain.State(), x, float64(fs), *tail, *block, nil)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

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

// namRate é a taxa em que os modelos NAM são treinados.
const namRate = 48000

func listar(c *engine.Chain) {
	fmt.Println("cadeia:", c)
	fmt.Println()
	fmt.Println("capturas NAM incluídas (-nam):")
	for _, n := range capturas.Names() {
		fmt.Println("  " + n)
	}
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
