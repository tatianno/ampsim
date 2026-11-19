// hgfit ajusta o pré High Gain (dsp.HighGain + tone stack TMB) contra uma captura NAM de
// referência, comparando as mesmas medidas dos dois lados:
//
//   - espectro por 1/3 de oitava (80 Hz a 8 kHz, só a forma) com a DI e com ruído branco em
//     −30 e −50 dBFS
//   - distorção (THD) de senos em 82, 110, 220, 440 e 1760 Hz, de −72 a −32 dBFS
//   - compressão: nível da fundamental em −40, −20 e 0 dBFS (relativo ao de 0 dBFS)
//
// Uso: go run ./cmd/hgfit -ref NAM/captura.nam -di testdata/guitarra_di.wav [-iter 20]
//
// Grava os parâmetros em -saida (JSON) a cada rodada; -inicio retoma de um JSON. Os valores
// finais vão à mão para dsp.HighGainDefault e dsp.TMBHighGain (o ganho G1 do ajuste é o do
// knob em 8). A captura de referência usada (um 5150III de terceiros) não fica no repositório.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/cmplx"
	"math/rand"
	"os"
	"sort"
	"sync"

	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/nam"
	"github.com/tatianno/ampsim/wav"
)

const fs = 48000

// ---------------------------------------------------------------- sinais e medidas

type signals struct {
	di, n30, n50 []float64
	sines        [][]float64 // thdFreqs × thdLevels
	comp         [][]float64 // compFreqs × compLevels
}

var (
	thdFreqs   = []float64{82, 110, 220, 440, 1760}
	thdLevels  = []float64{-72, -64, -56, -48, -40, -32}
	compFreqs  = []float64{82, 220, 880, 3520}
	compLevels = []float64{-40, -20, 0}
	bands      = func() (b []float64) {
		for f := 80.0; f <= 8100; f *= math.Pow(2, 1.0/3) {
			b = append(b, f)
		}
		return
	}()
)

const sineLen = 7200 // 150 ms

func sine(f, db float64) []float64 {
	x := make([]float64, sineLen)
	a := math.Pow(10, db/20)
	for i := range x {
		x[i] = a * math.Sin(2*math.Pi*f*float64(i)/fs)
	}
	return x
}

func makeSignals(di []float64) *signals {
	s := &signals{di: di}
	r := rand.New(rand.NewSource(1))
	n := make([]float64, fs*2)
	for i := range n {
		n[i] = r.NormFloat64()
	}
	s.n30, s.n50 = scale(n, -30), scale(n, -50)
	for _, f := range thdFreqs {
		for _, l := range thdLevels {
			s.sines = append(s.sines, sine(f, l))
		}
	}
	for _, f := range compFreqs {
		for _, l := range compLevels {
			s.comp = append(s.comp, sine(f, l))
		}
	}
	return s
}

func scale(x []float64, db float64) []float64 {
	g := math.Pow(10, db/20)
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = v * g
	}
	return out
}

// metrics são as medidas de uma cadeia.
type metrics struct {
	SpecDI, SpecN30, SpecN50 []float64 // dB por banda, média zero
	THD                      []float64 // log10(THD), thdFreqs × thdLevels
	Comp                     []float64 // dB relativo ao nível de 0 dBFS, compFreqs × compLevels
}

func spectrum(y []float64) []float64 {
	y = y[len(y)/8:]
	n := 1
	for n < len(y) {
		n <<= 1
	}
	buf := make([]complex128, n)
	for i, v := range y {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(y)))
		buf[i] = complex(v*w, 0)
	}
	fft(buf)
	out := make([]float64, len(bands))
	mean := 0.0
	for k, c := range bands {
		lo, hi := c/math.Pow(2, 1.0/6), c*math.Pow(2, 1.0/6)
		e := 1e-30
		for i := int(lo * float64(n) / fs); i <= int(hi*float64(n)/fs) && i < n/2; i++ {
			e += real(buf[i])*real(buf[i]) + imag(buf[i])*imag(buf[i])
		}
		out[k] = 10 * math.Log10(e)
		mean += out[k]
	}
	mean /= float64(len(bands))
	for k := range out {
		out[k] -= mean
	}
	return out
}

