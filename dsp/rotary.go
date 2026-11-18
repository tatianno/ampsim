package dsp

import "math"

// ---------------------------------------------------------------- Rotary (Leslie)

// Velocidades da caixa rotativa (Hz de rotação), como numa Leslie 122.
const (
	rotHornSlow, rotHornFast = 0.80, 6.70
	rotDrumSlow, rotDrumFast = 0.67, 5.90
)

// rotor é uma das partes que giram: Doppler (atraso modulado) + volume modulado.
type rotor struct {
	line          *DelayLine
	phase, speed  float64 // ciclos; Hz atual
	upK, downK    float64 // inércia (acelerar / desacelerar)
	baseMs, dopMs float64 // atraso médio e excursão do Doppler
	am            float64 // profundidade da modulação de volume
}

func (r *rotor) next(x, target, depth, fs float64) float64 {
	k := r.downK
	if target > r.speed {
		k = r.upK
	}
	r.speed += k * (target - r.speed)
	r.phase += r.speed / fs
	if r.phase >= 1 {
		r.phase -= math.Floor(r.phase)
	}
	s := math.Sin(2 * math.Pi * r.phase)
	d := (r.baseMs + depth*r.dopMs*s) / 1000 * fs
	y := r.line.ReadCubic(d)
	r.line.Write(x)
	return y * (1 - depth*r.am*0.5*(1-math.Cos(2*math.Pi*r.phase)))
}

// Rotary simula a caixa rotativa (Leslie): os agudos vão para a corneta e os graves para o
// tambor, cada um girando na sua velocidade, com aceleração suave ao trocar Lento/Rápido
// (a corneta, mais leve, acelera mais rápido).
type Rotary struct {
	fs         float64
	lpA, lpB   Biquad // divisor em 800 Hz (Linkwitz-Riley 4ª ordem: soma plana)
	hpA, hpB   Biquad
	horn, drum rotor
	fast       bool
	depth, bal Smoother
	mix        Smoother
}

// NewRotary cria a caixa rotativa (aloca).
func NewRotary(fs float64) *Rotary {
	tau := func(s float64) float64 { return 1 - math.Exp(-1/(s*fs)) }
	r := &Rotary{fs: fs}
	for _, f := range []*Biquad{&r.lpA, &r.lpB} {
		f.SetCoefs(Lowpass2(fs, 800, 0.707))
	}
	for _, f := range []*Biquad{&r.hpA, &r.hpB} {
		f.SetCoefs(Highpass2(fs, 800, 0.707))
	}
	r.horn = rotor{line: NewDelayLine(int(0.01 * fs)), speed: rotHornSlow, upK: tau(0.6), downK: tau(1.0), baseMs: 2, dopMs: 0.45, am: 0.5}
	r.drum = rotor{line: NewDelayLine(int(0.01 * fs)), speed: rotDrumSlow, upK: tau(3.5), downK: tau(5), baseMs: 2, dopMs: 0.2, am: 0.35, phase: 0.3}
	for _, s := range []*Smoother{&r.depth, &r.bal, &r.mix} {
		s.SetTime(fs, 20)
	}
	return r
}

// Set ajusta a velocidade (fast), a profundidade (0..1), o balanço corneta/tambor
// (0 = só tambor, 1 = só corneta) e o mix (0..1).
func (r *Rotary) Set(fast bool, depth, balance, mix float64) {
	r.fast = fast
	r.depth.SetTarget(depth)
	r.bal.SetTarget(balance)
	r.mix.SetTarget(mix)
}

// Speeds devolve a rotação atual da corneta e do tambor (Hz).
func (r *Rotary) Speeds() (horn, drum float64) { return r.horn.speed, r.drum.speed }

// Reset zera o estado (e volta à velocidade pedida, sem rampa).
func (r *Rotary) Reset() {
	for _, f := range []*Biquad{&r.lpA, &r.lpB, &r.hpA, &r.hpB} {
		f.Reset()
	}
	r.horn.line.Reset()
	r.drum.line.Reset()
	r.horn.speed, r.drum.speed = rotHornSlow, rotDrumSlow
	if r.fast {
		r.horn.speed, r.drum.speed = rotHornFast, rotDrumFast
	}
}

// ProcessBlock processa in-place.
func (r *Rotary) ProcessBlock(buf []float64) {
	th, td := rotHornSlow, rotDrumSlow
	if r.fast {
		th, td = rotHornFast, rotDrumFast
	}
	for i, x := range buf {
		lo := r.lpB.Process(r.lpA.Process(x))
		hi := r.hpB.Process(r.hpA.Process(x))
		d, b := r.depth.Next(), r.bal.Next()
		wet := 2 * (b*r.horn.next(hi, th, d, r.fs) + (1-b)*r.drum.next(lo, td, d, r.fs))
		m := r.mix.Next()
		buf[i] = (1-m)*x + m*wet
	}
}

// ---------------------------------------------------------------- Vibe

// vibeStages são as frequências de base dos 4 estágios (capacitores diferentes no
// circuito original: a modulação fica irregular, longe do phaser comum).
var vibeStages = [4]float64{90, 420, 1500, 3800}

// Vibe é o modulador de fase com lâmpada (o "Uni-Vibe"): 4 passa-tudo de 1ª ordem com
// frequências escalonadas, movidos por um LFO com a forma (e a inércia) da lâmpada.
// No modo chorus soma o seco; no vibrato, só o modulado.
type Vibe struct {
	fs          float64
	ap          [4]float64 // estado dos passa-tudo
	lfo         LFO
	lamp        float64 // brilho da lâmpada, suavizado
	lampK       float64
	vibrato     bool
	rate, inten Smoother
}

// NewVibe cria o vibe.
func NewVibe(fs float64) *Vibe {
	v := &Vibe{fs: fs, lampK: 1 - math.Exp(-1/(0.006*fs))}
	v.rate.SetTime(fs, 20)
	v.inten.SetTime(fs, 20)
	return v
}

// Set ajusta a velocidade (Hz), a intensidade (0..1) e o modo (vibrato = só o modulado).
func (v *Vibe) Set(rate, intensity float64, vibrato bool) {
	v.rate.SetTarget(rate)
	v.inten.SetTarget(intensity)
	v.vibrato = vibrato
}

// Reset zera o estado.
func (v *Vibe) Reset() { v.ap = [4]float64{}; v.lfo.Reset(); v.lamp = 0 }

// ProcessBlock processa in-place.
func (v *Vibe) ProcessBlock(buf []float64) {
	for i, x := range buf {
		s := v.lfo.Next(v.rate.Next(), v.fs)
		target := 0.5 * (1 + s)
		target *= target // a lâmpada acende mais rápido do que apaga: forma assimétrica
		v.lamp += v.lampK * (target - v.lamp)
		depth := v.inten.Next()
		m := math.Pow(4, depth*(2*v.lamp-1)) // varre ±2 oitavas em volta da base
		y := x
		for k, base := range vibeStages {
			t := math.Tan(math.Pi * math.Min(base*m, 0.45*v.fs) / v.fs)
			a := (t - 1) / (t + 1)
			// passa-tudo de 1ª ordem (forma direta transposta)
			out := a*y + v.ap[k]
			v.ap[k] = y - a*out
			y = out
		}
		if v.vibrato {
			buf[i] = y
		} else {
			buf[i] = 0.5 * (x + y)
		}
	}
}
