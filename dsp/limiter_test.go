package dsp

import (
	"math"
	"testing"
)

// Abaixo do teto, o limitador não toca no sinal (bit a bit), ligado ou desligado.
func TestLimiterTransparenteAbaixoDoTeto(t *testing.T) {
	const fs = 48000.0
	for _, on := range []bool{true, false} {
		l := NewSafetyLimiter(fs)
		l.SetOn(on)
		x := seno(fs, 440, 0.95, 4800)
		y := append([]float64(nil), x...)
		l.Process(y, nil)
		for i := range x {
			if x[i] != y[i] {
				t.Fatalf("on=%v: amostra %d mudou (%v → %v)", on, i, x[i], y[i])
			}
		}
		if r := l.Reduction(); r != 0 {
			t.Errorf("on=%v: redução %.2f dB sem passar do teto", on, r)
		}
	}
}

// Com picos acima do teto: a saída nunca passa dele, L e R recebem o mesmo ganho, numa nota
// grave o ganho fica parado (sem distorcer cada ciclo) e depois volta a 1.
func TestLimiterTetoEVolta(t *testing.T) {
	const fs = 48000.0
	ceil := math.Pow(10, LimiterCeilingDB/20)
	l := NewSafetyLimiter(fs)
	n := int(fs)
	xl := seno(fs, 82.4, 2, n) // +6 dBFS, mi grave
	xr := seno(fs, 1000, 1.4, n)
	yl, yr := append([]float64(nil), xl...), append([]float64(nil), xr...)
	for i := 0; i < n; i += 256 {
		j := min(i+256, n)
		l.Process(yl[i:j], yr[i:j])
	}
	gmin, gmax := 1.0, 0.0
	for i := range yl {
		if math.Abs(yl[i]) > ceil+1e-12 || math.Abs(yr[i]) > ceil+1e-12 {
			t.Fatalf("amostra %d passou do teto: %.4f / %.4f", i, yl[i], yr[i])
		}
		if math.Abs(xl[i]) > 0.1 && math.Abs(xr[i]) > 0.1 {
			gl, gr := yl[i]/xl[i], yr[i]/xr[i]
			if math.Abs(gl-gr) > 1e-12 {
				t.Fatalf("amostra %d: ganho L %.6f, R %.6f", i, gl, gr)
			}
			if i > n/2 {
				gmin, gmax = math.Min(gmin, gl), math.Max(gmax, gl)
			}
		}
	}
	if gmax-gmin > 1e-4 {
		t.Errorf("ganho variando na nota sustentada: %.6f a %.6f", gmin, gmax)
	}
	if r := l.Reduction(); math.Abs(r-(6.02+0.3)) > 0.1 {
		t.Errorf("redução %.2f dB, esperava ~6,3", r)
	}
	// o sinal volta para baixo do teto: o ganho sobe sem salto e chega a 1 (bit a bit)
	q := seno(fs, 440, 0.5, 2*n)
	y := append([]float64(nil), q...)
	l.Process(y, nil)
	prev, last := 0.0, 0
	for i := range y {
		if math.Abs(q[i]) < 0.2 {
			continue
		}
		g := y[i] / q[i]
		if prev > 0 && (g < prev-1e-12 || g-prev > 5e-4*float64(i-last)) {
			t.Fatalf("amostra %d: ganho %.6f depois de %.6f (subida suave)", i, g, prev)
		}
		prev, last = g, i
	}
	if prev != 1 || y[len(y)-100] != q[len(q)-100] {
		t.Errorf("o ganho não voltou a 1 (%.6f)", prev)
	}
}

// Desligar com o ganho reduzido não dá salto: o ganho volta pela curva normal.
func TestLimiterDesligarSemSalto(t *testing.T) {
	const fs = 48000.0
	l := NewSafetyLimiter(fs)
	x := seno(fs, 200, 3, 4800)
	l.Process(x, nil)
	l.SetOn(false)
	q := seno(fs, 200, 3, 2*int(fs))
	y := append([]float64(nil), q...)
	l.Process(y, nil)
	prev, last := 0.0, 0
	for i := range y {
		if math.Abs(q[i]) < 0.5 {
			continue
		}
		g := y[i] / q[i]
		if prev > 0 && (g < prev-1e-12 || g-prev > 5e-4*float64(i-last)) {
			t.Fatalf("amostra %d: ganho %.6f depois de %.6f", i, g, prev)
		}
		prev, last = g, i
	}
	if prev != 1 {
		t.Errorf("desligado, o ganho deveria voltar a 1 (%.6f)", prev)
	}
}

func TestLimiterSemAlocacao(t *testing.T) {
	l := NewSafetyLimiter(48000)
	a, b := seno(48000, 100, 2, 256), seno(48000, 300, 2, 256)
	if n := testing.AllocsPerRun(50, func() { l.Process(a, b) }); n != 0 {
		t.Errorf("aloca %.0f vezes", n)
	}
}
