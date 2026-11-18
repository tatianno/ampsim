package nam

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tatianno/ampsim/wav"
)

// ---------------------------------------------------------------- modelos sintéticos

// synth descreve um WaveNet de teste; vira JSON com pesos aleatórios do tamanho certo.
type synth struct {
	arrays []map[string]any
	head   map[string]any
}

func (s synth) json(t *testing.T, rng *rand.Rand) []byte {
	t.Helper()
	cfg := map[string]any{"layers": s.arrays, "head_scale": 0.02}
	if s.head != nil {
		cfg["head"] = s.head
	}
	raw, _ := json.Marshal(cfg)
	spec, err := buildWaveNet(raw, &weightReader{w: make([]float32, 1<<20)})
	if err != nil {
		t.Fatal(err)
	}
	nw := spec.Weights
	w := make([]float32, nw)
	for i := range w {
		w[i] = float32(rng.NormFloat64() * 0.4)
	}
	w[nw-1] = 0.5 // head_scale
	f := map[string]any{"version": "0.7.0", "architecture": "WaveNet", "config": cfg, "weights": w, "sample_rate": 48000}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// a1 é o formato clássico (como o "standard", mas pequeno).
func a1() synth {
	return synth{arrays: []map[string]any{
		{"input_size": 1, "condition_size": 1, "channels": 4, "head_size": 2, "head_bias": false,
			"kernel_size": 3, "dilations": []int{1, 2, 4, 8}, "activation": "Tanh", "gated": false},
		{"input_size": 4, "condition_size": 1, "channels": 2, "head_size": 1, "head_bias": true,
			"kernel_size": 3, "dilations": []int{1, 3, 9}, "activation": "Tanh", "gated": false},
	}}
}

// a2 imita os arquivos 0.7 reais (LeakyReLU, bottleneck, kernels por camada, cabeça k=16).
func a2(ch, bn int) synth {
	acts := []any{}
	gm := []string{}
	ks := []int{6, 6, 15, 6}
	for range ks {
		acts = append(acts, map[string]any{"type": "LeakyReLU", "negative_slope": 0.01})
		gm = append(gm, "none")
	}
	return synth{arrays: []map[string]any{{
		"input_size": 1, "condition_size": 1, "channels": ch, "bottleneck": bn,
		"head":         map[string]any{"out_channels": 1, "kernel_size": 16, "bias": true},
		"kernel_sizes": ks, "dilations": []int{1, 7, 13, 41}, "activation": acts, "gating_mode": gm,
		"layer1x1":      map[string]any{"active": true, "groups": 1},
		"head1x1":       map[string]any{"active": false, "out_channels": 1, "groups": 1},
		"conv_pre_film": map[string]any{"active": false, "shift": true, "groups": 1},
	}}}
}

// mixed exercita gating, blending, head1x1, grupos, sem layer1x1 e a cabeça pós-pilhas.
func mixed() synth {
	return synth{
		arrays: []map[string]any{
			{"input_size": 1, "condition_size": 1, "channels": 4, "bottleneck": 2,
				"head":         map[string]any{"out_channels": 3, "kernel_size": 2, "bias": true},
				"kernel_sizes": []int{2, 3, 3}, "dilations": []int{1, 2, 5},
				"activation":           []any{"Tanh", map[string]any{"type": "PReLU", "negative_slopes": []float64{0.1, 0.3}}, "SiLU"},
				"gating_mode":          []string{"gated", "blended", "none"},
				"secondary_activation": []any{"Sigmoid", "Hardtanh", nil},
				"layer1x1":             map[string]any{"active": true, "groups": 2},
				"head1x1":              map[string]any{"active": true, "out_channels": 2, "groups": 2},
				"groups_input":         2},
			{"input_size": 4, "condition_size": 1, "channels": 3, "head_size": 2, "head_bias": true,
				"kernel_size": 2, "dilations": []int{1, 4}, "activation": "Softsign",
				"layer1x1": map[string]any{"active": false, "groups": 1}},
		},
		head: map[string]any{"channels": 3, "out_channels": 1, "kernel_sizes": []int{3, 2}, "activation": "ReLU"},
	}
}

// ---------------------------------------------------------------- referência direta

// direct calcula o WaveNet sobre o sinal inteiro, sem streaming (float64, passado = 0).
func direct(s *Spec, x []float64) []float64 {
	T := len(x)
	newSig := func(ch int) [][]float64 {
		m := make([][]float64, ch)
		for i := range m {
			m[i] = make([]float64, T)
		}
		return m
	}
	convAll := func(c *conv, in [][]float64) [][]float64 {
		out := newSig(c.out)
		for o := range c.out {
			for t := range T {
				acc := 0.0
				if c.bias != nil {
					acc = float64(c.bias[o])
				}
				for k := range c.k {
					tt := t - (c.k-1-k)*c.dil
					if tt < 0 {
						continue
					}
					for i := range c.in {
						acc += float64(c.at(k, o, i)) * in[i][tt]
					}
				}
				out[o][t] = acc
			}
		}
		return out
	}
	act := func(a activation, v []float64, ch int) {
		buf := make([]float32, len(v))
		for i := range v {
			buf[i] = float32(v[i])
		}
		a.apply(buf, ch)
		for i := range v {
			v[i] = float64(buf[i])
		}
	}
	cond := [][]float64{x}
	var layerIn, headPrev [][]float64 = cond, nil
	for ai := range s.arrays {
		a := &s.arrays[ai]
		cur := convAll(&a.rechannel, layerIn)
		head := newSig(a.headIn)
		if headPrev != nil {
			for c := range head {
				copy(head[c], headPrev[c])
			}
		}
		for li := range a.layers {
			l := &a.layers[li]
			z := convAll(&l.conv, cur)
			mix := convAll(&l.mixin, cond)
			for c := range z {
				for t := range T {
					z[c][t] += mix[c][t]
				}
			}
			bn := a.bottleneck
			for c := range bn {
				pre := append([]float64(nil), z[c]...)
				act(l.act, z[c], c)
				if l.gating != gateNone {
					act(l.act2, z[bn+c], c)
					for t := range T {
						g := z[bn+c][t]
						if l.gating == gateGated {
							z[c][t] *= g
						} else {
							z[c][t] = g*z[c][t] + (1-g)*pre[t]
						}
					}
				}
			}
			zz := z[:bn]
			if l.head1x1 != nil {
				h := convAll(l.head1x1, zz)
				for c := range h {
					for t := range T {
						head[c][t] += h[c][t]
					}
				}
			} else {
				for c := range bn {
					for t := range T {
						head[c][t] += zz[c][t]
					}
				}
			}
			next := newSig(a.channels)
			for c := range next {
				copy(next[c], cur[c])
			}
			if l.layer1x1 != nil {
				r := convAll(l.layer1x1, zz)
				for c := range next {
					for t := range T {
						next[c][t] += r[c][t]
					}
				}
			}
			cur = next
		}
		layerIn = cur
		headPrev = convAll(&a.headRechannel, head)
	}
	out := headPrev
	for c := range out {
		for t := range T {
			out[c][t] *= float64(s.headScale)
		}
	}
	if s.head != nil {
		for i := range s.head.convs {
			for c := range out {
				act(s.head.act, out[c], c)
			}
			out = convAll(&s.head.convs[i], out)
		}
	}
	return out[0]
}

func noise(rng *rand.Rand, n int, amp float64) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(amp * (2*rng.Float64() - 1))
	}
	return x
}

