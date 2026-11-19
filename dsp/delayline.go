package dsp

import "math"

// DelayLine é um buffer circular com leitura fracionária (interpolação linear).
//
// Convenção: Read(d) devolve o valor de d amostras atrás do instante ATUAL, onde o
// instante atual é o da próxima chamada a Write. Por isso Read(d) pode ser chamado
// antes de Write na mesma amostra (necessário quando há realimentação).
type DelayLine struct {
	buf  []float64
	mask int
	w    int // posição da próxima escrita
}

// NewDelayLine cria uma linha para atrasos de até maxDelay amostras.
func NewDelayLine(maxDelay int) *DelayLine {
	n := 1
	for n < maxDelay+2 {
		n <<= 1
	}
	return &DelayLine{buf: make([]float64, n), mask: n - 1}
}

// MaxDelay devolve o maior atraso suportado.
func (d *DelayLine) MaxDelay() int { return len(d.buf) - 2 }

// Write escreve a amostra do instante atual e avança o tempo.
func (d *DelayLine) Write(x float64) {
	d.buf[d.w] = x
	d.w = (d.w + 1) & d.mask
}

// ReadInt lê com atraso inteiro (d ≥ 1).
func (d *DelayLine) ReadInt(delay int) float64 {
	return d.buf[(d.w-delay)&d.mask]
}

// Read lê com atraso fracionário (delay ≥ 1).
func (d *DelayLine) Read(delay float64) float64 {
	di := math.Floor(delay)
	f := delay - di
	i := int(di)
	a := d.buf[(d.w-i)&d.mask]   // x[t-i]
	b := d.buf[(d.w-i-1)&d.mask] // x[t-i-1]
	return a*(1-f) + b*f
}

// ReadCubic lê com atraso fracionário por interpolação cúbica (Hermite de 4 pontos), que
// não abafa os agudos como a linear quando o atraso varia (vibrato). delay ≥ 2.
func (d *DelayLine) ReadCubic(delay float64) float64 {
	di := math.Floor(delay)
	f := delay - di
	i := int(di)
	xm1 := d.buf[(d.w-i+1)&d.mask] // x[t-i+1]
	x0 := d.buf[(d.w-i)&d.mask]    // x[t-i]
	x1 := d.buf[(d.w-i-1)&d.mask]  // x[t-i-1]
	x2 := d.buf[(d.w-i-2)&d.mask]  // x[t-i-2]
	c1 := 0.5 * (x1 - xm1)
	c2 := xm1 - 2.5*x0 + 2*x1 - 0.5*x2
	c3 := 0.5*(x2-xm1) + 1.5*(x0-x1)
	return ((c3*f+c2)*f+c1)*f + x0
}

// Reset zera o buffer.
func (d *DelayLine) Reset() {
	clear(d.buf)
	d.w = 0
}

// LFO senoidal com acumulador de fase.
type LFO struct {
	phase float64 // em ciclos, [0, 1)
}

// Next devolve sin(2π·fase) e avança rate/fs ciclos.
func (l *LFO) Next(rate, fs float64) float64 {
	v := math.Sin(2 * math.Pi * l.phase)
	l.phase += rate / fs
	if l.phase >= 1 {
		l.phase -= math.Floor(l.phase)
	}
	return v
}

// Reset volta a fase para zero.
func (l *LFO) Reset() { l.phase = 0 }

// Phase devolve a fase atual (ciclos, [0, 1)).
func (l *LFO) Phase() float64 { return l.phase }

// SetPhase põe a fase em p ciclos (qualquer valor; fica em [0, 1)).
func (l *LFO) SetPhase(p float64) { l.phase = wrap1(p) }

// wrap1 leva p para [0, 1).
func wrap1(p float64) float64 { return p - math.Floor(p) }
