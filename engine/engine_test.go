package engine

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"ampsim/wav"
)

var (
	diPath = filepath.Join("..", "testdata", "guitarra_di.wav")
	irPath = filepath.Join("..", "testdata", "gabinete_2x12_sm57.wav")
)

func lerDI(t *testing.T) (float64, []float64) {
	t.Helper()
	fs, x, err := wav.ReadMonoFile(diPath)
	if err != nil {
		t.Fatal(err)
	}
	return float64(fs), x
}

// cadeia com tudo ligado e a IR carregada
func cadeiaCompleta(t *testing.T, fs float64, maxBlock int) *Chain {
	t.Helper()
	c := NewDefaultChain()
	for _, m := range c.Modules() {
		c.SetEnabled(m.ID(), true)
	}
	if err := c.Amp().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	c.Prepare(fs, maxBlock)
	return c
}

func rmsDB(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return 10 * math.Log10(s/float64(len(x))+1e-300)
}

// A saída não pode depender do tamanho de bloco do host.
func TestInvarianciaDeBloco(t *testing.T) {
	fs, di := lerDI(t)
	di = di[:44100]
	var ref []float64
	for _, bloco := range []int{512, 1, 37, 64, 500} {
		c := cadeiaCompleta(t, fs, 512)
		out := append([]float64(nil), di...)
		for i := 0; i < len(out); i += bloco {
			c.ProcessMono(out[i:min(i+bloco, len(out))])
		}
		if ref == nil {
			ref = out
			continue
		}
		for i := range out {
			if out[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere (%g != %g)", bloco, i, out[i], ref[i])
			}
		}
	}
}

func TestCadeiaSemAlocacao(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 256)
	in := [][]float32{make([]float32, 256), make([]float32, 256)}
	out := [][]float32{make([]float32, 256), make([]float32, 256)}
	for i := range in[0] {
		in[0][i] = float32(di[10000+i])
		in[1][i] = in[0][i]
	}
	liga := false
	a := testing.AllocsPerRun(50, func() {
		liga = !liga
		c.SetEnabled("chorus", liga) // inclui crossfade de bypass
		c.Process(in, out, 256)
	})
	if a != 0 {
		t.Errorf("Process aloca %.0f vezes por bloco", a)
	}
}

// Com tudo em bypass, a saída é a entrada atrasada exatamente pela latência da cadeia.
func TestBypassMantemLatencia(t *testing.T) {
	fs, di := lerDI(t)
	c := NewDefaultChain()
	c.SetEnabled("gate", false)
	c.SetEnabled("amp", false)
	c.Prepare(fs, 128)
	lat := c.Latency()
	if lat != 32 {
		t.Errorf("latência %d, esperava 32", lat)
	}
	x := di[:10000]
	out := append([]float64(nil), x...)
	c.ProcessMono(out)
	for i := lat; i < len(out); i++ {
		if out[i] != x[i-lat] {
			t.Fatalf("amostra %d: %g != %g", i, out[i], x[i-lat])
		}
	}
}

// O bug original: ruído no fim do áudio. Depois que a guitarra para, a saída tem que ir a silêncio.
func TestFimSemRuido(t *testing.T) {
	fs, di := lerDI(t)
	c := NewDefaultChain() // gate + amp
	if err := c.Amp().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	c.Prepare(fs, 256)
	// mesmo fade de 30 ms que o main.py aplica na DI (o arquivo termina num clique)
	fade := int(0.03 * fs)
	for i := 0; i < fade; i++ {
		g := 1 - float64(i)/float64(fade-1)
		di[len(di)-fade+i] *= g * g
	}
	out := append(append([]float64(nil), di...), make([]float64, int(fs))...)
	c.ProcessMono(out)
	tail := out[len(out)-int(0.5*fs):]
	t.Logf("pico %.2f, RMS dos últimos 0,5 s: %.1f dBFS", peak(out), rmsDB(tail))
	if rmsDB(tail) > -90 {
		t.Errorf("cauda com %.1f dBFS, esperava silêncio", rmsDB(tail))
	}
}