func maxErr(a []float32, b []float64) (float64, float64) {
	e, peak := 0.0, 0.0
	for i := range a {
		e = max(e, math.Abs(float64(a[i])-b[i]))
		peak = max(peak, math.Abs(b[i]))
	}
	return e, peak
}

func TestStreamingIgualADireta(t *testing.T) {
	for name, s := range map[string]synth{"a1": a1(), "a2": a2(3, 3), "a2-bottleneck": a2(4, 2), "misto": mixed()} {
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(1, 2))
			m, err := Parse(s.json(t, rng))
			if err != nil {
				t.Fatal(err)
			}
			spec := m.Submodels[0].Spec
			x := noise(rng, 3000, 0.8)
			x64 := make([]float64, len(x))
			for i, v := range x {
				x64[i] = float64(v)
			}
			want := direct(spec, x64)
			for _, block := range []int{1, 7, 64, 500, 4096} {
				net := NewNet(spec, block)
				got := make([]float32, len(x))
				net.Process(x, got)
				e, peak := maxErr(got, want)
				if peak == 0 || e > 1e-5*max(1, peak) {
					t.Errorf("bloco %d: erro máx %.3g (pico %.3g)", block, e, peak)
				}
			}
		})
	}
}

func TestPesosOrdemPyTorch(t *testing.T) {
	// conv 2→2, kernel 2: no arquivo a ordem é (saída, entrada, tap).
	r := &weightReader{w: []float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}}
	c, err := r.readConv(2, 2, 2, 1, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		k, o, i int
		w       float32
	}{{0, 0, 0, 1}, {1, 0, 0, 2}, {0, 0, 1, 3}, {1, 0, 1, 4}, {0, 1, 0, 5}, {1, 1, 1, 8}}
	for _, cs := range cases {
		if got := c.at(cs.k, cs.o, cs.i); got != cs.w {
			t.Errorf("w[k=%d][o=%d][i=%d] = %g, esperava %g", cs.k, cs.o, cs.i, got, cs.w)
		}
	}
	if c.bias[0] != 9 || c.bias[1] != 10 {
		t.Errorf("bias = %v", c.bias)
	}
	// agrupada 4→2 em 2 grupos: cada saída só vê as 2 entradas do seu grupo
	r = &weightReader{w: []float32{1, 2, 3, 4}}
	g, err := r.readConv(4, 2, 1, 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if g.at(0, 0, 0) != 1 || g.at(0, 0, 1) != 2 || g.at(0, 0, 2) != 0 || g.at(0, 1, 2) != 3 || g.at(0, 1, 3) != 4 || g.at(0, 1, 0) != 0 {
		t.Errorf("pesos agrupados: %v", g.w)
	}
}