func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(l)))
		for i := 0; i < n; i += l {
			wk := complex(1, 0)
			for k := 0; k < l/2; k++ {
				u, v := a[i+k], a[i+k+l/2]*wk
				a[i+k], a[i+k+l/2] = u+v, u-v
				wk *= w
			}
		}
	}
}

// harm devolve a amplitude dos harmônicos 1..n de f no trecho final de y (DFT direta).
func harm(y []float64, f float64, n int) []float64 {
	y = y[len(y)/3:]
	out := make([]float64, n)
	for h := 1; h <= n; h++ {
		fh := f * float64(h)
		if fh > fs/2-1000 {
			break
		}
		var re, im float64
		for i, v := range y {
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(y)))
			ph := 2 * math.Pi * fh * float64(i) / fs
			re += v * w * math.Cos(ph)
			im += v * w * math.Sin(ph)
		}
		out[h-1] = math.Hypot(re, im) * 4 / float64(len(y))
	}
	return out
}

func measure(s *signals, run func([]float64) []float64) *metrics {
	m := &metrics{
		SpecDI:  spectrum(run(s.di)),
		SpecN30: spectrum(run(s.n30)),
		SpecN50: spectrum(run(s.n50)),
	}
	for k, x := range s.sines {
		h := harm(run(x), thdFreqs[k/len(thdLevels)], 8)
		e := 0.0
		for _, a := range h[1:] {
			e += a * a
		}
		m.THD = append(m.THD, math.Log10(math.Sqrt(e)/(h[0]+1e-12)+1e-4))
	}
	for i := range compFreqs {
		var lv []float64
		for j := range compLevels {
			h := harm(run(s.comp[i*len(compLevels)+j]), compFreqs[i], 1)
			lv = append(lv, 20*math.Log10(h[0]+1e-12))
		}
		for _, v := range lv {
			m.Comp = append(m.Comp, v-lv[len(lv)-1])
		}
	}
	return m
}

func dist(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += math.Abs(a[i] - b[i])
	}
	return s / float64(len(a))
}

// cost compara as medidas: dB médio nos espectros, décadas de THD (×10) e dB de compressão.
func cost(ref, m *metrics) (total float64, parts [5]float64) {
	parts = [5]float64{
		dist(ref.SpecDI, m.SpecDI), dist(ref.SpecN30, m.SpecN30), dist(ref.SpecN50, m.SpecN50),
		10 * dist(ref.THD, m.THD), dist(ref.Comp, m.Comp),
	}
	total = 2*parts[0] + parts[1] + 0.5*parts[2] + 0.5*parts[3] + 0.5*parts[4]
	return
}

// ---------------------------------------------------------------- cadeias

func runNAM(model *nam.Model) func([]float64) []float64 {
	return func(x []float64) []float64 {
		net := model.NewNet(1, 4096)
		in := make([]float32, len(x))
		for i, v := range x {
			in[i] = float32(v)
		}
		out := make([]float32, len(x))
		for i := 0; i < len(x); i += 4096 {
			j := min(i+4096, len(x))
			net.Process(in[i:j], out[i:j])
		}
		y := make([]float64, len(x))
		for i, v := range out {
			y[i] = float64(v)
		}
		return y
	}
}

// fitParams são os parâmetros ajustados: os do pré, o ganho do 1º estágio no knob usado e o
// tone stack.
type fitParams struct {
	HG     dsp.HGParams
	G1     float64 // ganho do 1º estágio (o knob do ajuste)
	TMB    dsp.TMBModel
	Bass   float64 // posição do pot de grave (0..1); o knob sai da curva do modelo
	Mid    float64
	Treble float64
}

