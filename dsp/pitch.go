package dsp

import (
	"math"
)

// PitchDetector estima a frequência fundamental com o algoritmo YIN
// (de Cheveigné e Kawahara, 2002):
//
//  1. função de diferença d(τ) = Σ (x[j] − x[j+τ])²
//  2. normalização pela média acumulada: d'(τ) = d(τ)·τ / Σ_{k≤τ} d(k)  (d'(0) = 1)
//  3. o período é o primeiro mínimo local de d' abaixo do limiar (evita erros de oitava,
//     comuns em guitarra, cujo 2º harmônico muitas vezes é mais forte que a fundamental)
//  4. interpolação parabólica em torno do mínimo para precisão abaixo de uma amostra
//
// Detect aloca nada: os buffers são criados em NewPitchDetector. Não é para a thread de
// áudio (custa ~W·τmax operações): roda numa goroutine separada.
type PitchDetector struct {
	fs             float64
	minTau, maxTau int
	window         int
	threshold      float64
	d              []float64
}

// NewPitchDetector cria um detector para fundamentais entre minHz e maxHz, com janela de
// window amostras.
func NewPitchDetector(fs, minHz, maxHz float64, window int) *PitchDetector {
	maxTau := int(math.Ceil(fs / minHz))
	return &PitchDetector{
		fs:        fs,
		minTau:    max(2, int(fs/maxHz)),
		maxTau:    maxTau,
		window:    window,
		threshold: 0.12,
		d:         make([]float64, maxTau+2),
	}
}

// Need é quantas amostras Detect precisa (janela + maior período).
func (p *PitchDetector) Need() int { return p.window + p.maxTau + 1 }

// Detect estima a fundamental nas primeiras Need() amostras de x.
// clarity vai de 0 a 1 (1 = periódico perfeito); ok = false se não achou altura confiável
// ou se o sinal está baixo demais.
func (p *PitchDetector) Detect(x []float64) (freq, clarity float64, ok bool) {
	if len(x) < p.Need() {
		return 0, 0, false
	}
	// sinal baixo demais: sem nota (−60 dBFS RMS)
	e := 0.0
	for _, v := range x[:p.window] {
		e += v * v
	}
	if math.Sqrt(e/float64(p.window)) < 1e-3 {
		return 0, 0, false
	}

	d := p.d
	w := p.window
	for tau := 1; tau <= p.maxTau+1; tau++ {
		s := 0.0
		a, b := x[:w], x[tau:tau+w]
		for j := range a {
			diff := a[j] - b[j]
			s += diff * diff
		}
		d[tau] = s
	}
	// normalização pela média acumulada (in-place)
	d[0] = 1
	sum := 0.0
	for tau := 1; tau <= p.maxTau+1; tau++ {
		sum += d[tau]
		if sum == 0 {
			d[tau] = 1
		} else {
			d[tau] *= float64(tau) / sum
		}
	}

	// primeiro mínimo local abaixo do limiar; senão, o mínimo global
	best := -1
	for tau := p.minTau; tau <= p.maxTau; tau++ {
		if d[tau] < p.threshold {
			for tau+1 <= p.maxTau && d[tau+1] < d[tau] {
				tau++
			}
			best = tau
			break
		}
	}
	if best < 0 {
		best = p.minTau
		for tau := p.minTau + 1; tau <= p.maxTau; tau++ {
			if d[tau] < d[best] {
				best = tau
			}
		}
		if d[best] > 0.35 {
			return 0, 1 - d[best], false
		}
	}

	// interpolação parabólica
	t := float64(best)
	if best > 1 && best < p.maxTau+1 {
		y0, y1, y2 := d[best-1], d[best], d[best+1]
		if den := y0 - 2*y1 + y2; den != 0 {
			t += 0.5 * (y0 - y2) / den
		}
	}
	return p.fs / t, 1 - d[best], true
}

// noteNames usa sustenidos, como a maioria dos afinadores.
var noteNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// NoteOf converte uma frequência em nota (nome e oitava científica, A4 = a4) e o desvio em
// cents (−50..+50) até ela.
func NoteOf(freq, a4 float64) (name string, octave int, cents float64) {
	midi := 69 + 12*math.Log2(freq/a4)
	n := math.Round(midi)
	cents = 100 * (midi - n)
	m := int(n)
	return noteNames[((m%12)+12)%12], m/12 - 1, cents
}

// NoteFreq devolve a frequência exata da nota MIDI m (A4 = 69) para a referência a4.
func NoteFreq(m int, a4 float64) float64 { return a4 * math.Pow(2, float64(m-69)/12) }
