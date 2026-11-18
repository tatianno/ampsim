package dsp

import "math"

// Modos do wah.
const (
	WahEnvelope = iota // segue a força da palhetada (auto-wah / envelope filter)
	WahLFO             // varre sozinho
	WahFixed           // parado numa posição ("wah parado")
)

// Faixa do wah.
const (
	WahMinHz = 350.0
	WahMaxLo = 1200.0 // topo da varredura com Faixa em 0
	WahMaxHi = 3500.0 // topo da varredura com Faixa em 100%
)

// SVF é um filtro de estado variável (topologia TPT, de Andrew Simper): estável mesmo com
// a frequência mudando a cada amostra. Dá as saídas passa-banda e passa-baixa.
type SVF struct {
	ic1, ic2 float64
}

// Process filtra uma amostra com corte fc e Q; devolve (passa-banda, passa-baixa).
func (f *SVF) Process(x, fc, q, fs float64) (bp, lp float64) {
	g := math.Tan(math.Pi * math.Min(fc, 0.45*fs) / fs)
	k := 1 / q
	a1 := 1 / (1 + g*(g+k))
	a2 := g * a1
	a3 := g * a2
	v3 := x - f.ic2
	v1 := a1*f.ic1 + a2*v3
	v2 := f.ic2 + a2*f.ic1 + a3*v3
	f.ic1 = 2*v1 - f.ic1
	f.ic2 = 2*v2 - f.ic2
	return v1, v2
}

// Reset zera o estado.
func (f *SVF) Reset() { f.ic1, f.ic2 = 0, 0 }

// Wah é o wah automático: um passa-banda ressonante que varre de 350 Hz até o topo da Faixa,
// guiado pelo envelope da entrada, por um LFO ou por uma posição fixa.
type Wah struct {
	fs   float64
	svf  SVF
	lfo  LFO
	env  float64 // envelope da entrada
	pos  float64 // posição suavizada (0..1)
	posK float64

	mode                     int
	sens, top, q, mix, rate  Smoother
	fixed                    Smoother
	attackK, releaseK, relMs float64
	fc                       float64 // última frequência central (para a GUI e os testes)
}

// NewWah cria o wah.
func NewWah(fs float64) *Wah {
	w := &Wah{fs: fs, posK: 1 - math.Exp(-1/(0.002*fs)), attackK: 1 - math.Exp(-1/(0.004*fs))}
	for _, s := range []*Smoother{&w.sens, &w.top, &w.q, &w.mix, &w.rate, &w.fixed} {
		s.SetTime(fs, 20)
	}
	w.SetResponse(150)
	return w
}

// Set ajusta o modo, a sensibilidade (0..1), a faixa (0..1), o Q, o rate do LFO (Hz), a
// posição fixa (0..1) e o mix (0..1).
func (w *Wah) Set(mode int, sens, faixa, q, rate, fixed, mix float64) {
	w.mode = mode
	w.sens.SetTarget(sens)
	w.top.SetTarget(WahMaxLo * math.Pow(WahMaxHi/WahMaxLo, faixa))
	w.q.SetTarget(q)
	w.rate.SetTarget(rate)
	w.fixed.SetTarget(fixed)
	w.mix.SetTarget(mix)
}

// SetResponse ajusta o tempo de volta do envelope (ms): curto = "quac" rápido.
func (w *Wah) SetResponse(ms float64) {
	if ms != w.relMs {
		w.relMs = ms
		w.releaseK = 1 - math.Exp(-1/(ms/1000*w.fs))
	}
}

// Reset zera o estado.
func (w *Wah) Reset() { w.svf.Reset(); w.lfo.Reset(); w.env, w.pos = 0, 0 }

// CenterHz devolve a frequência central atual.
func (w *Wah) CenterHz() float64 { return w.fc }

// ProcessBlock processa in-place.
func (w *Wah) ProcessBlock(buf []float64) {
	for i, x := range buf {
		a := math.Abs(x)
		if a > w.env {
			w.env += w.attackK * (a - w.env)
		} else {
			w.env += w.releaseK * (a - w.env)
		}
		sens, rate, fixed := w.sens.Next(), w.rate.Next(), w.fixed.Next()
		lfo := w.lfo.Next(rate, w.fs)
		var p float64
		switch w.mode {
		case WahLFO:
			p = 0.5 * (1 + lfo)
		case WahFixed:
			p = fixed
		default: // envelope: com sensibilidade máxima, uma palhetada de −20 dBFS abre tudo
			p = math.Min(1, w.env*sens*12)
		}
		w.pos += w.posK * (p - w.pos)
		w.fc = WahMinHz * math.Pow(w.top.Next()/WahMinHz, w.pos)
		q := w.q.Next()
		bp, _ := w.svf.Process(x, w.fc, q, w.fs)
		wet := 3.5 * bp / q // passa-banda com +11 dB no pico: no geral, perto do nível da entrada
		m := w.mix.Next()
		buf[i] = (1-m)*x + m*wet
	}
}
