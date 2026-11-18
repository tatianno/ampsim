package dsp

import (
	"math"
	"math/cmplx"
	"math/rand"
	"testing"
)

func TestFFTContraDFT(t *testing.T) {
	const n = 64
	rng := rand.New(rand.NewSource(1))
	x := make([]complex128, n)
	for i := range x {
		x[i] = complex(rng.NormFloat64(), rng.NormFloat64())
	}
	want := make([]complex128, n)
	for k := range want {
		for j := range x {
			want[k] += x[j] * cmplx.Exp(complex(0, -2*math.Pi*float64(j*k)/n))
		}
	}
	f := NewFFT(n)
	got := append([]complex128(nil), x...)
	f.Forward(got)
	for k := range got {
		if cmplx.Abs(got[k]-want[k]) > 1e-9 {
			t.Fatalf("bin %d: %v != %v", k, got[k], want[k])
		}
	}
	f.Inverse(got)
	for i := range got {
		if cmplx.Abs(got[i]-x[i]) > 1e-12 {
			t.Fatalf("ida e volta, amostra %d: %v != %v", i, got[i], x[i])
		}
	}
}

// Resposta em frequência de um biquad em f.
func magDB(c Coefs, fs, f float64) float64 {
	z := cmplx.Exp(complex(0, -2*math.Pi*f/fs))
	num := complex(c.B0, 0) + complex(c.B1, 0)*z + complex(c.B2, 0)*z*z
	den := 1 + complex(c.A1, 0)*z + complex(c.A2, 0)*z*z
	return 20 * math.Log10(cmplx.Abs(num/den))
}

func TestEqualizadores(t *testing.T) {
	const fs = 48000.0
	casos := []struct {
		nome  string
		c     Coefs
		f, dB float64
	}{
		{"peaking +6 no centro", Peaking(fs, 700, 0.7, 6), 700, 6},
		{"peaking longe do centro", Peaking(fs, 700, 0.7, 6), 20000, 0},
		{"low shelf -9 em DC", LowShelf(fs, 100, 0.707, -9), 1, -9},
		{"low shelf nos agudos", LowShelf(fs, 100, 0.707, -9), 10000, 0},
		{"high shelf +12 no topo", HighShelf(fs, 3000, 0.707, 12), 23000, 12},
		{"high shelf em DC", HighShelf(fs, 3000, 0.707, 12), 1, 0},
		{"butter LP -3 dB no corte", Lowpass2(fs, 1000, math.Sqrt2/2), 1000, -3.0103},
	}
	for _, c := range casos {
		if got := magDB(c.c, fs, c.f); math.Abs(got-c.dB) > 0.15 {
			t.Errorf("%s: %.2f dB, esperava %.2f dB", c.nome, got, c.dB)
		}
	}
}

func TestResampleIdentidadeDeTaxa(t *testing.T) {
	// Um seno de 1 kHz reamostrado 48k → 44,1k continua um seno de 1 kHz.
	const fsIn, fsOut = 48000, 44100
	x := make([]float64, 4800)
	for i := range x {
		x[i] = math.Sin(2 * math.Pi * 1000 * float64(i) / fsIn)
	}
	y := ResampleRate(x, fsIn, fsOut)
	if len(y) != 4410 {
		t.Fatalf("tamanho %d, esperava 4410", len(y))
	}
	maxErr := 0.0
	for i := 200; i < len(y)-200; i++ {
		want := math.Sin(2 * math.Pi * 1000 * float64(i) / fsOut)
		maxErr = math.Max(maxErr, math.Abs(y[i]-want))
	}
	if maxErr > 1e-3 {
		t.Errorf("erro máximo %.2e", maxErr)
	}
}

func TestSmoother(t *testing.T) {
	s := NewSmoother(48000, 10)
	s.SetTarget(1)
	if s.Next() != 1 {
		t.Fatal("primeiro alvo deveria saltar direto")
	}
	s.SetTarget(0)
	v := 0.0
	for i := 0; i < 480; i++ { // 10 ms = 1 constante de tempo
		v = s.Next()
	}
	if math.Abs(v-math.Exp(-1)) > 0.01 {
		t.Errorf("após 1 τ: %.3f, esperava %.3f", v, math.Exp(-1))
	}
}

func TestFadeEdges(t *testing.T) {
	x := make([]float64, 4800)
	for i := range x {
		x[i] = 1
	}
	FadeEdges(x, 48000, 5, 20) // 240 e 960 amostras
	if x[0] != 0 || x[120] != 0.5 || x[240] != 1 || x[2400] != 1 {
		t.Errorf("rampa de entrada: %v %v %v", x[0], x[120], x[240])
	}
	if x[len(x)-1] != 0 || math.Abs(x[len(x)-1-480]-0.25) > 1e-12 || x[len(x)-1-960] != 1 {
		t.Errorf("rampa de saída: %v %v %v", x[len(x)-1], x[len(x)-1-480], x[len(x)-1-960])
	}
	FadeEdges(make([]float64, 10), 48000, 5, 20) // arquivo curtíssimo: não pode quebrar
}
