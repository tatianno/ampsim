package dsp

import (
	"math"
	"testing"
)

// goertzel devolve a amplitude da componente de frequência f em x.
func goertzel(x []float64, f, fs float64) float64 {
	w := 2 * math.Pi * f / fs
	c := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s1, s2 = v+c*s1-s2, s1
	}
	re := s1 - s2*math.Cos(w)
	im := s2 * math.Sin(w)
	return 2 * math.Hypot(re, im) / float64(len(x))
}

func rmsOf(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}

func finite(t *testing.T, name string, x []float64) {
	t.Helper()
	for i, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 20 {
			t.Fatalf("%s: valor inválido na amostra %d: %g", name, i, v)
		}
	}
}

func TestDrive(t *testing.T) {
	const fs = 48000.0
	n := int(fs / 2)
	// boost com drive 0, tone no meio: o sinal passa igual, atrasado pela latência
	d := NewDrive(fs)
	d.SetMode(DriveBoost)
	d.Set(0, 5, 0)
	x := senoMod(440, fs, n)
	y := append([]float64(nil), x...)
	d.ProcessBlock(y)
	// (os filtros suaves de 25 Hz e 18 kHz só defasam um pouco: comparamos nível e harmônicos)
	if db := 20 * math.Log10(rmsOf(y[n/2:])/rmsOf(x[n/2:])); math.Abs(db) > 0.2 {
		t.Errorf("boost limpo: nível %+.2f dB", db)
	}
	if h := goertzel(y[n/2:], 880, fs) + goertzel(y[n/2:], 1320, fs); h > 1e-3 {
		t.Errorf("boost limpo não deveria criar harmônicos: %.2g", h)
	}
	// nos modos que saturam: mais drive = mais harmônicos; nível perto do da entrada
	for _, mode := range []int{DriveOverdrive, DriveDistortion, DriveFuzz} {
		harm := func(drive float64) (h3, level float64) {
			d := NewDrive(fs)
			d.SetMode(mode)
			d.Set(drive, 5, 0)
			y := senoMod(220, fs, n)
			for i := range y {
				y[i] *= 0.25 // ~ uma palhetada (−12 dBFS)
			}
			d.ProcessBlock(y)
			finite(t, "drive", y)
			y = y[n/2:]
			f1 := goertzel(y, 220, fs)
			return (goertzel(y, 660, fs) + goertzel(y, 440, fs)) / f1, rmsOf(y) / (0.25 / math.Sqrt2)
		}
		lo, _ := harm(1)
		hi, lvl := harm(9)
		if hi <= lo {
			t.Errorf("modo %d: harmônicos com drive 9 (%.3f) deveriam passar os do drive 1 (%.3f)", mode, hi, lo)
		}
		if db := 20 * math.Log10(lvl); math.Abs(db) > 9 {
			t.Errorf("modo %d: nível %.1f dB em relação à entrada (Level 0 dB)", mode, db)
		}
		t.Logf("modo %d: harmônicos %.3f → %.3f; nível com drive 9: %+.1f dB", mode, lo, hi, 20*math.Log10(lvl))
	}
}

func TestWah(t *testing.T) {
	const fs = 48000.0
	// posição fixa: o pico fica em 350 Hz (pedal "para cima") e no topo da faixa (para baixo)
	pico := func(pos float64) float64 {
		best, bestF := 0.0, 0.0
		for f := 200.0; f < 5000; f *= 1.05 {
			w := NewWah(fs)
			w.Set(WahFixed, 0, 1, 5, 1, pos, 1)
			x := senoMod(f, fs, 4800)
			w.ProcessBlock(x)
			if a := goertzel(x[2400:], f, fs); a > best {
				best, bestF = a, f
			}
		}
		return bestF
	}
	if f := pico(0); f < 300 || f > 420 {
		t.Errorf("posição 0: pico em %.0f Hz, esperava ~350", f)
	}
	if f := pico(1); f < 2900 || f > 4200 {
		t.Errorf("posição 1 (faixa máxima): pico em %.0f Hz, esperava ~3500", f)
	}
	// envelope: palhetada forte abre mais o filtro que a fraca
	centro := func(amp float64) float64 {
		w := NewWah(fs)
		w.Set(WahEnvelope, 0.7, 0.5, 5, 1, 0, 1)
		x := senoMod(220, fs, 2400)
		for i := range x {
			x[i] *= amp
		}
		w.ProcessBlock(x)
		return w.CenterHz()
	}
	if fraca, forte := centro(0.02), centro(0.3); forte <= fraca*1.5 {
		t.Errorf("envelope: centro %.0f Hz (fraca) × %.0f Hz (forte)", fraca, forte)
	}
}

