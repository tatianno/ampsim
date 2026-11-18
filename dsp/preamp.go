package dsp

import "math"

// Preamp é o estágio de saturação do amp (porte de saturar_com_oversampling do main.py):
//
//	ganho = 2 + drive²·18
//	bias  = 0,25 · envelope(|x|)        ← assimetria que some no silêncio
//	y     = (tanh(x·ganho + bias) − tanh(bias)) / (1 + 0,35·ln(1+ganho))
//
// A não linearidade roda a 4x para reduzir aliasing, seguida de um DC blocker de 30 Hz.
type Preamp struct {
	os    *Oversampler4x
	env   Biquad  // passa-baixa de 20 Hz na taxa alta, sobre |x|
	dc    Cascade // passa-alta Butterworth 2ª ordem, 30 Hz
	drive Smoother
	up    [OversampleFactor]float64

	// ganho e normalização só mudam quando o drive muda
	lastDrive, g, norm float64
}

// NewPreamp cria o pré-amp para a taxa fs (aloca).
func NewPreamp(fs float64) *Preamp {
	p := &Preamp{os: NewOversampler4x()}
	p.env.SetCoefs(OnePoleLowpass(fs*OversampleFactor, 20))
	p.dc = NewCascade(ButterworthHighpass(2, fs, 30))
	p.drive = NewSmoother(fs, 20)
	return p
}

// PreampLatency é a latência do pré-amp em amostras (vem do oversampling).
const PreampLatency = osLatency

// Latency em amostras.
func (p *Preamp) Latency() int { return PreampLatency }

// SetDrive ajusta o drive (0..1).
func (p *Preamp) SetDrive(d float64) { p.drive.SetTarget(d) }

// Reset zera o estado.
func (p *Preamp) Reset() {
	p.os.Reset()
	p.env.Reset()
	p.dc.Reset()
}

// ProcessBlock processa um bloco in-place.
func (p *Preamp) ProcessBlock(buf []float64) {
	for i, x := range buf {
		if d := p.drive.Next(); d != p.lastDrive || p.norm == 0 {
			p.lastDrive = d
			p.g = 2 + d*d*18
			p.norm = 1 / (1 + 0.35*math.Log1p(p.g))
		}
		g, norm := p.g, p.norm

		p.os.Up(x, &p.up)
		// O bias vem de um envelope de 20 Hz: varia devagar, então tanh(bias) é
		// calculado uma vez por amostra da taxa base em vez de 4.
		var tb float64
		for k, u := range p.up {
			bias := 0.25 * p.env.Process(math.Abs(u))
			if k == 0 {
				tb = math.Tanh(bias)
			}
			p.up[k] = (math.Tanh(u*g+bias) - tb) * norm
		}
		buf[i] = p.dc.Process(p.os.Down(&p.up))
	}
}
