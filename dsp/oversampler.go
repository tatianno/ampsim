package dsp

// Fator de sobreamostragem usado pelo pré-amp.
const OversampleFactor = 4

// Oversampler4x faz interpolação e decimação 4x em streaming com FIRs polifásicos
// de fase linear. A latência total (subir + descer) é um número inteiro de amostras
// na taxa base: (taps-1)/4.
type Oversampler4x struct {
	h []float64 // protótipo passa-baixa (ganho DC 1); a interpolação multiplica por 4

	// interpolação: histórico da entrada (buffer duplicado para leitura contígua)
	upHist []float64
	upPos  int
	upLen  int

	// decimação: histórico na taxa alta (anel de osTaps+3 amostras, duplicado)
	dnHist []float64
	dnPos  int
	dnLen  int
}

const (
	osLatency = 32                             // amostras na taxa base
	osTaps    = OversampleFactor*osLatency + 1 // 129 coeficientes
)

// NewOversampler4x cria o sobreamostrador (aloca; chamar fora da thread de áudio).
func NewOversampler4x() *Oversampler4x {
	// Corte em 0,22 de Nyquist na taxa alta (≈ 0,88 do Nyquist da taxa base), Kaiser β=7 (~70 dB).
	h := LowpassFIR(osTaps, 0.22, 7.0)
	o := &Oversampler4x{h: h}
	o.upLen = (osTaps + OversampleFactor - 1) / OversampleFactor
	o.upHist = make([]float64, 2*o.upLen)
	o.dnLen = osTaps + OversampleFactor - 1
	o.dnHist = make([]float64, 2*o.dnLen)
	return o
}

// Latency devolve a latência em amostras da taxa base.
func (o *Oversampler4x) Latency() int { return osLatency }

// Reset zera os históricos.
func (o *Oversampler4x) Reset() {
	clear(o.upHist)
	clear(o.dnHist)
	o.upPos, o.dnPos = 0, 0
}

// Up recebe uma amostra na taxa base e produz 4 amostras na taxa alta.
func (o *Oversampler4x) Up(x float64, out *[OversampleFactor]float64) {
	// histórico: upHist[upPos] é a amostra mais recente; as anteriores vêm depois.
	o.upPos--
	if o.upPos < 0 {
		o.upPos = o.upLen - 1
	}
	o.upHist[o.upPos] = x
	o.upHist[o.upPos+o.upLen] = x
	hist := o.upHist[o.upPos : o.upPos+o.upLen]

	// u[4n+p] = 4 · Σ_i h[p+4i] · x[n-i]
	for p := 0; p < OversampleFactor; p++ {
		acc := 0.0
		for i, j := 0, p; j < osTaps; i, j = i+1, j+OversampleFactor {
			acc += o.h[j] * hist[i]
		}
		out[p] = acc * OversampleFactor
	}
}

// Down recebe 4 amostras da taxa alta e devolve uma amostra na taxa base.
func (o *Oversampler4x) Down(in *[OversampleFactor]float64) float64 {
	for p := 0; p < OversampleFactor; p++ {
		o.dnPos--
		if o.dnPos < 0 {
			o.dnPos = o.dnLen - 1
		}
		o.dnHist[o.dnPos] = in[p]
		o.dnHist[o.dnPos+o.dnLen] = in[p]
	}
	// d[n] = Σ_j h[j] · s[4n − j]. A amostra mais recente é s[4n+3] (em dnPos);
	// s[4n] (fase 0) fica 3 posições depois. Usar a fase 0 deixa a latência inteira.
	hist := o.dnHist[o.dnPos+OversampleFactor-1 : o.dnPos+OversampleFactor-1+osTaps]
	acc := 0.0
	for j, hj := range o.h {
		acc += hj * hist[j]
	}
	return acc
}
