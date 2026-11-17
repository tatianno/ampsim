package dsp

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tatianno/ampsim/wav"
)

// Compara os blocos Go com as saídas do protótipo Python.
// As referências são geradas por referencia_python/gerar_referencias.py;
// os parâmetros aqui precisam ser os mesmos de lá.

func lerRef(t *testing.T, nome string) (float64, []float64) {
	t.Helper()
	fs, x, err := wav.ReadMonoFile(filepath.Join("..", "testdata", "referencia", nome))
	if err != nil {
		t.Skipf("referência ausente (rode referencia_python/gerar_referencias.py): %v", err)
	}
	return float64(fs), x
}

// erroDB devolve 20·log10(rms(got−want) / rms(want)).
func erroDB(got, want []float64) float64 {
	n := min(len(got), len(want))
	var e, s float64
	for i := 0; i < n; i++ {
		d := got[i] - want[i]
		e += d * d
		s += want[i] * want[i]
	}
	return 10 * math.Log10(e/s+1e-300)
}

// processa em blocos de tamanho irregular, como um host faria
func emBlocos(buf []float64, bloco int, f func([]float64)) {
	for i := 0; i < len(buf); i += bloco {
		f(buf[i:min(i+bloco, len(buf))])
	}
}

func comparar(t *testing.T, nome string, got, want []float64, limiteDB float64) {
	t.Helper()
	e := erroDB(got, want)
	t.Logf("%-10s erro %7.1f dB (limite %.0f dB)", nome, e, limiteDB)
	if e > limiteDB {
		t.Errorf("%s: erro %.1f dB acima do limite %.0f dB", nome, e, limiteDB)
	}
}

func TestReferenciaFiltrosLineares(t *testing.T) {
	fs, di := lerRef(t, "di.wav")
	_, want := lerRef(t, "filtros.wav")
	hp := NewCascade(ButterworthHighpass(2, fs, 85))
	lp := NewCascade(ButterworthLowpass(3, fs, 6200))
	got := append([]float64(nil), di...)
	emBlocos(got, 37, func(b []float64) { hp.ProcessBlock(b); lp.ProcessBlock(b) })
	comparar(t, "filtros", got, want, -100)
}

func TestReferenciaGate(t *testing.T) {
	fs, di := lerRef(t, "di.wav")
	_, want1 := lerRef(t, "gate1.wav")
	g := NewGate(fs)
	g.SetHysteresis(0)
	g.SetThreshold(-40)
	g.SetTimes(100, 100) // Python: mesma suavização para abrir e fechar
	got := append([]float64(nil), di...)
	emBlocos(got, 37, g.ProcessBlock)
	comparar(t, "gate1", got, want1, -80)

	_, sat := lerRef(t, "sat.wav")
	_, want2 := lerRef(t, "gate2.wav")
	g2 := NewGate(fs)
	g2.SetHysteresis(0)
	g2.SetThreshold(-38)
	g2.SetTimes(120, 120)
	got2 := append([]float64(nil), sat...)
	emBlocos(got2, 37, g2.ProcessBlock)
	comparar(t, "gate2", got2, want2, -80)
}

func TestReferenciaIR(t *testing.T) {
	precisa(t, filepath.Join("..", "testdata", "gabinete_2x12_sm57.wav"))
	fs, want := lerRef(t, "ir_preparada.wav")
	fsIR, raw, err := wav.ReadMonoFile(filepath.Join("..", "testdata", "gabinete_2x12_sm57.wav"))
	if err != nil {
		t.Fatal(err)
	}
	got := PrepareIR(raw, fsIR, int(fs), 150)
	if len(got) != len(want) {
		t.Fatalf("IR com %d amostras, esperava %d", len(got), len(want))
	}
	comparar(t, "IR", got, want, -100)
}

func TestReferenciaGabinete(t *testing.T) {
	precisa(t, filepath.Join("..", "testdata", "gabinete_2x12_sm57.wav"))
	fs, in := lerRef(t, "gate2.wav")
	_, want := lerRef(t, "cab.wav")
	fsIR, raw, err := wav.ReadMonoFile(filepath.Join("..", "testdata", "gabinete_2x12_sm57.wav"))
	if err != nil {
		t.Fatal(err)
	}
	conv := NewConvolver(PrepareIR(raw, fsIR, int(fs), 150), 64)
	hp := NewCascade(ButterworthHighpass(2, fs, 85))
	lp := NewCascade(ButterworthLowpass(3, fs, 6200))
	got := append([]float64(nil), in...)
	emBlocos(got, 37, func(b []float64) { conv.ProcessBlock(b); hp.ProcessBlock(b); lp.ProcessBlock(b) })
	comparar(t, "gabinete", got, want, -100)
}

// A saturação usa filtros de oversampling diferentes do resample_poly do Python
// (que é de fase zero e não serve para tempo real), então a comparação é mais solta:
// alinhamos pela latência e exigimos o mesmo sinal com erro bem abaixo do nível dele.
func TestReferenciaSaturacao(t *testing.T) {
	fs, in := lerRef(t, "gate1.wav")
	_, want := lerRef(t, "sat.wav")
	p := NewPreamp(fs)
	p.SetDrive(0.65)
	lat := p.Latency()
	got := append(append([]float64(nil), in...), make([]float64, lat)...)
	emBlocos(got, 37, p.ProcessBlock)
	comparar(t, "saturação", got[lat:], want, -50)
}

func TestReferenciaEfeitos(t *testing.T) {
	fs, di := lerRef(t, "di.wav")
	entrada := func(caudaS float64) []float64 {
		x := make([]float64, len(di)+int(caudaS*fs))
		for i, v := range di {
			x[i] = 0.5 * v
		}
		return x
	}

	casos := []struct {
		nome  string
		cauda float64
		proc  func([]float64)
	}{
		{"chorus", 0, func() func([]float64) {
			c := NewChorus(fs)
			c.Set(1.2, 2, 22, 0.5)
			return c.ProcessBlock
		}()},
		{"flanger", 0, func() func([]float64) {
			f := NewFlanger(fs)
			f.Set(0.3, 0.5, 4, 0.6, 0.5)
			return f.ProcessBlock
		}()},
		{"phaser", 0, func() func([]float64) {
			p := NewPhaser(fs)
			p.Set(0.5, 200, 1800, 4, 0.5, 0.5)
			return p.ProcessBlock
		}()},
		{"delay", 2.0, func() func([]float64) {
			d := NewDelay(fs)
			d.Set(380, 0.45, 0.35, 0.35)
			return d.ProcessBlock
		}()},
		{"reverb", 1.5, func() func([]float64) {
			r := NewReverb(fs)
			r.Set(0.8, 0.4, 0.35)
			return r.ProcessBlock
		}()},
	}
	for _, c := range casos {
		_, want := lerRef(t, c.nome+".wav")
		got := entrada(c.cauda)
		if len(got) != len(want) {
			t.Fatalf("%s: %d amostras, esperava %d", c.nome, len(got), len(want))
		}
		emBlocos(got, 37, c.proc)
		comparar(t, c.nome, got, want, -100)
	}
}

// precisa pula o teste se algum arquivo de testdata/ não existir
// (os WAVs não vão para o repositório; veja o README).
func precisa(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("arquivo de teste ausente: %s (veja README, seção testdata)", p)
		}
	}
}
