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

// SyncLFO põe o LFO na fase de src mais offset ciclos (estéreo: o canal R defasado do L).
func (c *Chorus) SyncLFO(src *Chorus, offset float64) { c.lfo.SetPhase(src.lfo.phase + offset) }

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

// SyncLFO põe o LFO na fase de src mais offset ciclos (estéreo: o canal R defasado do L).
func (f *Flanger) SyncLFO(src *Flanger, offset float64) { f.lfo.SetPhase(src.lfo.phase + offset) }

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

// SyncLFO põe o LFO na fase de src mais offset ciclos (estéreo: o canal R defasado do L).
func (p *Phaser) SyncLFO(src *Phaser, offset float64) { p.lfo.SetPhase(src.lfo.phase + offset) }

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
//
// Em estéreo (ProcessStereo) há duas linhas. No modo duplo cada canal tem a sua, e a do R
// atrasa até DelayWideMs a mais conforme a largura. No ping-pong a entrada (L+R)/2 entra na
// linha L e as repetições alternam L, R, L, ...; a largura vai do eco no centro (0) ao
// ping-pong total (1).
type Delay struct {
	fs                   float64
	line, lineR          *DelayLine
	lp, lpR              float64
	timeSm, fb, damp, mx Smoother
	width                Smoother
	pingPong             bool
	quiet                int // amostras seguidas abaixo de TailSilence (entrada, eco e o que foi gravado)
}

// TailSilence é o nível abaixo do qual uma cauda (delay, reverb) é considerada extinta (−80 dB).
const TailSilence = 1e-4

// DelayMaxMs é o maior tempo de delay suportado.
const DelayMaxMs = 2000.0

// DelayWideMs é quanto o canal R atrasa a mais, com a largura toda, no modo duplo.
const DelayWideMs = 25.0

