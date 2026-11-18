package dsp

import (
	"math"
	"testing"
)

func seno(fs, f, amp float64, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = amp * math.Sin(2*math.Pi*f*float64(i)/fs)
	}
	return x
}

func picoDB(x []float64) float64 {
	p := 0.0
	for _, v := range x {
		p = math.Max(p, math.Abs(v))
	}
	return 20 * math.Log10(p)
}

func TestCompressorCurvaEstatica(t *testing.T) {
	c := NewCompressor(48000)
	c.Set(-20, 4, 10, 150, 0, 1)
	casos := []struct{ in, gr float64 }{
		{-40, 0},        // bem abaixo do limiar
		{-26, 0},        // abaixo do joelho (limiar − 3 dB)
		{-20, 0.5625},   // no limiar: metade do joelho → (1−1/4)·3²/12
		{-6, 14 * 0.75}, // 14 dB acima, razão 4:1 → reduz 10,5 dB
		{0, 20 * 0.75},  // 20 dB acima → 15 dB
	}
	for _, k := range casos {
		if got := c.StaticGR(k.in); math.Abs(got-k.gr) > 1e-9 {
			t.Errorf("entrada %v dB: GR %.4f, esperava %.4f", k.in, got, k.gr)
		}
	}
	c.Set(-20, 1, 10, 150, 0, 1) // razão 1:1 = sem compressão
	if gr := c.StaticGR(0); gr != 0 {
		t.Errorf("razão 1: GR %v", gr)
	}
}

// Um seno a −6 dBFS com limiar −20 e razão 4 sai ~10,5 dB mais baixo (depois de assentar);
// o Level compensa.
func TestCompressorNivelDeSaida(t *testing.T) {
	const fs = 48000
	c := NewCompressor(fs)
	c.Set(-20, 4, 5, 200, 0, 1)
	x := seno(fs, 220, math.Pow(10, -6.0/20), fs) // 1 s
	c.ProcessBlock(x)
	got := picoDB(x[fs/2:])
	if math.Abs(got-(-16.5)) > 1 {
		t.Errorf("pico de saída %.2f dB, esperava ≈ −16,5 dB", got)
	}
	if gr := c.GainReductionDB(); math.Abs(gr-10.5) > 1 {
		t.Errorf("medidor de GR %.2f dB, esperava ≈ 10,5 dB", gr)
	}

	c2 := NewCompressor(fs)
	c2.Set(-20, 4, 5, 200, 10.5, 1)
	y := seno(fs, 220, math.Pow(10, -6.0/20), fs)
	c2.ProcessBlock(y)
	if got := picoDB(y[fs/2:]); math.Abs(got-(-6)) > 1 {
		t.Errorf("com Level +10,5 dB: %.2f dB, esperava ≈ −6 dB", got)
	}
}

func TestCompressorAbaixoDoLimiarEMixSeco(t *testing.T) {
	const fs = 48000
	c := NewCompressor(fs)
	c.Set(-20, 8, 10, 150, 0, 1)
	x := seno(fs, 440, 0.01, 4800) // −40 dBFS: não comprime
	ref := append([]float64(nil), x...)
	c.ProcessBlock(x)
	for i := range x {
		if math.Abs(x[i]-ref[i]) > 1e-9 {
			t.Fatalf("abaixo do limiar deveria passar intacto (amostra %d)", i)
		}
	}
	d := NewCompressor(fs)
	d.Set(-40, 20, 1, 50, 12, 0) // mix 0: só o sinal seco
	y := seno(fs, 440, 0.9, 4800)
	ref = append([]float64(nil), y...)
	d.ProcessBlock(y)
	for i := range y {
		if y[i] != ref[i] {
			t.Fatalf("mix 0 deveria devolver o sinal seco (amostra %d)", i)
		}
	}
}

// Degrau de nível: a redução de ganho chega a ~63% do valor final em ~1 constante de attack.
func TestCompressorAttack(t *testing.T) {
	const fs = 48000
	c := NewCompressor(fs)
	c.Set(-30, 10, 20, 500, 0, 1)
	buf := make([]float64, int(0.02*fs)) // 20 ms de DC em 0 dBFS (nível constante, sem ondulação)
	for i := range buf {
		buf[i] = 1
	}
	c.ProcessBlock(buf)
	final := c.StaticGR(0)
	if r := c.gr / final; math.Abs(r-(1-math.Exp(-1))) > 0.02 {
		t.Errorf("após 1 τ de attack: %.1f%% do GR final, esperava ~63%%", 100*r)
	}
}