func runOurs(p fitParams) func([]float64) []float64 {
	return func(x []float64) []float64 {
		h := dsp.NewHighGain(fs)
		hp := p.HG
		hp.G1Min, hp.G1Max = p.G1, p.G1 // knob fixo
		h.SetParams(hp)
		h.SetDrive(1)
		st := dsp.NewTMBStack(fs, p.TMB)
		st.Set(p.TMB.BassKnob(p.Bass), p.Mid, p.Treble)
		pr := dsp.NewPresence(fs)
		pr.Set(5)
		y := append([]float64(nil), x...)
		for i := 0; i < len(y); i += 256 {
			b := y[i:min(i+256, len(y))]
			h.ProcessBlock(b)
			st.ProcessBlock(b)
			for k, v := range b {
				b[k] = math.Tanh(v)
			}
			pr.ProcessBlock(b)
		}
		return y
	}
}

// ---------------------------------------------------------------- otimização

// knob é um parâmetro ajustável em escala log (todos positivos) ou linear.
type knob struct {
	name     string
	get      func(*fitParams) *float64
	min, max float64
	log      bool
}

var knobs = []knob{
	{"TightHz", func(p *fitParams) *float64 { return &p.HG.TightHz }, 20, 1500, true},
	{"BrightHz", func(p *fitParams) *float64 { return &p.HG.BrightHz }, 500, 8000, true},
	{"BrightDB", func(p *fitParams) *float64 { return &p.HG.BrightDB }, -6, 15, false},
	{"HP1Hz", func(p *fitParams) *float64 { return &p.HG.HP1Hz }, 10, 1500, true},
	{"HP2Hz", func(p *fitParams) *float64 { return &p.HG.HP2Hz }, 10, 1500, true},
	{"LP1Hz", func(p *fitParams) *float64 { return &p.HG.LP1Hz }, 1500, 30000, true},
	{"LP2Hz", func(p *fitParams) *float64 { return &p.HG.LP2Hz }, 1500, 30000, true},
	{"LP3Hz", func(p *fitParams) *float64 { return &p.HG.LP3Hz }, 1500, 30000, true},
	{"G1", func(p *fitParams) *float64 { return &p.G1 }, 1, 300, true},
	{"G2", func(p *fitParams) *float64 { return &p.HG.G2 }, 1, 300, true},
	{"G3", func(p *fitParams) *float64 { return &p.HG.G3 }, 1, 100, true},
	{"Bias", func(p *fitParams) *float64 { return &p.HG.Bias }, 0, 1.2, false},
	// Out fixo: acima de ~0,25 o power amp satura e o ajuste piora; abaixo, o nível cai
	{"Out", func(p *fitParams) *float64 { return &p.HG.Out }, 0.25, 0.25, true},
	{"R4", func(p *fitParams) *float64 { return &p.TMB.R4 }, 3e3, 200e3, true},
	{"C1", func(p *fitParams) *float64 { return &p.TMB.C1 }, 100e-12, 2e-9, true},
	// C2 até 47 nF: acima disso o grave satura no meio do knob (a metade de cima fica morta)
	{"C2", func(p *fitParams) *float64 { return &p.TMB.C2 }, 2e-9, 47e-9, true},
	{"C3", func(p *fitParams) *float64 { return &p.TMB.C3 }, 2e-9, 200e-9, true},
}

func step(k knob, v, s float64) float64 {
	if k.log {
		v *= math.Pow(2, s)
	} else {
		v += s * (k.max - k.min) / 8
	}
	return math.Max(k.min, math.Min(k.max, v))
}

