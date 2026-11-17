package dsp

import "math"

// Os efeitos abaixo são portes em streaming de referencia_python/*.py.
// Com parâmetros constantes, a saída é a mesma do Python (sem o np.clip final,
// que num pedal real não existe: o clip fica para o fim da cadeia).
// Cada parâmetro é suavizado para poder girar o knob ao vivo.

// ---------------------------------------------------------------- Chorus

// Chorus: linha de atraso modulada por LFO, sem realimentação.
type Chorus struct {
	fs                float64
	line              *DelayLine
	lfo               LFO
	rate, depth, base Smoother // Hz, ms, ms
	mix               Smoother
}

// NewChorus cria um chorus (atraso máximo 50 ms).
func NewChorus(fs float64) *Chorus {
	c := &Chorus{fs: fs, line: NewDelayLine(int(0.05 * fs))}
	for _, s := range []*Smoother{&c.rate, &c.depth, &c.base, &c.mix} {
		s.SetTime(fs, 20)
	}
	return c
}

// Set ajusta os parâmetros: rate em Hz, depth e delay em ms, mix 0..1.
func (c *Chorus) Set(rate, depthMs, delayMs, mix float64) {
	c.rate.SetTarget(rate)
	c.depth.SetTarget(depthMs)
	c.base.SetTarget(delayMs)
	c.mix.SetTarget(mix)
}

// Reset zera o estado.
func (c *Chorus) Reset() { c.line.Reset(); c.lfo.Reset() }

// ProcessBlock processa in-place.
func (c *Chorus) ProcessBlock(buf []float64) {
	maxD := float64(c.line.MaxDelay() - 1)
	for i, x := range buf {
		lfo := c.lfo.Next(c.rate.Next(), c.fs)
		d := (c.base.Next() + c.depth.Next()*lfo) / 1000 * c.fs
		d = math.Min(maxD, math.Max(1, d))
		wet := c.line.Read(d)
		c.line.Write(x)
		m := c.mix.Next()
		buf[i] = (1-m)*x + m*wet
	}
}

// ---------------------------------------------------------------- Flanger

// Flanger: atraso curto modulado com realimentação.
type Flanger struct {
	fs                        float64
	line                      *DelayLine
	lfo                       LFO
	rate, minMs, maxMs, fb, m Smoother
}

// NewFlanger cria um flanger (atraso máximo 20 ms).
func NewFlanger(fs float64) *Flanger {
	f := &Flanger{fs: fs, line: NewDelayLine(int(0.02 * fs))}
	for _, s := range []*Smoother{&f.rate, &f.minMs, &f.maxMs, &f.fb, &f.m} {
		s.SetTime(fs, 20)
	}
	return f
}

// Set ajusta os parâmetros: rate em Hz, atrasos mínimo/máximo em ms, feedback -1..1, mix 0..1.
func (f *Flanger) Set(rate, minMs, maxMs, feedback, mix float64) {
	f.rate.SetTarget(rate)
	f.minMs.SetTarget(minMs)
	f.maxMs.SetTarget(maxMs)
	f.fb.SetTarget(feedback)
	f.m.SetTarget(mix)
}

// Reset zera o estado.
func (f *Flanger) Reset() { f.line.Reset(); f.lfo.Reset() }

// ProcessBlock processa in-place.
func (f *Flanger) ProcessBlock(buf []float64) {
	maxD := float64(f.line.MaxDelay() - 1)
	for i, x := range buf {
		lfo := 0.5 * (1 + f.lfo.Next(f.rate.Next(), f.fs))
		lo, hi := f.minMs.Next(), f.maxMs.Next()
		d := (lo + (hi-lo)*lfo) / 1000 * f.fs
		d = math.Min(maxD, math.Max(1, d))
		wet := f.line.Read(d)
		f.line.Write(x + f.fb.Next()*wet)
		m := f.m.Next()
		buf[i] = (1-m)*x + m*wet
	}
}

// ---------------------------------------------------------------- Phaser

// PhaserMaxStages é o número máximo de estágios all-pass.
const PhaserMaxStages = 8

// Phaser: cascata de all-pass de 1ª ordem com frequência varrida por LFO (escala log).
type Phaser struct {
	fs                    float64
	lfo                   LFO
	xPrev, yPrev          [PhaserMaxStages]float64
	last                  float64
	stages                int
	rate, lo, hi, fb, mix Smoother
}

// NewPhaser cria um phaser.
func NewPhaser(fs float64) *Phaser {
	p := &Phaser{fs: fs, stages: 4}
	for _, s := range []*Smoother{&p.rate, &p.lo, &p.hi, &p.fb, &p.mix} {
		s.SetTime(fs, 20)
	}
	return p
}

// Set ajusta: rate em Hz, faixa de varredura em Hz, estágios (2..8), feedback -1..1, mix 0..1.
func (p *Phaser) Set(rate, minHz, maxHz float64, stages int, feedback, mix float64) {
	p.rate.SetTarget(rate)
	p.lo.SetTarget(minHz)
	p.hi.SetTarget(maxHz)
	p.stages = max(1, min(PhaserMaxStages, stages))
	p.fb.SetTarget(feedback)
	p.mix.SetTarget(mix)
}

// Reset zera o estado.
func (p *Phaser) Reset() {
	p.lfo.Reset()
	p.xPrev, p.yPrev = [PhaserMaxStages]float64{}, [PhaserMaxStages]float64{}
	p.last = 0
}

