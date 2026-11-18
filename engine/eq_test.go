package engine

import (
	"math"
	"testing"
)

// A resposta calculada bate com a medida (seno puro pelo EQ) e cada banda faz o que promete.
func TestEQResposta(t *testing.T) {
	const fs = 48000.0
	m := NewEQ()
	set := func(id string, v float64) { m.Param(id).Set(v) }
	set("g1", 6)   // shelf de graves +6 dB em 100 Hz
	set("f3", 800) // pico −9 dB em 800 Hz, Q 2
	set("g3", -9)
	set("q3", 2)
	set("g5", -6) // shelf de agudos −6 dB em 6 kHz
	set("level", 2)
	m.Prepare(fs, 512)

	freqs := []float64{30, 800, 15000, 3000}
	calc := make([]float64, len(freqs))
	m.ResponseDB(freqs, calc)
	want := []float64{6 + 2, -9 + 2, -6 + 2}
	for i, w := range want {
		if math.Abs(calc[i]-w) > 0.6 {
			t.Errorf("%g Hz: %.2f dB, esperava ~%.1f", freqs[i], calc[i], w)
		}
	}
	for i, f := range freqs {
		m.Reset()
		n := int(fs) // 1 s; mede o último terço
		x := make([]float64, n)
		for k := range x {
			x[k] = math.Sin(2 * math.Pi * f * float64(k) / fs)
		}
		for k := 0; k < n; k += 512 {
			m.Process(x[k:min(k+512, n)])
		}
		rms := 0.0
		for _, v := range x[2*n/3:] {
			rms += v * v
		}
		got := 10*math.Log10(rms/float64(n/3)) + 3.0103 // seno: rms = pico/√2
		if math.Abs(got-calc[i]) > 0.2 {
			t.Errorf("%g Hz: medido %.2f dB, calculado %.2f dB", f, got, calc[i])
		}
	}
}

// Com tudo em 0 dB, o EQ não muda o sinal.
func TestEQPlanoETransparente(t *testing.T) {
	m := NewEQ()
	m.Prepare(48000, 256)
	x := make([]float64, 256)
	for i := range x {
		x[i] = math.Sin(float64(i) * 0.37)
	}
	y := append([]float64(nil), x...)
	m.Process(y)
	for i := range x {
		if math.Abs(y[i]-x[i]) > 1e-9 {
			t.Fatalf("amostra %d: %g × %g", i, y[i], x[i])
		}
	}
}

func TestEQSemAlocacao(t *testing.T) {
	c := NewDefaultChain()
	c.SetEnabled("eq", true)
	c.Prepare(48000, 256)
	eq := c.Module("eq")
	buf := make([]float64, 256)
	k := 0
	if a := testing.AllocsPerRun(60, func() {
		k++
		eq.Param("f3").Set(200 + float64(k*50))
		eq.Param("g2").Set(float64(k%7) - 3)
		eq.Param("q4").Set(0.5 + float64(k%5))
		c.ProcessMono(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}

func TestParametroLog(t *testing.T) {
	p := NewParam(ParamInfo{ID: "f", Min: 100, Max: 10000, Default: 1000, Log: true})
	if n := p.Normalized(); math.Abs(n-0.5) > 1e-12 {
		t.Errorf("1 kHz entre 100 Hz e 10 kHz deveria ficar no meio do knob: %g", n)
	}
	p.SetNormalized(0.25)
	if v := p.Get(); math.Abs(v-math.Sqrt(10)*100) > 1e-9 {
		t.Errorf("0,25 do knob = %g Hz", v)
	}
}
