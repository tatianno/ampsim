package dsp

import "math"

// Compressor é um compressor feed-forward com detector no domínio de dB
// (topologia "log-domain branching", como em Giannoulis, Massberg e Reiss, 2012):
//
//  1. nível instantâneo em dB: L = 20·log10|x|
//  2. curva estática com joelho suave: quanto reduzir (GR, dB) para chegar à razão pedida
//  3. GR suavizado com attack (quando precisa reduzir mais) e release (quando pode soltar)
//  4. saída = x · 10^((−GR + makeup)/20), misturada com o sinal seco (mix: compressão paralela)
//
// Sem lookahead: latência zero.
type Compressor struct {
	fs float64

	thresh, ratio, knee float64
	atkC, relC          float64
	atkMs, relMs        float64

	gr     float64 // redução de ganho suavizada (dB, ≥ 0)
	maxGR  float64 // maior GR do último bloco (para o medidor)
	makeup Smoother
	mix    Smoother
}

// CompressorKneeDB é a largura do joelho suave.
const CompressorKneeDB = 6.0

// NewCompressor cria um compressor.
func NewCompressor(fs float64) *Compressor {
	c := &Compressor{fs: fs, knee: CompressorKneeDB, ratio: 4, thresh: -20}
	c.makeup.SetTime(fs, 20)
	c.mix.SetTime(fs, 20)
	c.SetTimes(10, 150)
	return c
}

// Set ajusta limiar (dBFS), razão (≥ 1), attack e release (ms), ganho de compensação (dB) e mix (0..1).
func (c *Compressor) Set(threshDB, ratio, attackMs, releaseMs, makeupDB, mix float64) {
	c.thresh = threshDB
	c.ratio = math.Max(1, ratio)
	c.SetTimes(attackMs, releaseMs)
	c.makeup.SetTarget(makeupDB)
	c.mix.SetTarget(mix)
}

// SetTimes ajusta attack e release em ms (recalcula só quando mudam).
func (c *Compressor) SetTimes(attackMs, releaseMs float64) {
	if attackMs != c.atkMs {
		c.atkMs = attackMs
		c.atkC = timeCoef(c.fs, attackMs)
	}
	if releaseMs != c.relMs {
		c.relMs = releaseMs
		c.relC = timeCoef(c.fs, releaseMs)
	}
}

// coeficiente de um filtro de 1 polo com constante de tempo ms
func timeCoef(fs, ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return math.Exp(-1 / (ms / 1000 * fs))
}

// StaticGR devolve a redução de ganho (dB) da curva estática para um nível de entrada em dBFS.
func (c *Compressor) StaticGR(levelDB float64) float64 {
	over := levelDB - c.thresh
	slope := 1 - 1/c.ratio
	switch {
	case 2*over < -c.knee:
		return 0
	case 2*math.Abs(over) <= c.knee: // dentro do joelho: transição quadrática
		d := over + c.knee/2
		return slope * d * d / (2 * c.knee)
	default:
		return slope * over
	}
}

// GainReductionDB devolve a maior redução de ganho do último bloco (dB, ≥ 0).
func (c *Compressor) GainReductionDB() float64 { return c.maxGR }

// Reset zera o estado.
func (c *Compressor) Reset() { c.gr, c.maxGR = 0, 0 }

// ProcessBlock processa in-place.
func (c *Compressor) ProcessBlock(buf []float64) {
	c.maxGR = 0
	for i, x := range buf {
		target := c.StaticGR(20 * math.Log10(math.Abs(x)+1e-12))
		if target > c.gr {
			c.gr = target + c.atkC*(c.gr-target) // attack: aumenta a redução
		} else {
			c.gr = target + c.relC*(c.gr-target) // release: solta
		}
		c.maxGR = math.Max(c.maxGR, c.gr)
		y := x * math.Pow(10, (c.makeup.Next()-c.gr)/20)
		m := c.mix.Next()
		buf[i] = (1-m)*x + m*y
	}
}