// ProcessBlock processa in-place.
func (p *Phaser) ProcessBlock(buf []float64) {
	for i, x := range buf {
		lfo := 0.5 * (1 + p.lfo.Next(p.rate.Next(), p.fs))
		lo, hi := p.lo.Next(), p.hi.Next()
		fc := lo * math.Pow(hi/lo, lfo)
		t := math.Tan(math.Pi * fc / p.fs)
		a := (t - 1) / (t + 1)

		s := x + p.fb.Next()*p.last
		for k := 0; k < p.stages; k++ {
			y := a*s + p.xPrev[k] - a*p.yPrev[k]
			p.xPrev[k], p.yPrev[k] = s, y
			s = y
		}
		p.last = s
		m := p.mix.Next()
		buf[i] = (1-m)*x + m*s
	}
}

// ---------------------------------------------------------------- Delay

// Delay analógico: eco com realimentação e um passa-baixa de 1 polo que escurece cada repetição.
type Delay struct {
	fs                   float64
	line                 *DelayLine
	lp                   float64
	timeSm, fb, damp, mx Smoother
}

// DelayMaxMs é o maior tempo de delay suportado.
const DelayMaxMs = 2000.0

// NewDelay cria um delay.
func NewDelay(fs float64) *Delay {
	d := &Delay{fs: fs, line: NewDelayLine(int(DelayMaxMs/1000*fs) + 1)}
	d.timeSm.SetTime(fs, 80) // mudar o tempo "estica" a fita, como num delay analógico
	for _, s := range []*Smoother{&d.fb, &d.damp, &d.mx} {
		s.SetTime(fs, 20)
	}
	return d
}

// Set ajusta: tempo em ms, feedback 0..1, damping 0..1 (0 = digital limpo), mix 0..1.
func (d *Delay) Set(timeMs, feedback, damping, mix float64) {
	samples := math.Max(1, math.Floor(math.Min(timeMs, DelayMaxMs)/1000*d.fs))
	d.timeSm.SetTarget(samples)
	d.fb.SetTarget(feedback)
	d.damp.SetTarget(damping)
	d.mx.SetTarget(mix)
}

// Reset zera o estado.
func (d *Delay) Reset() { d.line.Reset(); d.lp = 0 }

// ProcessBlock processa in-place.
func (d *Delay) ProcessBlock(buf []float64) {
	for i, x := range buf {
		wet := d.line.Read(d.timeSm.Next())
		a := d.damp.Next()
		d.lp = wet*(1-a) + d.lp*a
		d.line.Write(x + d.lp*d.fb.Next())
		m := d.mx.Next()
		buf[i] = (1-m)*x + m*wet
	}
}

// ---------------------------------------------------------------- Reverb

var (
	reverbCombMs    = [4]float64{29.7, 37.1, 41.1, 43.7}
	reverbAllpassMs = [2]float64{5.0, 1.7}
)

type comb struct {
	buf   []float64
	idx   int
	store float64
}

type allpass struct {
	buf []float64
	idx int
}

// Reverb algorítmico Schroeder/Freeverb: 4 combs em paralelo + 2 all-pass em série.
type Reverb struct {
	combs           [4]comb
	aps             [2]allpass
	size, damp, mix Smoother
}

// NewReverb cria um reverb.
func NewReverb(fs float64) *Reverb {
	r := &Reverb{}
	for i, ms := range reverbCombMs {
		r.combs[i].buf = make([]float64, max(1, int(ms/1000*fs)))
	}
	for i, ms := range reverbAllpassMs {
		r.aps[i].buf = make([]float64, max(1, int(ms/1000*fs)))
	}
	for _, s := range []*Smoother{&r.size, &r.damp, &r.mix} {
		s.SetTime(fs, 20)
	}
	return r
}

// Set ajusta: tamanho da sala 0..1 (feedback dos combs), amortecimento 0..1, mix 0..1.
func (r *Reverb) Set(size, damping, mix float64) {
	r.size.SetTarget(size)
	r.damp.SetTarget(damping)
	r.mix.SetTarget(mix)
}

// Reset zera o estado.
func (r *Reverb) Reset() {
	for i := range r.combs {
		clear(r.combs[i].buf)
		r.combs[i].idx, r.combs[i].store = 0, 0
	}
	for i := range r.aps {
		clear(r.aps[i].buf)
		r.aps[i].idx = 0
	}
}

// ProcessBlock processa in-place.
func (r *Reverb) ProcessBlock(buf []float64) {
	for i, x := range buf {
		fb, damp := r.size.Next(), r.damp.Next()
		sum := 0.0
		for k := range r.combs {
			c := &r.combs[k]
			y := c.buf[c.idx]
			c.store = y*(1-damp) + c.store*damp
			c.buf[c.idx] = x + c.store*fb
			if c.idx++; c.idx == len(c.buf) {
				c.idx = 0
			}
			sum += y
		}
		s := sum * 0.25
		for k := range r.aps {
			a := &r.aps[k]
			out := a.buf[a.idx]
			a.buf[a.idx] = s + out*0.5
			s = -s + out
			if a.idx++; a.idx == len(a.buf) {
				a.idx = 0
			}
		}
		m := r.mix.Next()
		buf[i] = (1-m)*x + m*s
	}
}