func TestErrosDeValidacao(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	ok := a2(3, 3).json(t, rng)
	var f map[string]any
	_ = json.Unmarshal(ok, &f)

	mut := func(edit func(f map[string]any)) []byte {
		var g map[string]any
		_ = json.Unmarshal(ok, &g)
		edit(g)
		b, _ := json.Marshal(g)
		return b
	}
	layer := func(g map[string]any) map[string]any {
		return g["config"].(map[string]any)["layers"].([]any)[0].(map[string]any)
	}
	cases := map[string][]byte{
		"peso a mais":   mut(func(g map[string]any) { g["weights"] = append(g["weights"].([]any), 1.0) }),
		"peso a menos":  mut(func(g map[string]any) { w := g["weights"].([]any); g["weights"] = w[:len(w)-2] }),
		"LSTM":          mut(func(g map[string]any) { g["architecture"] = "LSTM" }),
		"FiLM ligado":   mut(func(g map[string]any) { layer(g)["conv_post_film"] = map[string]any{"active": true} }),
		"ativação":      mut(func(g map[string]any) { layer(g)["activation"] = "Foo" }),
		"kernels":       mut(func(g map[string]any) { layer(g)["kernel_sizes"] = []int{3} }),
		"não é .nam":    []byte(`{"foo": 1}`),
		"JSON quebrado": []byte(`{`),
	}
	for name, b := range cases {
		if _, err := Parse(b); err == nil {
			t.Errorf("%s: esperava erro", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
	if _, err := Parse(ok); err != nil {
		t.Fatal(err)
	}
}

func TestResetVoltaAoPrewarm(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	m, err := Parse(a2(3, 3).json(t, rng))
	if err != nil {
		t.Fatal(err)
	}
	net := m.NewNet(1, 128)
	x := noise(rng, 2000, 0.5)
	a := make([]float32, len(x))
	b := make([]float32, len(x))
	net.Process(x, a)
	net.Process(noise(rng, 999, 0.9), make([]float32, 999)) // suja o estado
	net.Reset()
	net.Process(x, b)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("depois do Reset a saída mudou na amostra %d: %g != %g", i, a[i], b[i])
		}
	}
	// Silêncio depois do prewarm: saída constante (o repouso da rede), sem transiente.
	net.Reset()
	z := make([]float32, 300)
	net.Process(z, z)
	for i := range z {
		if z[i] != z[0] {
			t.Fatalf("silêncio depois do prewarm não é constante: %g vs %g", z[i], z[0])
		}
	}
}

func TestSemAlocacao(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	m, err := Parse(mixed().json(t, rng))
	if err != nil {
		t.Fatal(err)
	}
	net := m.NewNet(1, 256)
	x := noise(rng, 256, 0.5)
	y := make([]float32, 256)
	if n := testing.AllocsPerRun(50, func() { net.Process(x, y); net.Reset() }); n != 0 {
		t.Errorf("Process/Reset alocaram %.0f vezes", n)
	}
}

// ---------------------------------------------------------------- arquivos reais

func namFiles(t testing.TB) []string {
	files, _ := filepath.Glob("../NAM/*.nam")
	if len(files) == 0 {
		t.Skip("sem arquivos .nam em ../NAM (ficam fora do git)")
	}
	return files
}

func TestArquivosReais(t *testing.T) {
	for _, f := range namFiles(t) {
		m, err := Load(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %s, %g Hz, %d submodelos", filepath.Base(f), m.Architecture, m.SampleRate, len(m.Submodels))
		for i, sm := range m.Submodels {
			s := sm.Spec
			t.Logf("  [%d] até %.2f: %d canais, %d camadas, %d pesos, campo receptivo %d, ~%d MAC/amostra",
				i, sm.MaxValue, s.Channels, s.Layers, s.Weights, s.ReceptiveField(), s.MACs())
		}
		if m.SampleRate != 48000 {
			t.Errorf("taxa %g", m.SampleRate)
		}
		if m.SubmodelFor(0.2) != 0 || m.SubmodelFor(0.5) != len(m.Submodels)-1 || m.SubmodelFor(1) != len(m.Submodels)-1 {
			t.Error("escolha do submodelo")
		}
		// Saída finita e não nula para um sinal de guitarra plausível.
		rng := rand.New(rand.NewPCG(9, 10))
		for i := range m.Submodels {
			net := NewNet(m.Submodels[i].Spec, 256)
			net.Prewarm()
			x := noise(rng, 48000, 0.3)
			net.Process(x, x)
			peak := 0.0
			for _, v := range x {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatal("saída não finita")
				}
				peak = max(peak, math.Abs(float64(v)))
			}
			if peak == 0 || peak > 10 {
				t.Errorf("[%d] pico estranho: %g", i, peak)
			}
		}
	}
}

func BenchmarkArquivosReais(b *testing.B) {
	files := namFiles(b)
	m, err := Load(files[0])
	if err != nil {
		b.Fatal(err)
	}
	const fs, block = 48000.0, 256
	for i, sm := range m.Submodels {
		name := "completo"
		if i < len(m.Submodels)-1 {
			name = "lite"
		}
		b.Run(name, func(b *testing.B) {
			net := NewNet(sm.Spec, block)
			net.Prewarm()
			x := noise(rand.New(rand.NewPCG(1, 1)), block, 0.3)
			y := make([]float32, block)
			for b.Loop() {
				net.Process(x, y)
			}
			perBlock := b.Elapsed().Seconds() / float64(b.N)
			b.ReportMetric(100*perBlock/(block/fs), "%tempo-real")
		})
	}
}

// TestReferenciaOficial compara com saídas da NeuralAmpModelerCore (ferramenta `render`,
// build sem o atalho A2) guardadas em testdata/nam/ (fora do git): <modelo>.lite.ref.wav e
// <modelo>.completo.ref.wav, geradas a partir de testdata/nam/di48.wav. Veja o PLANO.
//
// Tolerância: a própria NAM core difere de si mesma em até ~1,6e-4 (SNR ~91 dB) entre os
// seus dois caminhos de código num modelo high-gain, que amplifica o arredondamento de float32.
func TestReferenciaOficial(t *testing.T) {
	_, x64, err := wav.ReadMonoFile("../testdata/nam/di48.wav")
	if err != nil {
		t.Skip("sem testdata/nam/di48.wav:", err)
	}
	x := make([]float32, len(x64))
	for i, v := range x64 {
		x[i] = float32(v)
	}
	ran := 0
	for _, f := range namFiles(t) {
		m, err := Load(f)
		if err != nil {
			t.Fatal(err)
		}
		base := strings.TrimSuffix(filepath.Base(f), ".nam")
		for _, c := range []struct {
			nome    string
			tamanho float64
		}{{"lite", 0}, {"completo", 1}} {
			_, ref, err := wav.ReadMonoFile(filepath.Join("../testdata/nam", base+"."+c.nome+".ref.wav"))
			if err != nil {
				continue
			}
			ran++
			net := m.NewNet(c.tamanho, 64)
			y := make([]float32, len(x))
			net.Process(x, y)
			var se, sr, emax float64
			for i := range ref {
				d := float64(y[i]) - ref[i]
				se += d * d
				sr += ref[i] * ref[i]
				emax = max(emax, math.Abs(d))
			}
			snr := 10 * math.Log10(sr/max(se, 1e-300))
			t.Logf("%s (%s): erro máx %.2g, SNR %.1f dB", base, c.nome, emax, snr)
			if snr < 85 || emax > 5e-4 {
				t.Errorf("%s (%s): longe da referência (erro máx %.2g, SNR %.1f dB)", base, c.nome, emax, snr)
			}
		}
	}
	if ran == 0 {
		t.Skip("sem saídas de referência em testdata/nam")
	}
}
