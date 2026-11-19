package dsp

import "math"

// SafetyLimiter é um limitador de pico de segurança para a saída: sem lookahead (latência
// zero), com os dois canais ligados (o mesmo ganho em L e R, a imagem não anda). Abaixo do
// teto não faz nada (a saída é bit a bit a entrada); quando um pico passa, o ganho cai na
// hora o bastante para ele ficar no teto, fica parado por um tempo (hold: sem o hold o ganho
// seguiria cada meio ciclo de uma nota grave e distorceria) e volta devagar.
//
// O ataque instantâneo deforma um pouco o primeiro pico; é o preço da latência zero, e a
// ideia é ele atuar só de vez em quando (o volume continua sendo o ajuste do nível).
type SafetyLimiter struct {
	ceiling float64
	g       float64 // ganho atual (1 = sem redução)
	holdN   int
	hold    int
	rel     float64 // fração que falta até o ganho pedido recuperada por amostra
	on      bool
	minG    float64 // menor ganho desde a última Reduction
}

// Ajustes do limitador de saída.
const (
	LimiterCeilingDB = -0.3
	LimiterHoldMs    = 10.0 // cobre meio ciclo até 50 Hz
	LimiterReleaseMs = 50.0 // constante de tempo da volta
)

// holdBand: um pico a até 0,05 dB do nível limitado renova o hold.
var holdBand = math.Pow(10, 0.05/20)

// NewSafetyLimiter cria o limitador (ligado) para a taxa fs.
func NewSafetyLimiter(fs float64) *SafetyLimiter {
	return &SafetyLimiter{
		ceiling: math.Pow(10, LimiterCeilingDB/20),
		g:       1,
		holdN:   int(LimiterHoldMs / 1000 * fs),
		rel:     1 - math.Exp(-1/(LimiterReleaseMs/1000*fs)),
		on:      true,
		minG:    1,
	}
}

// SetOn liga ou desliga. Desligar com o ganho reduzido não dá salto: o ganho volta a 1 pela
// curva de volta normal e só então o limitador sai do caminho.
func (l *SafetyLimiter) SetOn(on bool) { l.on = on }

// Reset volta ao ganho 1.
func (l *SafetyLimiter) Reset() { l.g, l.hold = 1, 0 }

// Reduction devolve a maior redução (dB, ≥ 0) desde a chamada anterior.
func (l *SafetyLimiter) Reduction() float64 {
	r := -20 * math.Log10(l.minG)
	l.minG = l.g
	return r
}

// Process limita L e R in-place (r pode ser nil: mono). Não aloca.
func (l *SafetyLimiter) Process(left, r []float64) {
	if !l.on && l.g == 1 {
		return
	}
	for i, x := range left {
		p := math.Abs(x)
		if r != nil {
			p = math.Max(p, math.Abs(r[i]))
		}
		need := 1.0
		if l.on && p > l.ceiling {
			need = l.ceiling / p
		}
		switch {
		case need < l.g:
			l.g, l.hold = need, l.holdN
		case need < 1 && need < l.g*holdBand:
			// pico de novo no nível que está sendo limitado (amostrado um pouco abaixo do
			// anterior): renova o hold, senão o ganho subiria entre os ciclos de uma nota
			l.hold = l.holdN
		case l.hold > 0:
			l.hold--
		case l.g < 1:
			l.g = math.Min(need, l.g+(1-l.g)*l.rel)
			if l.g > 1-1e-4 && need == 1 { // o último passo (< 0,001 dB) não se ouve
				l.g = 1
			}
		}
		if l.g == 1 {
			continue
		}
		l.minG = math.Min(l.minG, l.g)
		left[i] *= l.g
		if r != nil {
			r[i] *= l.g
		}
	}
}
