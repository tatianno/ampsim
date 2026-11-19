package dsp

import "math"

// HighGain é o pré-amp de alto ganho: três estágios de saturação em cascata (como as válvulas
// de um pré moderno), com filtros entre eles, a 4x para reduzir o aliasing.
//
//	entrada → passa-alta "tight" → shelf de brilho → [4x] estágio 1 → PA+PB → estágio 2 →
//	PA+PB → estágio 3 → PB → [1x] → bloqueio de DC → nível
//
// Cada estágio é tanh(g·x + b) − tanh(b), com ganho negativo (a válvula inverte a fase): com
// o bias fixo, a assimetria alterna de estágio para estágio, como numa cascata de triodos.
// Os passa-altas entre estágios (capacitores de acoplamento pequenos) tiram o grave antes de
// cada ceifamento, o que deixa o grave firme mesmo com muito ganho; os passa-baixas (Miller,
// capacitores de placa) seguram o chiado.
//
// Os valores (HighGainDefault) foram ajustados medindo uma captura NAM de um amp moderno de
// alto ganho (veja PLANO.md): resposta por 1/3 de oitava com a DI e com ruído, compressão e
// limiar de distorção por frequência.
type HighGain struct {
	p      HGParams
	fs     float64
	os     *Oversampler4x
	tight  Biquad // taxa base
	tight2 Biquad
	bright Biquad
	hp     [2]Biquad // taxa alta: depois dos estágios 1 e 2
	lp     [3]Biquad // taxa alta: depois de cada estágio
	dc     Cascade
	drive  Smoother
	up     [OversampleFactor]float64

	lastDrive, g1 float64
	tb            float64 // tanh(bias)
}

// HGParams são os parâmetros do pré de alto ganho (frequências em Hz, ganhos lineares).
type HGParams struct {
	TightHz, Tight2Hz   float64 // passa-altas de 1ª ordem na entrada (o 2º = 0: sem)
	BrightHz, BrightDB  float64 // shelf de agudos antes do 1º estágio
	HP1Hz, HP2Hz        float64 // acoplamento depois dos estágios 1 e 2
	LP1Hz, LP2Hz, LP3Hz float64 // passa-baixas depois de cada estágio
	G1Min, G1Max        float64 // ganho do 1º estágio com o knob em 0 e em 10 (escala log)
	G2, G3              float64 // ganho dos estágios 2 e 3
	Bias                float64 // assimetria de cada estágio
	Out                 float64 // nível de saída
}

// HighGainDefault são os valores ajustados (cmd/hgfit, 19/11/2026) contra uma captura NAM de
// um EVH 5150III (canal vermelho), com o knob de ganho em 8 (G1 = 80) e o tone stack em 5/5/5.
// Erro médio do espectro por 1/3 de oitava: 0,7 dB com a DI e com ruído em −30 dBFS; a THD por
// frequência e nível acompanha a da captura (inclusive o grave que só distorce com mais sinal).
// O passa-alta de entrada em ~420 Hz e o de 300 Hz depois do 1º estágio são o "tight"; os
// passa-baixas de ~3 kHz entre os estágios seguram o chiado. Out = 0,25 deixa o power amp
// quase linear, como na captura (o custo do ajuste só cresce acima disso). O knob de ganho vai
// de G1 = 0,05 (crunch: os estágios 2 e 3 sozinhos já dão ~55 dB) a ~506, em escala log.
var HighGainDefault = HGParams{
	TightHz: 424, Tight2Hz: 0,
	BrightHz: 841, BrightDB: 1.7,
	HP1Hz: 300, HP2Hz: 10,
	LP1Hz: 3000, LP2Hz: 2680, LP3Hz: 21200,
	G1Min: 0.05, G1Max: 506, G2: 47.6, G3: 12,
	Bias: 0.15,
	Out:  0.25,
}

