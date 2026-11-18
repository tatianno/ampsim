package dsp

import (
	"math"
	"math/rand"
	"testing"
)

func TestConvolverContraDireta(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, irLen := range []int{1, 63, 64, 65, 1000, 7200} {
		ir := make([]float64, irLen)
		for i := range ir {
			ir[i] = rng.NormFloat64() * math.Exp(-float64(i)/500)
		}
		x := make([]float64, 5000)
		for i := range x {
			x[i] = rng.NormFloat64()
		}
		want := make([]float64, len(x))
		for n := range want {
			for j := 0; j < irLen && j <= n; j++ {
				want[n] += ir[j] * x[n-j]
			}
		}
		for _, bloco := range []int{1, 37, 64, 512} {
			c := NewConvolver(ir, 64)
			got := append([]float64(nil), x...)
			emBlocos(got, bloco, c.ProcessBlock)
			if e := erroDB(got, want); e > -100 {
				t.Errorf("IR %d, bloco %d: erro %.1f dB", irLen, bloco, e)
			}
		}
	}
}

// Sem não linearidade, subir e descer 4x tem que devolver o sinal atrasado de Latency().
func TestOversamplerLatencia(t *testing.T) {
	const fs = 48000.0
	o := NewOversampler4x()
	lat := o.Latency()
	n := 4800
	x := make([]float64, n)
	for i := range x {
		x[i] = math.Sin(2*math.Pi*1000*float64(i)/fs) + 0.5*math.Sin(2*math.Pi*7000*float64(i)/fs)
	}
	y := make([]float64, n)
	var up [OversampleFactor]float64
	for i, v := range x {
		o.Up(v, &up)
		y[i] = o.Down(&up)
	}
	e := erroDB(y[lat+200:], x[200:n-lat])
	t.Logf("oversampler ida e volta: erro %.1f dB, latência %d", e, lat)
	if e > -50 {
		t.Errorf("erro %.1f dB", e)
	}
}

func TestSemAlocacao(t *testing.T) {
	const fs = 48000.0
	buf := make([]float64, 256)
	for i := range buf {
		buf[i] = 0.3 * math.Sin(float64(i)*0.05)
	}
	ir := make([]float64, 7200)
	ir[0] = 1
	conv := NewConvolver(ir, 64)
	pre := NewPreamp(fs)
	pre.SetDrive(0.5)
	g := NewGate(fs)
	ts := NewToneStack(fs)
	ts.Set(7, 3, 6)
	pr := NewPresence(fs)
	pr.Set(8)
	ch := NewChorus(fs)
	ch.Set(1, 2, 20, 0.5)
	fl := NewFlanger(fs)
	fl.Set(0.3, 0.5, 4, 0.6, 0.5)
	ph := NewPhaser(fs)
	ph.Set(0.5, 200, 1800, 6, 0.5, 0.5)
	de := NewDelay(fs)
	de.Set(350, 0.4, 0.3, 0.3)
	rv := NewReverb(fs)
	rv.Set(0.8, 0.4, 0.3)
	cp := NewCompressor(fs)
	cp.Set(-30, 4, 10, 150, 6, 1)
	blocos := map[string]func([]float64){
		"convolver": conv.ProcessBlock, "preamp": pre.ProcessBlock, "gate": g.ProcessBlock,
		"tonestack": ts.ProcessBlock, "presence": pr.ProcessBlock, "chorus": ch.ProcessBlock,
		"flanger": fl.ProcessBlock, "phaser": ph.ProcessBlock, "delay": de.ProcessBlock,
		"reverb": rv.ProcessBlock, "compressor": cp.ProcessBlock,
	}
	for nome, f := range blocos {
		if a := testing.AllocsPerRun(20, func() { f(buf) }); a != 0 {
			t.Errorf("%s aloca %.0f vezes por bloco", nome, a)
		}
	}
}