func peak(x []float64) float64 {
	p := 0.0
	for _, v := range x {
		p = math.Max(p, math.Abs(v))
	}
	return p
}

// Ligar e desligar pedais e girar knobs ao vivo não pode gerar NaN nem explosão.
func TestAutomacaoAoVivo(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 128)
	ids := []string{"gate", "amp", "chorus", "flanger", "phaser", "delay", "reverb"}
	out := append([]float64(nil), di...)
	for i, k := 0, 0; i < len(out); i, k = i+128, k+1 {
		if k%20 == 0 {
			id := ids[(k/20)%len(ids)]
			c.SetEnabled(id, !c.Enabled(id))
			for _, p := range c.Module(id).Params() {
				p.SetNormalized(float64(k%7) / 6)
			}
		}
		c.ProcessMono(out[i:min(i+128, len(out))])
	}
	for i, v := range out {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 20 {
			t.Fatalf("amostra %d inválida: %g", i, v)
		}
	}
}

func TestPresetIdaEVolta(t *testing.T) {
	a := NewDefaultChain()
	a.Module("amp").Param("gain").Set(8.2)
	a.Module("delay").Param("time").Set(450)
	a.SetEnabled("delay", true)
	a.SetEnabled("gate", false)
	if err := a.Amp().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a.State())
	if err != nil {
		t.Fatal(err)
	}

	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	c := NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	if g := c.Module("amp").Param("gain").Get(); g != 8.2 {
		t.Errorf("gain = %v", g)
	}
	if !c.Enabled("delay") || c.Enabled("gate") {
		t.Error("estado ligado/desligado não foi restaurado")
	}
	if c.Amp().IRPath() != irPath {
		t.Errorf("IR = %q", c.Amp().IRPath())
	}

	st.Modules = append(st.Modules, ModuleState{ID: "wah"})
	if err := c.ApplyState(st); err == nil {
		t.Error("módulo desconhecido deveria gerar erro")
	}
}

func TestParametroDiscreto(t *testing.T) {
	p := NewPhaser().Param("stages")
	for _, c := range []struct{ in, want float64 }{{4.9, 4}, {5.2, 6}, {7.2, 8}, {12, 8}, {0, 4}} {
		p.Set(c.in)
		if p.Get() != c.want {
			t.Errorf("Set(%v) → %v, esperava %v", c.in, p.Get(), c.want)
		}
	}
}

// Trocar a IR com o áudio rodando (outra goroutine) não pode gerar corrida de dados.
func TestTrocaDeIRAoVivo(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 128)
	fsIR, raw, err := wav.ReadMonoFile(irPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			c.Amp().LoadIR(raw[:len(raw)/(i+1)], fsIR, "")
		}
	}()
	out := append([]float64(nil), di[:44100]...)
	for i := 0; i < len(out); i += 128 {
		c.ProcessMono(out[i:min(i+128, len(out))])
	}
	<-done
	c.ProcessMono(make([]float64, 128)) // aplica a última IR pendente
}

// O crossfade de bypass precisa durar BypassFadeMs (não pode ser instantâneo).
func TestCrossfadeDeBypass(t *testing.T) {
	const fs = 48000.0
	c := NewChain(NewChorus())
	c.Module("chorus").Param("mix").Set(100)
	c.Module("chorus").Param("depth").Set(0)
	c.SetEnabled("chorus", false)
	c.Prepare(fs, 256)
	// entrada constante 1: seco = 1; chorus 100% wet com atraso fixo de 22 ms = 0 no início
	buf := make([]float64, int(0.03*fs))
	for i := range buf {
		buf[i] = 1
	}
	c.SetEnabled("chorus", true)
	c.ProcessMono(buf)
	fade := int(BypassFadeMs / 1000 * fs)
	if got := buf[fade/2]; math.Abs(got-0.5) > 0.01 {
		t.Errorf("no meio do crossfade a saída deveria ser ~0,5, veio %.3f", got)
	}
	if buf[fade+10] != 0 {
		t.Errorf("depois do crossfade deveria ser só o efeito (0), veio %.3f", buf[fade+10])
	}
}
