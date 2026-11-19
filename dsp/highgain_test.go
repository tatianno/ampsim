package dsp

import (
	"math"
	"testing"
)

// Estável em todo o knob de ganho, inclusive com sinal forte, e sem alocar.
func TestHighGainEstavelESemAlocacao(t *testing.T) {
	h := NewHighGain(48000)
	buf := make([]float64, 256)
	for _, d := range []float64{0, 0.5, 1} {
		h.SetDrive(d)
		h.Reset()
		peak := 0.0
		for b := 0; b < 200; b++ {
			for i := range buf {
				n := b*256 + i
				buf[i] = 0.9 * math.Sin(2*math.Pi*82*float64(n)/48000) // corda solta, forte
			}
			h.ProcessBlock(buf)
			for _, v := range buf {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("ganho %.1f: valor inválido", d)
				}
				peak = math.Max(peak, math.Abs(v))
			}
		}
		// a saída é limitada pelo último estágio (|tanh| ≤ 1) vezes o nível de saída
		if peak > 2*HighGainDefault.Out || peak < 0.01 {
			t.Errorf("ganho %.1f: pico %.3f", d, peak)
		}
	}
	if a := testing.AllocsPerRun(50, func() { h.ProcessBlock(buf) }); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}

// O knob de ganho muda a distorção: com o ganho no zero, um sinal fraco sai bem mais limpo.
func TestHighGainKnobDeGanho(t *testing.T) {
	thd := func(d float64) float64 {
		h := NewHighGain(48000)
		h.SetDrive(d)
		x := make([]float64, 48000)
		for i := range x {
			x[i] = 0.003 * math.Sin(2*math.Pi*440*float64(i)/48000)
		}
		h.ProcessBlock(x)
		y := x[24000:]
		amp := func(f float64) float64 {
			var re, im float64
			for i, v := range y {
				re += v * math.Cos(2*math.Pi*f*float64(i)/48000)
				im += v * math.Sin(2*math.Pi*f*float64(i)/48000)
			}
			return math.Hypot(re, im)
		}
		h1 := amp(440)
		e := 0.0
		for k := 2; k <= 6; k++ {
			e += amp(440*float64(k)) * amp(440*float64(k))
		}
		return math.Sqrt(e) / h1
	}
	lo, hi := thd(0), thd(1)
	t.Logf("THD com −50 dBFS: ganho 0 = %.1f%%, ganho 10 = %.1f%%", 100*lo, 100*hi)
	if !(lo < hi/3) {
		t.Errorf("o ganho não muda a distorção: %.3f × %.3f", lo, hi)
	}
}

func BenchmarkHighGain(b *testing.B) {
	h := NewHighGain(48000)
	h.SetDrive(0.7)
	buf := make([]float64, 64)
	for i := range buf {
		buf[i] = 0.2 * math.Sin(float64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ProcessBlock(buf)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/(64.0/48000*1e9)*100, "%tempo-real")
}