func main() {
	refPath := flag.String("ref", "", "captura NAM de referência")
	diPath := flag.String("di", "testdata/guitarra_di.wav", "DI de teste")
	iters := flag.Int("iter", 12, "rodadas de busca por coordenada")
	start := flag.String("inicio", "", "JSON com fitParams para começar (saída de uma rodada anterior)")
	outPath := flag.String("saida", "hgfit.json", "onde gravar o resultado")
	flag.Parse()

	model, err := nam.Load(*refPath)
	if err != nil {
		fail(err)
	}
	sr, di, err := wav.ReadMonoFile(*diPath)
	if err != nil {
		fail(err)
	}
	s := makeSignals(dsp.ResampleRate(di, sr, fs))
	ref := measure(s, runNAM(model))

	p := fitParams{HG: dsp.HighGainDefault, G1: 20, TMB: dsp.TMBMarshall, Bass: dsp.LegacyBassPot, Mid: 5, Treble: 5}
	p.TMB.Name = "HighGain"
	if *start != "" {
		b, err := os.ReadFile(*start)
		if err != nil {
			fail(err)
		}
		if err := json.Unmarshal(b, &p); err != nil {
			fail(err)
		}
	}
	eval := func(p fitParams) float64 { c, _ := cost(ref, measure(s, runOurs(p))); return c }
	best := eval(p)
	report(ref, s, p, "início")

	sizes := []float64{1, 0.5, 0.25}
	for it := 0; it < *iters; it++ {
		sz := sizes[min(it/4, len(sizes)-1)]
		improved := false
		for _, k := range knobs {
			// as duas direções em paralelo
			cands := []fitParams{p, p}
			*k.get(&cands[0]) = step(k, *k.get(&p), sz)
			*k.get(&cands[1]) = step(k, *k.get(&p), -sz)
			costs := make([]float64, 2)
			var wg sync.WaitGroup
			for i := range cands {
				wg.Add(1)
				go func() { defer wg.Done(); costs[i] = eval(cands[i]) }()
			}
			wg.Wait()
			for i, c := range costs {
				if c < best-1e-4 {
					best, p, improved = c, cands[i], true
				}
			}
		}
		fmt.Printf("rodada %d (passo %.2f oit.): custo %.3f\n", it+1, sz, best)
		save(*outPath, p)
		if !improved && sz == sizes[len(sizes)-1] {
			break
		}
	}
	report(ref, s, p, "final")
}

func report(ref *metrics, s *signals, p fitParams, title string) {
	m := measure(s, runOurs(p))
	c, parts := cost(ref, m)
	fmt.Printf("\n== %s: custo %.3f (DI %.2f dB, ruído −30 %.2f dB, ruído −50 %.2f dB, THD %.2f, compressão %.2f dB)\n",
		title, c, parts[0], parts[1], parts[2], parts[3]/10, parts[4])
	fmt.Print("banda   ")
	for _, b := range bands {
		fmt.Printf("%6.0f", b)
	}
	fmt.Println()
	row := func(name string, v []float64) {
		fmt.Printf("%-8s", name)
		for _, x := range v {
			fmt.Printf("%6.1f", x)
		}
		fmt.Println()
	}
	row("ref DI", ref.SpecDI)
	row("nós DI", m.SpecDI)
	row("ref −30", ref.SpecN30)
	row("nós −30", m.SpecN30)
	fmt.Println("THD (%) ref / nós, níveis", thdLevels)
	for i, f := range thdFreqs {
		fmt.Printf("%6.0f Hz:", f)
		for j := range thdLevels {
			k := i*len(thdLevels) + j
			fmt.Printf(" %5.1f/%-5.1f", 100*math.Pow(10, ref.THD[k]), 100*math.Pow(10, m.THD[k]))
		}
		fmt.Println()
	}
	fmt.Println("compressão (dB rel. 0 dBFS) ref / nós, níveis", compLevels)
	for i, f := range compFreqs {
		fmt.Printf("%6.0f Hz:", f)
		for j := range compLevels {
			k := i*len(compLevels) + j
			fmt.Printf(" %5.1f/%-5.1f", ref.Comp[k], m.Comp[k])
		}
		fmt.Println()
	}
	keys := make([]string, 0, len(knobs))
	for _, k := range knobs {
		keys = append(keys, fmt.Sprintf("%s=%.4g", k.name, *k.get(&p)))
	}
	sort.Strings(keys)
	fmt.Println(keys)
}

func save(path string, p fitParams) {
	b, _ := json.MarshalIndent(p, "", "  ")
	_ = os.WriteFile(path, b, 0o644)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
