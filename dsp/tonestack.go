package dsp

import "math"

// EQBand é uma banda de equalização cujo ganho pode mudar ao vivo.
// O ganho é suavizado e os coeficientes são recalculados a cada 16 amostras
// enquanto o knob se move (calcular cos/sin por amostra seria caro à toa).
type EQBand struct {
	fs, freq, q float64
	design      func(fs, f, q, gainDB float64) Coefs
	f           Biquad
	gain        Smoother
	applied     float64
	count       int
}

func newEQBand(fs, freq, q float64, design func(fs, f, q, gainDB float64) Coefs) EQBand {
	b := EQBand{fs: fs, freq: freq, q: q, design: design, gain: NewSmoother(fs, 30)}
	b.gain.Snap(0)
	b.f.SetCoefs(design(fs, freq, q, 0))
	return b
}

// SetGainDB define o ganho da banda.
func (b *EQBand) SetGainDB(db float64) { b.gain.SetTarget(db) }

func (b *EQBand) process(x float64) float64 {
	g := b.gain.Next()
	if b.count == 0 && g != b.applied {
		b.f.SetCoefs(b.design(b.fs, b.freq, b.q, g))
		b.applied = g
	}
	b.count = (b.count + 1) & 15
	return b.f.Process(x)
}

// ToneStack tem os controles Grave, Médio e Agudo (knobs 0..10, 5 = plano).
//
// Versão inicial com biquads independentes:
//
//	Grave: low shelf  em 100 Hz, ±12 dB
//	Médio: peaking    em 700 Hz, ±12 dB
//	Agudo: high shelf em 3 kHz,  ±12 dB
//
// (Fase 5 do plano: trocar pelo modelo do tone stack Fender/Marshall, em que os controles interagem.)
type ToneStack struct {
	bass, mid, treble EQBand
}

// NewToneStack cria o tone stack.
func NewToneStack(fs float64) *ToneStack {
	return &ToneStack{
		bass:   newEQBand(fs, 100, math.Sqrt2/2, LowShelf),
		mid:    newEQBand(fs, 700, 0.7, Peaking),
		treble: newEQBand(fs, 3000, math.Sqrt2/2, HighShelf),
	}
}

// KnobToDB converte um knob 0..10 (5 = plano) para ±rangeDB.
func KnobToDB(knob, rangeDB float64) float64 { return (knob - 5) / 5 * rangeDB }

// Set ajusta os três knobs (0..10).
func (t *ToneStack) Set(bass, mid, treble float64) {
	t.bass.SetGainDB(KnobToDB(bass, 12))
	t.mid.SetGainDB(KnobToDB(mid, 12))
	t.treble.SetGainDB(KnobToDB(treble, 12))
}

// Reset zera o estado dos filtros.
func (t *ToneStack) Reset() {
	t.bass.f.Reset()
	t.mid.f.Reset()
	t.treble.f.Reset()
}

// ProcessBlock processa in-place.
func (t *ToneStack) ProcessBlock(buf []float64) {
	for i, x := range buf {
		buf[i] = t.treble.process(t.mid.process(t.bass.process(x)))
	}
}

// Presence imita o controle de presença do power amp: um sino largo em 4 kHz, ±10 dB.
type Presence struct{ band EQBand }

// NewPresence cria o controle de presença.
func NewPresence(fs float64) *Presence {
	return &Presence{band: newEQBand(fs, 4000, 0.9, Peaking)}
}

// Set ajusta o knob (0..10, 5 = plano).
func (p *Presence) Set(knob float64) { p.band.SetGainDB(KnobToDB(knob, 10)) }

// Reset zera o estado.
func (p *Presence) Reset() { p.band.f.Reset() }

// ProcessBlock processa in-place.
func (p *Presence) ProcessBlock(buf []float64) {
	for i, x := range buf {
		buf[i] = p.band.process(x)
	}
}