// NewDelay cria um delay.
func NewDelay(fs float64) *Delay {
	n := int((DelayMaxMs+DelayWideMs)/1000*fs) + 1
	d := &Delay{fs: fs, line: NewDelayLine(n), lineR: NewDelayLine(n)}
	d.quiet = len(d.line.buf)
	d.timeSm.SetTime(fs, 80) // mudar o tempo "estica" a fita, como num delay analógico
	for _, s := range []*Smoother{&d.fb, &d.damp, &d.mx, &d.width} {
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

// SetStereo ajusta o estéreo: largura 0..1 e o modo (ping-pong ou duplo).
func (d *Delay) SetStereo(width float64, pingPong bool) {
	d.width.SetTarget(width)
	d.pingPong = pingPong
}

// Reset zera o estado. O próximo Set vale na hora (sem rampa), como num pedal recém-ligado.
func (d *Delay) Reset() {
	d.line.Reset()
	d.lineR.Reset()
	d.lp, d.lpR, d.quiet = 0, 0, len(d.line.buf)
	for _, s := range []*Smoother{&d.timeSm, &d.fb, &d.damp, &d.mx, &d.width} {
		s.Unprime()
	}
}

// Silent informa se não sobrou nada a repetir: tudo o que entrou nas linhas no último trecho
// do tamanho do tempo de delay (o atual e o alvo, se ainda estiver mudando, mais o atraso do
// R) estava abaixo de TailSilence.
func (d *Delay) Silent() bool {
	return float64(d.quiet) > math.Max(d.timeSm.Value(), d.timeSm.target)+DelayWideMs/1000*d.fs+2
}

func (d *Delay) track(peak float64) {
	if peak < TailSilence {
		d.quiet++
	} else {
		d.quiet = 0
	}
}

// ProcessBlock processa in-place (mono: só a linha L).
func (d *Delay) ProcessBlock(buf []float64) {
	for i, x := range buf {
		wet := d.line.Read(d.timeSm.Next())
		a := d.damp.Next()
		d.lp = wet*(1-a) + d.lp*a
		w := x + d.lp*d.fb.Next()
		d.line.Write(w)
		d.track(math.Max(math.Abs(w), math.Abs(wet)))
		m := d.mx.Next()
		d.width.Next()
		buf[i] = (1-m)*x + m*wet
	}
}

// ProcessStereo processa L e R in-place.
func (d *Delay) ProcessStereo(l, r []float64) {
	wide := DelayWideMs / 1000 * d.fs
	for i, xl := range l {
		xr := r[i]
		t, w := d.timeSm.Next(), d.width.Next()
		a, fb, m := d.damp.Next(), d.fb.Next(), d.mx.Next()
		var wl, wr, ol, or, inL, inR float64
		if d.pingPong {
			wl, wr = d.line.Read(t), d.lineR.Read(t)
			d.lp = wl*(1-a) + d.lp*a
			d.lpR = wr*(1-a) + d.lpR*a
			inL, inR = 0.5*(xl+xr)+fb*d.lpR, fb*d.lp // L → R → L: cada repetição passa por um filtro
			ol, or = wl+(1-w)*wr, wr+(1-w)*wl
		} else {
			wl, wr = d.line.Read(t), d.lineR.Read(t+w*wide)
			d.lp = wl*(1-a) + d.lp*a
			d.lpR = wr*(1-a) + d.lpR*a
			inL, inR = xl+fb*d.lp, xr+fb*d.lpR
			ol, or = wl, wr
		}
		d.line.Write(inL)
		d.lineR.Write(inR)
		d.track(math.Max(math.Max(math.Abs(inL), math.Abs(inR)), math.Max(math.Abs(wl), math.Abs(wr))))
		l[i] = (1-m)*xl + m*ol
		r[i] = (1-m)*xr + m*or
	}
}

// ---------------------------------------------------------------- Reverb

var (
	reverbCombMs    = [4]float64{29.7, 37.1, 41.1, 43.7}
	reverbAllpassMs = [2]float64{5.0, 1.7}
)

// reverbSpread é quanto os combs e all-pass do canal R são mais longos (23 amostras a
// 44,1 kHz, como no Freeverb): as duas reflexões ficam descorrelacionadas.
const reverbSpread = 23.0 / 44100

type comb struct {
	buf   []float64
	idx   int
	store float64
}

type allpass struct {
	buf []float64
	idx int
}

// reverbBank é um conjunto de 4 combs em paralelo + 2 all-pass em série (um canal).
type reverbBank struct {
	combs [4]comb
	aps   [2]allpass
}

func newReverbBank(fs, extra float64) reverbBank {
	var b reverbBank
	for i, ms := range reverbCombMs {
		b.combs[i].buf = make([]float64, max(1, int((ms/1000+extra)*fs)))
	}
	for i, ms := range reverbAllpassMs {
		b.aps[i].buf = make([]float64, max(1, int((ms/1000+extra)*fs)))
	}
	return b
}

func (b *reverbBank) span() int {
	n := 0
	for _, c := range b.combs {
		n = max(n, len(c.buf))
	}
	for _, a := range b.aps {
		n += len(a.buf)
	}
	return n
}

func (b *reverbBank) reset() {
	for i := range b.combs {
		clear(b.combs[i].buf)
		b.combs[i].idx, b.combs[i].store = 0, 0
	}
	for i := range b.aps {
		clear(b.aps[i].buf)
		b.aps[i].idx = 0
	}
}

// next processa uma amostra; devolve a saída e o maior |valor| que passou pelos filtros.
func (b *reverbBank) next(x, fb, damp float64) (s, peak float64) {
	sum := 0.0
	for k := range b.combs {
		c := &b.combs[k]
		y := c.buf[c.idx]
		peak = math.Max(peak, math.Abs(y))
		c.store = y*(1-damp) + c.store*damp
		c.buf[c.idx] = x + c.store*fb
		if c.idx++; c.idx == len(c.buf) {
			c.idx = 0
		}
		sum += y
	}
	s = sum * 0.25
	for k := range b.aps {
		a := &b.aps[k]
		out := a.buf[a.idx]
		peak = math.Max(peak, math.Abs(out))
		a.buf[a.idx] = s + out*0.5
		s = -s + out
		if a.idx++; a.idx == len(a.buf) {
			a.idx = 0
		}
	}
	return s, peak
}

// Reverb algorítmico Schroeder/Freeverb: 4 combs em paralelo + 2 all-pass em série. Em
// estéreo, o R tem um segundo conjunto, um pouco mais longo; a largura vai do R igual ao L
// (0) ao R independente (1).
type Reverb struct {
	l, r                   reverbBank
	size, damp, mix, width Smoother
	quiet, span            int // amostras seguidas em silêncio; quantas bastam (o comb e os all-pass mais longos)
}

// NewReverb cria um reverb.
func NewReverb(fs float64) *Reverb {
	r := &Reverb{l: newReverbBank(fs, 0), r: newReverbBank(fs, reverbSpread)}
	for _, s := range []*Smoother{&r.size, &r.damp, &r.mix, &r.width} {
		s.SetTime(fs, 20)
	}
	r.span = max(r.l.span(), r.r.span())
	r.quiet = r.span
	return r
}

// Silent informa se a cauda acabou: combs e all-pass abaixo de TailSilence por tempo
// suficiente para todo o conteúdo deles ter passado.
func (r *Reverb) Silent() bool { return r.quiet >= r.span }

// Set ajusta: tamanho da sala 0..1 (feedback dos combs), amortecimento 0..1, mix 0..1.
func (r *Reverb) Set(size, damping, mix float64) {
	r.size.SetTarget(size)
	r.damp.SetTarget(damping)
	r.mix.SetTarget(mix)
}

// SetWidth ajusta a largura do estéreo (0..1).
func (r *Reverb) SetWidth(width float64) { r.width.SetTarget(width) }

// Reset zera o estado. O próximo Set vale na hora (sem rampa).
func (r *Reverb) Reset() {
	r.quiet = r.span
	for _, s := range []*Smoother{&r.size, &r.damp, &r.mix, &r.width} {
		s.Unprime()
	}
	r.l.reset()
	r.r.reset()
}

func (r *Reverb) track(peak float64) {
	if peak < TailSilence {
		r.quiet++
	} else {
		r.quiet = 0
	}
}

// ProcessBlock processa in-place (mono: só o conjunto L).
func (r *Reverb) ProcessBlock(buf []float64) {
	for i, x := range buf {
		fb, damp := r.size.Next(), r.damp.Next()
		s, peak := r.l.next(x, fb, damp)
		r.track(math.Max(peak, math.Abs(x)))
		m := r.mix.Next()
		r.width.Next()
		buf[i] = (1-m)*x + m*s
	}
}

// ProcessStereo processa L e R in-place: os dois conjuntos recebem (L+R)/2.
func (r *Reverb) ProcessStereo(l, rr []float64) {
	for i, xl := range l {
		xr := rr[i]
		x := 0.5 * (xl + xr)
		fb, damp := r.size.Next(), r.damp.Next()
		sl, pl := r.l.next(x, fb, damp)
		sr, pr := r.r.next(x, fb, damp)
		r.track(math.Max(math.Max(pl, pr), math.Abs(x)))
		m, w := r.mix.Next(), r.width.Next()
		l[i] = (1-m)*xl + m*sl
		rr[i] = (1-m)*xr + m*((1-w)*sl+w*sr)
	}
}
