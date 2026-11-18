package dsp

import "math"

// Parâmetros do pitch shifter (em ms; viram amostras na taxa do áudio).
const (
	pitchMinMs   = 4.0  // atraso mínimo da leitura
	pitchMaxMs   = 45.0 // atraso máximo da leitura
	pitchJumpMin = 10.0 // menor salto de emenda (cobre períodos até 100 Hz... e múltiplos)
	pitchJumpMax = 25.0 // maior salto (contém pelo menos um período do Mi grave, 12 ms)
	pitchCorrMs  = 6.0  // trecho comparado para achar o ponto de emenda
	pitchFadeMs  = 5.0  // crossfade da emenda
)

// pitchVoice é uma voz do pitch shifter: uma leitura da linha de atraso que anda em outra
// velocidade (muda o tom). Quando a leitura chega perto da ponta da linha, ela "emenda":
// pula para um ponto alinhado com o período do sinal (o salto de maior correlação, como no
// SOLA) e faz um crossfade curto. Assim a afinação fica exata e sem trêmulo.
type pitchVoice struct {
	d, dNew float64 // atraso atual e o da leitura que entra na emenda
	fade    int     // amostras restantes do crossfade (0 = sem emenda)
	ratio   Smoother
}

// PitchShifter muda o tom (até ±1 oitava) com duas vozes, para harmonizar em intervalos
// fixos, dobrar com detune ou fazer oitavas. Polifônico (sem detectar notas); o sinal
// modulado tem um atraso de 4 a 45 ms (o seco não atrasa).
type PitchShifter struct {
	fs                     float64
	line                   *DelayLine
	v1, v2                 pitchVoice
	lvl2, mix              Smoother
	dMin, dMax, jMin, jMax float64
	corr, fadeLen          int
}

// NewPitchShifter cria o pitch shifter (aloca).
func NewPitchShifter(fs float64) *PitchShifter {
	ms := func(v float64) float64 { return v / 1000 * fs }
	p := &PitchShifter{fs: fs, dMin: ms(pitchMinMs), dMax: ms(pitchMaxMs), jMin: ms(pitchJumpMin), jMax: ms(pitchJumpMax),
		corr: int(ms(pitchCorrMs)), fadeLen: int(ms(pitchFadeMs))}
	p.line = NewDelayLine(int(p.dMax+p.jMax) + p.corr + 8)
	for _, s := range []*Smoother{&p.v1.ratio, &p.v2.ratio, &p.lvl2, &p.mix} {
		s.SetTime(fs, 30)
	}
	p.Reset()
	return p
}

// Semitones converte semitons e cents na razão de frequência.
func Semitones(semi, cents float64) float64 { return math.Pow(2, (semi+cents/100)/12) }

// Set ajusta as razões das duas vozes, o nível da voz 2 (0..1) e o mix (0..1).
func (p *PitchShifter) Set(ratio1, ratio2, level2, mix float64) {
	p.v1.ratio.SetTarget(ratio1)
	p.v2.ratio.SetTarget(ratio2)
	p.lvl2.SetTarget(level2)
	p.mix.SetTarget(mix)
}

// Reset zera o estado.
func (p *PitchShifter) Reset() {
	p.line.Reset()
	mid := (p.dMin + p.dMax) / 2
	p.v1 = pitchVoice{d: mid, ratio: p.v1.ratio}
	p.v2 = pitchVoice{d: mid, ratio: p.v2.ratio}
}

// bestJump procura, entre jMin e jMax, o salto (para trás se back, senão para a frente) em
// que o trecho recente do sinal mais se parece com o trecho na leitura atual (correlação).
func (p *PitchShifter) bestJump(d float64, back bool) float64 {
	base := int(math.Round(d))
	best, bestJ := math.Inf(-1), p.jMin
	// busca grossa (passo 2) e depois fina (±2) em volta do melhor
	try := func(j int) {
		k0 := base + j
		if !back {
			k0 = base - j
		}
		if float64(k0) < p.dMin || float64(k0) > p.dMax {
			return
		}
		s := 0.0
		for k := 0; k < p.corr; k += 2 {
			s += p.line.ReadInt(base+k) * p.line.ReadInt(k0+k)
		}
		if s > best {
			best, bestJ = s, float64(j)
		}
	}
	for j := int(p.jMin); j <= int(p.jMax); j += 2 {
		try(j)
	}
	c := int(bestJ)
	for j := c - 2; j <= c+2; j++ {
		try(j)
	}
	if back {
		return bestJ
	}
	return -bestJ
}

func (p *PitchShifter) voice(v *pitchVoice) float64 {
	r := v.ratio.Next()
	step := 1 - r // o atraso muda isso por amostra (negativo = tom acima)
	if v.fade == 0 {
		need := step < 0 && v.d+step*float64(p.fadeLen) < p.dMin ||
			step > 0 && v.d+step*float64(p.fadeLen) > p.dMax
		if need {
			v.dNew = v.d + p.bestJump(v.d, step < 0)
			v.fade = p.fadeLen
		}
	}
	v.d += step
	y := p.line.ReadCubic(math.Max(2, v.d))
	if v.fade > 0 {
		v.dNew += step
		w := 1 - float64(v.fade)/float64(p.fadeLen)
		w = 0.5 - 0.5*math.Cos(math.Pi*w) // crossfade suave (soma 1: os trechos estão em fase)
		y = (1-w)*y + w*p.line.ReadCubic(math.Max(2, v.dNew))
		v.fade--
		if v.fade == 0 {
			v.d = v.dNew
		}
	}
	return y
}

// ProcessBlock processa in-place.
func (p *PitchShifter) ProcessBlock(buf []float64) {
	for i, x := range buf {
		p.line.Write(x)
		w1 := p.voice(&p.v1)
		w2 := p.voice(&p.v2)
		l2 := p.lvl2.Next()
		wet := (w1 + l2*w2) / (1 + l2)
		m := p.mix.Next()
		buf[i] = (1-m)*x + m*wet
	}
}
