package dsp

import "math"

// Gate é um noise gate / expansor (evolução de aplicar_noise_gate_rigido do main.py):
//
//   - envelope RMS: passa-baixa Butterworth 2ª ordem de 30 Hz sobre x²;
//   - abaixo do limiar o ganho cai como (env/limiar)³ (expansão 1:4);
//   - histerese: depois de aberto, só fecha quando o envelope cai HystDB abaixo do limiar;
//   - ganho suavizado com tempos separados de attack (abrir) e release (fechar);
//   - ganho abaixo de -60 dB vira zero exato.
//
// Com HystDB = 0 e attack = release, o resultado é idêntico ao do protótipo Python.
type Gate struct {
	fs                    float64
	env                   Biquad
	smoothAtk, smoothRel  Coefs
	gz                    float64 // estado do filtro de suavização do ganho
	last                  float64 // último ganho suavizado (antes do clip)
	thresh, closeAt, hyst float64
	open                  bool
}

// NewGate cria um gate.
func NewGate(fs float64) *Gate {
	g := &Gate{fs: fs}
	g.env.SetCoefs(Lowpass2(fs, 30, math.Sqrt2/2))
	g.SetThreshold(-40)
	g.SetTimes(1, 100)
	return g
}

// SetThreshold define o limiar em dBFS.
func (g *Gate) SetThreshold(db float64) {
	g.thresh = math.Pow(10, db/20)
	g.closeAt = g.thresh * math.Pow(10, -g.hyst/20)
}

// SetHysteresis define a histerese em dB (0 = sem histerese).
func (g *Gate) SetHysteresis(db float64) {
	g.hyst = db
	g.closeAt = g.thresh * math.Pow(10, -db/20)
}

// SetTimes define attack e release em milissegundos.
func (g *Gate) SetTimes(attackMs, releaseMs float64) {
	g.smoothAtk = g.smoothCoefs(math.Max(0.1, attackMs))
	g.smoothRel = g.smoothCoefs(math.Max(5, releaseMs))
}

// passa-baixa de 1ª ordem com constante de tempo τ (fc = 1/(2πτ)), como no main.py
func (g *Gate) smoothCoefs(ms float64) Coefs {
	fc := math.Min(1000/(2*math.Pi*ms), 0.45*g.fs)
	return OnePoleLowpass(g.fs, fc)
}

// IsOpen informa se o gate estava aberto na última amostra processada.
func (g *Gate) IsOpen() bool { return g.open }

// Reset zera o estado.
func (g *Gate) Reset() {
	g.env.Reset()
	g.gz, g.last = 0, 0
	g.open = false
}

// ProcessBlock aplica o gate in-place.
func (g *Gate) ProcessBlock(buf []float64) {
	for i, x := range buf {
		env := math.Sqrt(math.Max(0, g.env.Process(x*x)))

		if g.open {
			g.open = env >= g.closeAt
		} else {
			g.open = env >= g.thresh
		}
		target := 1.0
		if !g.open {
			r := env / (g.thresh + 1e-12)
			target = r * r * r
		}

		// Filtro de 1ª ordem (forma direta II transposta) com coeficientes de
		// attack quando o ganho precisa subir e de release quando precisa descer.
		c := &g.smoothRel
		if target > g.last {
			c = &g.smoothAtk
		}
		y := c.B0*target + g.gz
		g.gz = c.B1*target - c.A1*y
		gain := math.Min(1, math.Max(0, y))
		if gain < 0.001 {
			gain = 0
		}
		g.last = y
		buf[i] = x * gain
	}
}
