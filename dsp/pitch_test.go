package dsp

import (
	"math"
	"math/rand"
	"testing"
)

func cents(f, ref float64) float64 { return 1200 * math.Log2(f/ref) }

func novoDetector() *PitchDetector { return NewPitchDetector(48000, 60, 1400, 2048) }

func TestPitchSenos(t *testing.T) {
	p := novoDetector()
	for _, f := range []float64{61.74, 82.41, 110, 146.83, 196, 246.94, 329.63, 440, 659.26, 987.77} {
		x := seno(48000, f, 0.3, p.Need())
		got, clarity, ok := p.Detect(x)
		if !ok || math.Abs(cents(got, f)) > 0.5 {
			t.Errorf("%.2f Hz: detectou %.3f Hz (%.2f cents, ok=%v, clareza %.2f)", f, got, cents(got, f), ok, clarity)
		}
	}
}

// Corda grave de guitarra: harmônicos fortes, o 2º mais forte que a fundamental.
// Um detector ingênuo erra a oitava (diria 164,8 Hz); o YIN tem que dar 82,41 Hz.
func TestPitchHarmonicosSemErroDeOitava(t *testing.T) {
	p := novoDetector()
	const f = 82.41
	x := make([]float64, p.Need())
	amps := []float64{0.5, 1.0, 0.7, 0.5, 0.35, 0.25, 0.15} // harmônicos 1..7
	for i := range x {
		tm := float64(i) / 48000
		for h, a := range amps {
			x[i] += 0.1 * a * math.Sin(2*math.Pi*f*float64(h+1)*tm+float64(h))
		}
	}
	got, _, ok := p.Detect(x)
	if !ok || math.Abs(cents(got, f)) > 1 {
		t.Errorf("detectou %.2f Hz (%.1f cents), esperava %.2f Hz", got, cents(got, f), f)
	}
}

func TestPitchRuidoESilencio(t *testing.T) {
	p := novoDetector()
	if _, _, ok := p.Detect(make([]float64, p.Need())); ok {
		t.Error("silêncio não tem nota")
	}
	rng := rand.New(rand.NewSource(3))
	x := make([]float64, p.Need())
	for i := range x {
		x[i] = 0.3 * rng.NormFloat64()
	}
	if f, c, ok := p.Detect(x); ok {
		t.Errorf("ruído branco não deveria ter nota (deu %.1f Hz, clareza %.2f)", f, c)
	}
}

func TestNoteOf(t *testing.T) {
	casos := []struct {
		f, a4  float64
		name   string
		octave int
		cents  float64
	}{
		{440, 440, "A", 4, 0},
		{82.4069, 440, "E", 2, 0},
		{329.628, 440, "E", 4, 0},
		{440 * math.Pow(2, 10.0/1200), 440, "A", 4, 10},
		{440 * math.Pow(2, -30.0/1200), 440, "A", 4, -30},
		{261.626, 440, "C", 4, 0},
		{432, 432, "A", 4, 0},
		{440, 432, "A", 4, 1200 * math.Log2(440.0/432)},
	}
	for _, c := range casos {
		n, o, ct := NoteOf(c.f, c.a4)
		if n != c.name || o != c.octave || math.Abs(ct-c.cents) > 0.05 {
			t.Errorf("NoteOf(%.3f, A4=%v) = %s%d %+.2f, esperava %s%d %+.2f", c.f, c.a4, n, o, ct, c.name, c.octave, c.cents)
		}
	}
	if f := NoteFreq(40, 440); math.Abs(f-82.4069) > 0.001 {
		t.Errorf("Mi2 = %.4f Hz", f)
	}
}
