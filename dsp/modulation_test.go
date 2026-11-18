package dsp

import (
	"math"
	"testing"
)

func senoMod(f, fs float64, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = math.Sin(2 * math.Pi * f * float64(i) / fs)
	}
	return x
}

func TestTremolo(t *testing.T) {
	const fs = 48000.0
	// depth 0: o sinal passa intacto
	tr := NewTremolo(fs)
	tr.Set(5, 0, WaveSine)
	x := senoMod(440, fs, 4800)
	y := append([]float64(nil), x...)
	tr.ProcessBlock(y)
	for i := range x {
		if math.Abs(y[i]-x[i]) > 1e-12 {
			t.Fatalf("depth 0 mudou o sinal na amostra %d", i)
		}
	}
	// depth 100%, sinal constante: o ganho vai de ~0 a 1, no ritmo do rate
	for _, shape := range []int{WaveSine, WaveTriangle, WaveSquare} {
		tr := NewTremolo(fs)
		tr.Set(4, 1, shape)
		g := make([]float64, int(fs)) // 1 s = 4 ciclos
		for i := range g {
			g[i] = 1
		}
		tr.ProcessBlock(g)
		lo, hi := 1.0, 0.0
		for _, v := range g[int(fs/4):] {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		if lo > 0.02 || hi < 0.98 {
			t.Errorf("forma %d: ganho de %.3f a %.3f, esperava ~0 a ~1", shape, lo, hi)
		}
		// mínimos (vales) por segundo = rate
		vales := 0
		for i := 1; i < len(g)-1; i++ {
			if g[i] < 0.05 && g[i-1] >= 0.05 {
				vales++
			}
		}
		if vales < 3 || vales > 5 {
			t.Errorf("forma %d: %d vales em 1 s, esperava 4", shape, vales)
		}
	}
}

func TestVibrato(t *testing.T) {
	const fs = 48000.0
	// depth 0: só um atraso de 2 amostras
	v := NewVibrato(fs)
	v.Set(5, 0)
	x := senoMod(440, fs, 4800)
	y := append([]float64(nil), x...)
	v.ProcessBlock(y)
	for i := 2; i < len(x); i++ {
		if math.Abs(y[i]-x[i-2]) > 1e-9 {
			t.Fatalf("depth 0: amostra %d = %g, esperava %g", i, y[i], x[i-2])
		}
	}
	// desvio de tom: ≈ 2π·rate·depth (rate 2 Hz, depth 1 ms → ±1,257%)
	v = NewVibrato(fs)
	v.depth.Snap(1)
	v.rate.Snap(2)
	n := int(2 * fs)
	y = senoMod(1000, fs, n)
	v.ProcessBlock(y)
	var cruz []float64           // cruzamentos de zero subindo, com interpolação
	for i := n / 8; i < n; i++ { // depois do começo (linha de atraso ainda vazia)
		if y[i-1] < 0 && y[i] >= 0 {
			cruz = append(cruz, float64(i-1)+y[i-1]/(y[i-1]-y[i]))
		}
	}
	fmin, fmax := math.Inf(1), 0.0
	for k := 1; k < len(cruz); k++ {
		f := fs / (cruz[k] - cruz[k-1])
		fmin, fmax = math.Min(fmin, f), math.Max(fmax, f)
	}
	want := 2 * math.Pi * 2 * 0.001 * 1000 // Hz de desvio em 1 kHz
	if math.Abs((fmax-1000)-want) > 0.15*want || math.Abs((1000-fmin)-want) > 0.15*want {
		t.Errorf("tom de %.1f a %.1f Hz, esperava 1000 ± %.1f Hz", fmin, fmax, want)
	}
	// a interpolação cúbica não abafa: o nível de um tom agudo fica igual
	v = NewVibrato(fs)
	v.depth.Snap(1)
	v.rate.Snap(5)
	y = senoMod(8000, fs, n)
	v.ProcessBlock(y)
	rms := 0.0
	for _, s := range y[n/2:] {
		rms += s * s
	}
	if db := 10 * math.Log10(rms/float64(n/2)*2); db < -0.5 {
		t.Errorf("8 kHz perdeu %.2f dB no vibrato", -db)
	}
}