// BritishModDefault é a cascata da voz Britânico Mod: um cabeçote britânico com um estágio de
// ganho a mais (como as modificações dos anos 80). Comparada à do High Gain: entrada menos
// "tight" (o grave entra mais solto), shelf de brilho maior (o capacitor de brilho do canal),
// passa-baixas mais abertos (mais agudo e "fizz") e menos ganho nos estágios 2 e 3, para o knob
// ir do crunch ao saturado. Sem captura de referência: ajustada pela THD (veja o PLANO).
var BritishModDefault = HGParams{
	TightHz: 120, Tight2Hz: 0,
	BrightHz: 1500, BrightDB: 4,
	HP1Hz: 160, HP2Hz: 20,
	LP1Hz: 6000, LP2Hz: 5000, LP3Hz: 21200,
	G1Min: 0.3, G1Max: 20, G2: 6, G3: 3,
	Bias: 0.2,
	Out:  0.25,
}

// NewHighGain cria o pré de alto ganho para a taxa fs (aloca).
func NewHighGain(fs float64) *HighGain { return NewHighGainWith(fs, HighGainDefault) }

// NewHighGainWith cria a cascata com os parâmetros p (aloca).
func NewHighGainWith(fs float64, p HGParams) *HighGain {
	h := &HighGain{fs: fs, os: NewOversampler4x()}
	h.dc = NewCascade(ButterworthHighpass(2, fs, 30))
	h.drive = NewSmoother(fs, 20)
	h.SetParams(p)
	return h
}

// SetParams troca os parâmetros (fora da thread de áudio; usado no ajuste e nos testes).
func (h *HighGain) SetParams(p HGParams) {
	h.p = p
	fs, hi := h.fs, h.fs*OversampleFactor
	h.tight.SetCoefs(OnePoleHighpass(fs, p.TightHz))
	if p.Tight2Hz > 0 {
		h.tight2.SetCoefs(OnePoleHighpass(fs, p.Tight2Hz))
	} else {
		h.tight2.SetCoefs(Coefs{B0: 1})
	}
	h.bright.SetCoefs(HighShelf(fs, p.BrightHz, 0.707, p.BrightDB))
	h.hp[0].SetCoefs(OnePoleHighpass(hi, p.HP1Hz))
	h.hp[1].SetCoefs(OnePoleHighpass(hi, p.HP2Hz))
	for i, f := range [3]float64{p.LP1Hz, p.LP2Hz, p.LP3Hz} {
		h.lp[i].SetCoefs(OnePoleLowpass(hi, f))
	}
	h.tb = math.Tanh(p.Bias)
	h.lastDrive = -1
}

// Latency em amostras (a do oversampling, igual à do Preamp).
func (h *HighGain) Latency() int { return osLatency }

// SetDrive ajusta o ganho (0..1).
func (h *HighGain) SetDrive(d float64) { h.drive.SetTarget(d) }

// Reset zera o estado.
func (h *HighGain) Reset() {
	h.os.Reset()
	h.tight.Reset()
	h.tight2.Reset()
	h.bright.Reset()
	for i := range h.hp {
		h.hp[i].Reset()
	}
	for i := range h.lp {
		h.lp[i].Reset()
	}
	h.dc.Reset()
}

// stage é um triodo: inverte a fase e ceifa de forma assimétrica.
func (h *HighGain) stage(x, g float64) float64 {
	return math.Tanh(-g*x+h.p.Bias) - h.tb
}

// ProcessBlock processa um bloco in-place.
func (h *HighGain) ProcessBlock(buf []float64) {
	p := &h.p
	for i, x := range buf {
		if d := h.drive.Next(); d != h.lastDrive {
			h.lastDrive = d
			h.g1 = p.G1Min * math.Pow(p.G1Max/p.G1Min, d)
		}
		x = h.bright.Process(h.tight2.Process(h.tight.Process(x)))
		h.os.Up(x, &h.up)
		for k, u := range h.up {
			u = h.lp[0].Process(h.stage(u, h.g1))
			u = h.lp[1].Process(h.stage(h.hp[0].Process(u), p.G2))
			u = h.lp[2].Process(h.stage(h.hp[1].Process(u), p.G3))
			h.up[k] = u
		}
		buf[i] = h.dc.Process(h.os.Down(&h.up)) * p.Out
	}
}