func TestOctaver(t *testing.T) {
	const fs = 48000.0
	n := int(fs)
	run := func(sub1, sub2, up float64) []float64 {
		o := NewOctaver(fs)
		o.Set(sub1, sub2, up, 0)
		x := senoMod(220, fs, n)
		for i := range x {
			x[i] *= 0.3
		}
		o.ProcessBlock(x)
		finite(t, "octaver", x)
		return x[n/4:]
	}
	domina := func(name string, y []float64, f float64, outras ...float64) {
		a := goertzel(y, f, fs)
		for _, g := range outras {
			if b := goertzel(y, g, fs); b >= a {
				t.Errorf("%s: %.0f Hz (%.4f) deveria dominar %.0f Hz (%.4f)", name, f, a, g, b)
			}
		}
	}
	domina("−1 oitava", run(1, 0, 0), 110, 220, 330)
	domina("−2 oitavas", run(0, 1, 0), 55, 110, 165, 220)
	domina("+1 oitava", run(0, 0, 1), 440, 220, 110)
}

func TestRotary(t *testing.T) {
	const fs = 48000.0
	r := NewRotary(fs)
	r.Set(false, 1, 0.5, 1)
	buf := make([]float64, 256)
	r.ProcessBlock(buf)
	if h, d := r.Speeds(); math.Abs(h-rotHornSlow) > 1e-9 || math.Abs(d-rotDrumSlow) > 1e-9 {
		t.Fatalf("lento: %.2f / %.2f Hz", h, d)
	}
	// acelera: depois de 1 s a corneta já está quase no rápido e o tambor ainda não
	r.Set(true, 1, 0.5, 1)
	for i := 0; i < int(fs)/256; i++ {
		r.ProcessBlock(buf)
	}
	h, d := r.Speeds()
	if h < 0.8*rotHornFast || d > 0.6*rotDrumFast {
		t.Errorf("1 s depois de acelerar: corneta %.2f Hz, tambor %.2f Hz", h, d)
	}
	// o volume de um tom agudo pulsa no ritmo da corneta
	r = NewRotary(fs)
	r.Set(true, 1, 1, 1)
	r.Reset()
	x := senoMod(3000, fs, n2(fs))
	r.ProcessBlock(x)
	finite(t, "rotary", x)
	env := make([]float64, 0, len(x)/240)
	for i := len(x) / 2; i+240 <= len(x); i += 240 {
		env = append(env, rmsOf(x[i:i+240]))
	}
	lo, hi := math.Inf(1), 0.0
	for _, v := range env {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if hi/lo < 1.5 {
		t.Errorf("o volume da corneta deveria pulsar: %.3f a %.3f", lo, hi)
	}
}

func n2(fs float64) int { return int(fs) }

func TestVibe(t *testing.T) {
	const fs = 48000.0
	// vibrato: só passa-tudo, o volume de um tom não muda
	v := NewVibe(fs)
	v.Set(3, 1, true)
	x := senoMod(800, fs, int(fs))
	v.ProcessBlock(x)
	finite(t, "vibe", x)
	for i := len(x) / 2; i+480 <= len(x); i += 480 {
		if r := rmsOf(x[i:i+480]) * math.Sqrt2; math.Abs(r-1) > 0.05 {
			t.Fatalf("vibrato: nível %.3f em %d (deveria ficar em 1)", r, i)
		}
	}
	// chorus: somando o seco, aparecem cancelamentos que variam (o volume pulsa)
	v = NewVibe(fs)
	v.Set(3, 1, false)
	x = senoMod(800, fs, int(fs))
	v.ProcessBlock(x)
	lo, hi := math.Inf(1), 0.0
	for i := len(x) / 2; i+480 <= len(x); i += 480 {
		r := rmsOf(x[i : i+480])
		lo, hi = math.Min(lo, r), math.Max(hi, r)
	}
	if hi/lo < 2 {
		t.Errorf("chorus: o volume deveria variar com a fase (%.3f a %.3f)", lo, hi)
	}
}

func TestPitchShifter(t *testing.T) {
	const fs = 48000.0
	for _, c := range []struct {
		semi   float64
		want   float64
		outras []float64
	}{{12, 880, []float64{440}}, {-12, 220, []float64{440}}, {7, 440 * math.Pow(2, 7.0/12), []float64{440}}, {0, 440, []float64{880, 220}}} {
		p := NewPitchShifter(fs)
		p.Set(Semitones(c.semi, 0), 1, 0, 1)
		x := senoMod(440, fs, int(fs))
		p.ProcessBlock(x)
		finite(t, "pitch", x)
		y := x[len(x)/4:]
		a := goertzel(y, c.want, fs)
		for _, g := range c.outras {
			if b := goertzel(y, g, fs); b > a/4 {
				t.Errorf("%+g semitons: %.0f Hz (%.3f) deveria dominar %.0f Hz (%.3f)", c.semi, c.want, a, g, b)
			}
		}
		if a < 0.6 {
			t.Errorf("%+g semitons: amplitude %.3f em %.0f Hz", c.semi, a, c.want)
		}
	}
}
