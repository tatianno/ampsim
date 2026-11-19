package dsp

import "math"

// ---------------------------------------------------------------- Tremolo

// Formas de onda do tremolo.
const (
	WaveSine = iota
	WaveTriangle
	WaveSquare
)

// Tremolo varia o volume com um LFO: ganho entre 1 − depth e 1.
type Tremolo struct {
	fs          float64
	phase       float64 // ciclos, [0, 1)
	rate, depth Smoother
	shape       int
	gain        float64 // ganho suavizado (~1,5 ms): sem estalo na troca de forma
	gcoef       float64
}

// NewTremolo cria um tremolo.
func NewTremolo(fs float64) *Tremolo {
	t := &Tremolo{fs: fs, gain: 1, gcoef: 1 - math.Exp(-1/(0.0015*fs))}
	t.rate.SetTime(fs, 20)
	t.depth.SetTime(fs, 20)
	return t
}

// Set ajusta rate (Hz), depth (0..1) e a forma (WaveSine, WaveTriangle, WaveSquare).
func (t *Tremolo) Set(rate, depth float64, shape int) {
	t.rate.SetTarget(rate)
	t.depth.SetTarget(depth)
	t.shape = shape
}

// Reset volta a fase e o ganho para o início.
func (t *Tremolo) Reset() { t.phase, t.gain = 0, 1 }

// SyncLFO põe a fase na de src mais offset ciclos (estéreo: com 0,5 vira auto-pan).
func (t *Tremolo) SyncLFO(src *Tremolo, offset float64) { t.phase = wrap1(src.phase + offset) }

// squareK define o quão "quadrada" é a onda quadrada (tanh de um seno).
const squareK = 4.0

var squareNorm = 1 / math.Tanh(squareK)

func wave(shape int, p float64) float64 {
	switch shape {
	case WaveTriangle:
		return 1 - 4*math.Abs(p-0.5) // −1 em p = 0, +1 em p = 0,5
	case WaveSquare:
		return math.Tanh(squareK*math.Sin(2*math.Pi*p)) * squareNorm
	}
	return math.Sin(2 * math.Pi * p)
}

// ProcessBlock processa in-place.
func (t *Tremolo) ProcessBlock(buf []float64) {
	for i, x := range buf {
		l := wave(t.shape, t.phase)
		t.phase += t.rate.Next() / t.fs
		if t.phase >= 1 {
			t.phase -= math.Floor(t.phase)
		}
		g := 1 - t.depth.Next()*(1-l)/2
		t.gain += t.gcoef * (g - t.gain)
		buf[i] = x * t.gain
	}
}

// ---------------------------------------------------------------- Vibrato

// VibratoMaxMs é a maior profundidade do vibrato (amplitude do atraso).
const VibratoMaxMs = 2.5

// vibratoMinDelay é o menor atraso (amostras): a interpolação cúbica precisa de ≥ 2.
const vibratoMinDelay = 2.0

// Vibrato varia o tom com uma linha de atraso modulada, 100% molhado (sem o sinal seco).
// O atraso vai de 2 amostras a 2 + 2·depth; o desvio máximo de tom é ≈ 2π·rate·depth.
type Vibrato struct {
	fs          float64
	line        *DelayLine
	lfo         LFO
	rate, depth Smoother // Hz, ms
}

// NewVibrato cria um vibrato.
func NewVibrato(fs float64) *Vibrato {
	v := &Vibrato{fs: fs, line: NewDelayLine(int(2*VibratoMaxMs/1000*fs) + 8)}
	v.rate.SetTime(fs, 20)
	v.depth.SetTime(fs, 20)
	return v
}

// Set ajusta rate (Hz) e depth (ms, até VibratoMaxMs).
func (v *Vibrato) Set(rate, depthMs float64) {
	v.rate.SetTarget(rate)
	v.depth.SetTarget(math.Min(depthMs, VibratoMaxMs))
}

// Reset zera o estado.
func (v *Vibrato) Reset() { v.line.Reset(); v.lfo.Reset() }

// ProcessBlock processa in-place.
func (v *Vibrato) ProcessBlock(buf []float64) {
	for i, x := range buf {
		lfo := v.lfo.Next(v.rate.Next(), v.fs)
		d := vibratoMinDelay + v.depth.Next()/1000*v.fs*(1+lfo)
		y := v.line.ReadCubic(d)
		v.line.Write(x)
		buf[i] = y
	}
}
