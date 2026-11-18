package dsp

// Fator de sobreamostragem usado pelo pré-amp.
const OversampleFactor = 4

// Oversampler4x faz interpolação e decimação 4x em streaming com FIRs polifásicos
// de fase linear. A latência total (subir + descer) é um número inteiro de amostras
// na taxa base: (taps-1)/4.
type Oversampler4x struct {
	h   []float64                   // protótipo passa-baixa (ganho DC 1), simétrico (fase linear)
	sub [OversampleFactor][]float64 // fases da interpolação, contíguas e já com o ganho 4
	hh  []float64                   // primeira metade de h (para a decimação dobrada)

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
	o := &Oversampler4x{h: h, hh: h[:osTaps/2]}
	o.upLen = (osTaps + OversampleFactor - 1) / OversampleFactor
	// polifásico: fase p usa h[p], h[p+4], h[p+8]... em sequência na memória
	for p := range o.sub {
		for j := p; j < osTaps; j += OversampleFactor {
			o.sub[p] = append(o.sub[p], h[j]*OversampleFactor)
		}
	}
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
	for p := range o.sub {
		out[p] = dot(o.sub[p], hist)
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
	// h é simétrico (h[j] = h[N-1-j]): soma as amostras em pares e multiplica uma vez só.
	n := len(hist) - 1
	mid := len(o.hh)
	acc := o.h[mid] * hist[mid] // coeficiente central (N ímpar)
	hist = hist[:n+1]
	for j, hj := range o.hh {
		acc += hj * (hist[j] + hist[n-j])
	}
	return acc
}

// dot é o produto escalar a·b (len(b) ≥ len(a)), com 4 acumuladores independentes
// para o processador poder sobrepor as multiplicações.
func dot(a, b []float64) float64 {
	b = b[:len(a)]
	var s0, s1, s2, s3 float64
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return (s0 + s1) + (s2 + s3)
}
