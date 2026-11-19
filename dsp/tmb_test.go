package dsp

import (
	"math"
	"math/cmplx"
	"testing"
)

func evalH(b, a [4]float64, s complex128) complex128 {
	var n, d complex128
	p := complex(1, 0)
	for i := 0; i < 4; i++ {
		n += complex(b[i], 0) * p
		d += complex(a[i], 0) * p
		p *= s
	}
	return n / d
}

func dbOf(h complex128) float64 { return 20 * math.Log10(cmplx.Abs(h)+1e-300) }

// O H(s) dos polinômios tem que bater com a solução direta do circuito em qualquer frequência.
func TestTMBPolinomiosContraCircuito(t *testing.T) {
	for _, m := range []TMBModel{TMBFender, TMBMarshall} {
		for _, k := range [][3]float64{{0.5, 0.5, 0.5}, {0.1, 0.9, 0.3}, {1, 0, 1}, {0.8, 0.2, 0.05}} {
			b, a := m.Analog(k[0], k[1], k[2])
			for _, f := range []float64{20, 100, 440, 1000, 5000, 15000} {
				got := evalH(b, a, complex(0, 2*math.Pi*f))
				want := m.AnalogResponse(k[0], k[1], k[2], f)
				if e := cmplx.Abs(got-want) / cmplx.Abs(want); e > 1e-9 {
					t.Errorf("%s %v @ %.0f Hz: erro relativo %.2e (%.2f dB vs %.2f dB)", m.Name, k, f, e, dbOf(got), dbOf(want))
				}
			}
		}
	}
}

// Comportamentos conhecidos do circuito.
func TestTMBComportamentos(t *testing.T) {
	// Fender com tudo em 0: o cursor do agudo vai para a terra — o amp fica mudo (os pots têm
	// uma resistência mínima, como os reais, então não é zero absoluto: ≈ −60 dB).
	if g := dbOf(TMBFender.AnalogResponse(0, 0, 0, 1000)); g > -50 {
		t.Errorf("Fender tudo em 0: %.1f dB, esperava mudo (< −50 dB)", g)
	}
	// Tudo em 5: o "vale" nos médios — a resposta em 300–800 Hz fica bem abaixo das pontas.
	for _, m := range []TMBModel{TMBFender, TMBMarshall} {
		lo, hi := dbOf(m.AnalogResponse(0.5, 0.5, 0.5, 60)), dbOf(m.AnalogResponse(0.5, 0.5, 0.5, 6000))
		vale := math.Inf(1)
		for f := 300.0; f <= 800; f += 10 {
			vale = math.Min(vale, dbOf(m.AnalogResponse(0.5, 0.5, 0.5, f)))
		}
		if vale > math.Min(lo, hi)-3 {
			t.Errorf("%s em 5/5/5: vale %.1f dB, pontas %.1f / %.1f dB — esperava o vale nos médios", m.Name, vale, lo, hi)
		}
		t.Logf("%s 5/5/5: 60 Hz %.1f dB, vale %.1f dB, 6 kHz %.1f dB", m.Name, lo, vale, hi)
	}
	// Mais médio levanta a região do vale; mais agudo levanta os agudos.
	m := TMBFender
	if dbOf(m.AnalogResponse(0.5, 1, 0.5, 500)) <= dbOf(m.AnalogResponse(0.5, 0, 0.5, 500)) {
		t.Error("médio em 10 deveria ter mais 500 Hz que médio em 0")
	}
	if dbOf(m.AnalogResponse(1, 0.5, 0.5, 5000)) <= dbOf(m.AnalogResponse(0, 0.5, 0.5, 5000)) {
		t.Error("agudo em 10 deveria ter mais 5 kHz que agudo em 0")
	}
	if dbOf(m.AnalogResponse(0.5, 0.5, 1, 60)) <= dbOf(m.AnalogResponse(0.5, 0.5, 0.05, 60)) {
		t.Error("grave em 10 deveria ter mais 60 Hz que grave em 0")
	}
}

// A versão digital (bilinear) segue a analógica até alguns kHz.
func TestTMBDigitalContraAnalogico(t *testing.T) {
	const fs = 48000.0
	for _, m := range []TMBModel{TMBFender, TMBMarshall} {
		b, a := m.Analog(0.5, 0.5, 0.5)
		B, A := Bilinear3(b, a, fs)
		for _, f := range []float64{50, 200, 500, 1000, 2000, 4000} {
			z := cmplx.Exp(complex(0, -2*math.Pi*f/fs))
			hd := evalH(B, A, z) // em z⁻¹: o mesmo avaliador serve
			ha := evalH(b, a, complex(0, 2*math.Pi*f))
			if d := math.Abs(dbOf(hd) - dbOf(ha)); d > 0.5 {
				t.Errorf("%s @ %.0f Hz: digital %.2f dB, analógico %.2f dB", m.Name, f, dbOf(hd), dbOf(ha))
			}
		}
	}
}

