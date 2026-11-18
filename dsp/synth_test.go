package dsp

import (
	"math"
	"testing"
)

// resposta mede o ganho (dB) do filtro fixo (sem modulação) num seno de frequência f.
func synthResp(ftype int, cutoff, res, f float64) float64 {
	const fs = 48000.0
	s := NewSynthFilter(fs)
	s.Set(SynthEnvelope, ftype, cutoff, res, 0, 0, 1, 0, 0, 1, 0)
	x := senoMod(f, fs, 24000)
	for i := range x {
		x[i] *= 0.05 // nível baixo: região linear
	}
	s.ProcessBlock(x)
	return 20 * math.Log10(rmsOf(x[12000:])/(0.05/math.Sqrt2))
}

func TestSynthFiltroFixo(t *testing.T) {
	// passa-baixa em 1 kHz: passa os graves, corta ~24 dB/oitava
	if g := synthResp(SynthLP, 1000, 0, 150); math.Abs(g) > 3 {
		t.Errorf("LP: 150 Hz com %.1f dB", g)
	}
	if g := synthResp(SynthLP, 1000, 0, 4000); g > -25 {
		t.Errorf("LP: 4 kHz com %.1f dB (2 oitavas acima do corte)", g)
	}
	// passa-alta: o contrário
	if g := synthResp(SynthHP, 1000, 0, 6000); math.Abs(g) > 3 {
		t.Errorf("HP: 6 kHz com %.1f dB", g)
	}
	if g := synthResp(SynthHP, 1000, 0, 200); g > -20 {
		t.Errorf("HP: 200 Hz com %.1f dB", g)
	}
	// passa-banda: pico perto do corte
	if pk, lo := synthResp(SynthBP, 1000, 0.3, 1000), synthResp(SynthBP, 1000, 0.3, 150); pk < lo+10 {
		t.Errorf("BP: 1 kHz %.1f dB × 150 Hz %.1f dB", pk, lo)
	}
	// ressonância: pico no corte bem acima dos graves, e os graves sem salto de volume
	if pk, lo := synthResp(SynthLP, 1000, 0.9, 1000), synthResp(SynthLP, 1000, 0.9, 150); pk < lo+6 || math.Abs(lo) > 3 {
		t.Errorf("ressonância: 1 kHz %.1f dB × 150 Hz %.1f dB", pk, lo)
	}
	t.Logf("LP 150 Hz %.1f dB; LP res 0,9: 150 Hz %.1f dB, 1 kHz %.1f dB", synthResp(SynthLP, 1000, 0, 150),
		synthResp(SynthLP, 1000, 0.9, 150), synthResp(SynthLP, 1000, 0.9, 1000))
}

func TestSynthModulacao(t *testing.T) {
	const fs = 48000.0
	// sequência: o corte segue os passos, no ritmo (8 passos/s, 2 oitavas de profundidade)
	s := NewSynthFilter(fs)
	s.Set(SynthSequence, SynthLP, 200, 0.5, 2, 0, 8, 0, 0, 1, 0)
	s.SetSteps([SynthSteps]float64{0, 1, 0.5, 0, 0, 0, 0, 0}, 0)
	buf := make([]float64, int(fs/8)) // um passo
	s.ProcessBlock(buf[:len(buf)/2])  // mede no meio de cada passo, longe das fronteiras
	var fcs []float64
	for k := 0; k < 4; k++ {
		if k == 0 {
			fcs = append(fcs, s.CutoffHz())
			continue
		}
		s.ProcessBlock(buf)
		fcs = append(fcs, s.CutoffHz())
	}
	want := []float64{200, 800, 400, 200} // passos 0..3: 200·2^(2·valor)
	for i, w := range want {
		if math.Abs(fcs[i]-w) > 0.05*w {
			t.Errorf("passo %d: corte %.0f Hz, esperava %.0f", i, fcs[i], w)
		}
	}
	// envelope: palhetada forte abre mais
	abre := func(amp float64) float64 {
		s := NewSynthFilter(fs)
		s.Set(SynthEnvelope, SynthLP, 200, 0.5, 4, 0.8, 1, 0, 0, 1, 0)
		x := senoMod(110, fs, 4800)
		for i := range x {
			x[i] *= amp
		}
		s.ProcessBlock(x)
		return s.CutoffHz()
	}
	if fraca, forte := abre(0.01), abre(0.3); forte < 3*fraca {
		t.Errorf("envelope: %.0f Hz (fraca) × %.0f Hz (forte)", fraca, forte)
	}
	// FM e ressonância alta: continua finito e limitado
	s = NewSynthFilter(fs)
	s.Set(SynthLFO, SynthLP, 500, 1, 3, 0, 3, 1, 12, 1, 0)
	x := senoMod(110, fs, int(fs))
	for i := range x {
		x[i] *= 0.4
	}
	s.ProcessBlock(x)
	finite(t, "synth", x)
}