// Com os knobs em 5, o ganho de compensação deixa a média de 100 Hz a 5 kHz perto de 0 dB, e
// o filtro digital é estável (resposta ao impulso decai) em toda a faixa dos knobs.
func TestTMBStackGanhoEEstabilidade(t *testing.T) {
	const fs = 48000.0
	for _, m := range []TMBModel{TMBFender, TMBMarshall} {
		for _, k := range [][3]float64{{5, 5, 5}, {0, 0, 0}, {10, 10, 10}, {10, 0, 10}, {0, 10, 0}, {3, 7, 2}} {
			s := NewTMBStack(fs, m)
			s.Set(k[0], k[1], k[2])
			for i := range s.knobs {
				s.knobs[i].Snap([]float64{k[0], k[1], k[2]}[i])
			}
			s.update()
			x := make([]float64, int(fs))
			x[0] = 1
			s.ProcessBlock(x)
			tail := 0.0
			for _, v := range x[len(x)-4800:] {
				tail = math.Max(tail, math.Abs(v))
			}
			if math.IsNaN(tail) || tail > 1e-6 {
				t.Errorf("%s %v: resposta ao impulso não decai (%.2e no fim)", m.Name, k, tail)
			}
		}
		s := NewTMBStack(fs, m)
		x := seno(fs, 1000, 0.1, 9600)
		s.ProcessBlock(x)
		t.Logf("%s 5/5/5, 1 kHz: %+.1f dB (compensação %.1f dB)", m.Name, picoDB(x[4800:])+20, 20*math.Log10(s.makeup))
	}
}

func TestTMBStackSemAlocacao(t *testing.T) {
	s := NewTMBStack(48000, TMBFender)
	buf := make([]float64, 64)
	k := 0.0
	if a := testing.AllocsPerRun(50, func() {
		k = math.Mod(k+0.7, 10)
		s.Set(k, 10-k, k/2) // knobs girando: recalcula os coeficientes
		s.ProcessBlock(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}

func BenchmarkTMBRecalculo(b *testing.B) {
	m := TMBFender
	for i := 0; i < b.N; i++ {
		ba, aa := m.Analog(0.3, 0.6, 0.2)
		Bilinear3(ba, aa, 48000)
	}
}

func BenchmarkTMBStack(b *testing.B) {
	s := NewTMBStack(benchFS, TMBFender)
	s.Set(6, 4, 7)
	bench(b, s.ProcessBlock)
}

// A IR genérica tem o formato de um gabinete de guitarra: presença em 2,2 kHz, vale "caixa"
// em 450 Hz, graves cortados abaixo da ressonância e agudos bem atenuados.
func TestGenericCabIR(t *testing.T) {
	const fs = 48000.0
	ir := GenericCabIR(fs)
	mag := func(f float64) float64 {
		var acc complex128
		for n, v := range ir {
			acc += complex(v, 0) * cmplx.Exp(complex(0, -2*math.Pi*f*float64(n)/fs))
		}
		return dbOf(acc)
	}
	g := map[float64]float64{}
	for _, f := range []float64{50, 110, 450, 1000, 2200, 10000} {
		g[f] = mag(f)
	}
	t.Logf("50 Hz %.1f · 110 Hz %.1f · 450 Hz %.1f · 1 kHz %.1f · 2,2 kHz %.1f · 10 kHz %.1f dB",
		g[50], g[110], g[450], g[1000], g[2200], g[10000])
	switch {
	case g[2200] <= g[1000]:
		t.Error("deveria ter presença em 2,2 kHz (acima de 1 kHz)")
	case g[450] >= g[1000]:
		t.Error("deveria ter o vale em 450 Hz (abaixo de 1 kHz)")
	case g[10000] > g[1000]-25:
		t.Error("10 kHz deveria estar pelo menos 25 dB abaixo de 1 kHz")
	case g[50] >= g[110]:
		t.Error("abaixo da ressonância (50 Hz) deveria ter menos grave que em 110 Hz")
	}
}

// O knob de grave anda em passos iguais de dB em 100 Hz (médio e agudo em 5), do pot quase
// zerado ao máximo, e BassKnob é o inverso de BassPot.
func TestTMBCurvaDoGrave(t *testing.T) {
	for _, m := range []TMBModel{TMBFender, TMBMarshall, TMBHighGain} {
		c := m.newBassCurve()
		if p0, p10 := c.pot(0), c.pot(10); math.Abs(p0-bassPotMin) > 1e-12 || p10 != 1 {
			t.Errorf("%s: pot(0) = %g, pot(10) = %g", m.Name, p0, p10)
		}
		g0, g10 := m.bassDB(c.pot(0)), m.bassDB(c.pot(10))
		if g10-g0 < 8 {
			t.Errorf("%s: faixa do grave em 100 Hz só %.1f dB", m.Name, g10-g0)
		}
		prev := c.pot(0)
		for k := 0.25; k <= 10; k += 0.25 {
			l := c.pot(k)
			if l <= prev {
				t.Fatalf("%s: curva não cresce em %.2f", m.Name, k)
			}
			prev = l
			want := g0 + (g10-g0)*k/10
			if d := math.Abs(m.bassDB(l) - want); d > 0.05 {
				t.Errorf("%s knob %.2f: %.2f dB, esperava %.2f (linear)", m.Name, k, m.bassDB(l), want)
			}
			if kk := m.BassKnob(l); math.Abs(kk-k) > 1e-6 {
				t.Errorf("%s: BassKnob(BassPot(%.2f)) = %.6f", m.Name, k, kk)
			}
		}
		// metade de cima do knob com efeito real (antes: < 1,5 dB de 5 a 10)
		if d := m.bassDB(c.pot(10)) - m.bassDB(c.pot(5)); d < 4 {
			t.Errorf("%s: de 5 a 10 só %.1f dB em 100 Hz", m.Name, d)
		}
		t.Logf("%s: %.1f dB de faixa em 100 Hz; o antigo 5 é o %.2f", m.Name, g10-g0, m.BassKnob(LegacyBassPot))
	}
}
