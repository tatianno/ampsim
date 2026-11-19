package dsp

import (
	"errors"
	"fmt"
	"math"
)

// BlockProcessor processa um bloco de float32 (in e out podem ser o mesmo slice).
// nam.Net satisfaz esta interface.
type BlockProcessor interface {
	Process(in, out []float32)
}

// RateBridge roda um processador numa taxa interna diferente da taxa do áudio: reamostra
// a entrada (fs → fsInner), chama o processador e reamostra de volta (fsInner → fs), em
// streaming. Os dois conversores são polifásicos racionais (L/M) com FIR de fase linear
// (janela de Kaiser). O par é projetado junto para que:
//   - a latência da ida e volta seja um número inteiro de amostras na taxa do áudio,
//     menor ou igual ao limite pedido (cabe na latência que o bloco já compensa);
//   - a saída não dependa do tamanho dos blocos: a amostra j da volta fica pronta assim
//     que a amostra j da entrada chega, então não há FIFO com atraso extra.
//
// A banda útil vai até 0,41·min(fs, fsInner) (18 kHz entre 44,1 e 48 kHz); a rejeição
// começa em 0,59·min(fs, fsInner), onde nada mais dobra para dentro da banda útil.
type RateBridge struct {
	inner   BlockProcessor
	up, dn  polyStage
	latency int

	mid  []float32 // entrada do processador (taxa interna)
	mid2 []float32 // saída do processador
	fifo []float32 // saída da volta ainda não entregue
	nf   int
}

// Limites do projeto.
const (
	bridgeMaxAtten = 80.0 // dB de rejeição desejada
	bridgeMinAtten = 40.0 // abaixo disso a latência permitida é curta demais
	bridgeMaxL     = 4096 // fases por conversor (44,1 ↔ 48 kHz usa 160 e 147)
	bridgeBand     = 0.41 // fim da banda útil / min(fs, fsInner)
)

// polyStage é um conversor L/M em streaming: y[k] = L · Σ_i h[p + i·L] · x[n0 − i], com
// n0 = ⌊k·M/L⌋ e p = k·M − n0·L. A saída k sai assim que a entrada n0 chega.
type polyStage struct {
	l, m  int
	taps  int       // coeficientes por fase
	coef  []float64 // [l][taps], já com o ganho L
	hist  []float64 // histórico duplicado (leitura contígua); hist[pos] é a mais recente
	pos   int
	phase int // k·M − n·L da próxima saída, relativo à entrada atual
}

func newPolyStage(l, m int, h []float64) polyStage {
	taps := (len(h) + l - 1) / l
	s := polyStage{l: l, m: m, taps: taps, coef: make([]float64, l*taps), hist: make([]float64, 2*taps)}
	for p := range l {
		for i := range taps {
			if j := p + i*l; j < len(h) {
				s.coef[p*taps+i] = h[j] * float64(l)
			}
		}
	}
	return s
}

func (s *polyStage) reset() {
	clear(s.hist)
	s.pos, s.phase = 0, 0
}

// process converte in e grava em out; devolve quantas amostras gravou.
func (s *polyStage) process(in, out []float32) int {
	n := 0
	for _, v := range in {
		s.pos--
		if s.pos < 0 {
			s.pos = s.taps - 1
		}
		s.hist[s.pos] = float64(v)
		s.hist[s.pos+s.taps] = float64(v)
		hist := s.hist[s.pos : s.pos+s.taps]
		for s.phase < s.l {
			out[n] = float32(dot(s.coef[s.phase*s.taps:(s.phase+1)*s.taps], hist))
			n++
			s.phase += s.m
		}
		s.phase -= s.l
	}
	return n
}

// maxOut é o máximo de saídas para k entradas.
func (s *polyStage) maxOut(k int) int { return (k*s.l+s.m-1)/s.m + 1 }

// BridgeMaxInner é o maior bloco que o processador interno recebe quando o áudio vem em
// blocos de até maxBlock amostras (para dimensionar o processador).
func BridgeMaxInner(fs, fsInner float64, maxBlock int) int {
	l, m, err := bridgeRatio(fs, fsInner)
	if err != nil {
		return maxBlock
	}
	return (maxBlock*l+m-1)/m + 1
}

func bridgeRatio(fs, fsInner float64) (l, m int, err error) {
	a, b := int(math.Round(fs)), int(math.Round(fsInner))
	if a <= 0 || b <= 0 || float64(a) != fs || float64(b) != fsInner {
		return 0, 0, fmt.Errorf("taxas não inteiras: %g e %g Hz", fs, fsInner)
	}
	g := gcd(a, b)
	l, m = b/g, a/g
	if l > bridgeMaxL || m > bridgeMaxL {
		return 0, 0, fmt.Errorf("razão %d/%d grande demais para reamostrar em tempo real", l, m)
	}
	return l, m, nil
}

// kaiserBeta é o β da janela de Kaiser para atenuação a (dB) (fórmula de Kaiser).
func kaiserBeta(a float64) float64 {
	switch {
	case a > 50:
		return 0.1102 * (a - 8.7)
	case a >= 21:
		return 0.5842*math.Pow(a-21, 0.4) + 0.07886*(a-21)
	}
	return 0
}

// NewRateBridge cria a ponte fs → fsInner → fs em volta de inner, para blocos de até
// maxBlock amostras, com latência de no máximo maxLatency amostras na taxa fs. O inner
// precisa aceitar blocos de BridgeMaxInner(fs, fsInner, maxBlock). Aloca: chamar fora
// da thread de áudio.
func NewRateBridge(fs, fsInner float64, maxBlock, maxLatency int, inner BlockProcessor) (*RateBridge, error) {
	if fs == fsInner {
		return nil, errors.New("taxas iguais: não precisa reamostrar")
	}
	l, m, err := bridgeRatio(fs, fsInner)
	if err != nil {
		return nil, err
	}
	// Ida: L/M sobre fs; volta: M/L sobre fsInner. As duas trabalham na mesma taxa alta
	// H = L·fs = M·fsInner, com o mesmo protótipo (corte em min/2, transição 0,18·min).
	// Comprimento de Kaiser: N ≈ (A − 8)·H / (2,285 · 2π · Δf).
	hi := float64(l) * fs
	fmin := math.Min(fs, fsInner)
	width := (1 - 2*bridgeBand) * fmin
	perDB := hi / (2.285 * 2 * math.Pi * width) // coeficientes por dB
	// latência = (N − 1 + e)/L amostras de fs, com 0 ≤ e < L: reserva 1 amostra para e
	maxN := (maxLatency-1)*l + 1
	atten := math.Min(bridgeMaxAtten, 8+float64(maxN)/perDB)
	if atten < bridgeMinAtten {
		return nil, fmt.Errorf("latência de %d amostras curta demais para reamostrar %g → %g Hz", maxLatency, fs, fsInner)
	}
	n := int(math.Ceil((atten - 8) * perDB))
	n = min(n, maxN)
	if n%2 == 0 { // ímpar: atraso de grupo inteiro na taxa alta
		n--
	}
	h := LowpassFIR(n, fmin/hi, kaiserBeta(atten))
	// atraso total na taxa alta: (N−1)/2 de cada lado; completa com e zeros na ida até
	// virar múltiplo de L (latência inteira em fs)
	e := (l - (n-1)%l) % l
	hUp := make([]float64, e+n)
	copy(hUp[e:], h)

	b := &RateBridge{
		inner:   inner,
		up:      newPolyStage(l, m, hUp),
		dn:      newPolyStage(m, l, h),
		latency: (n - 1 + e) / l,
	}
	maxMid := b.up.maxOut(maxBlock)
	b.mid = make([]float32, maxMid)
	b.mid2 = make([]float32, maxMid)
	b.fifo = make([]float32, maxBlock+b.dn.maxOut(maxMid))
	return b, nil
}

// Latency devolve a latência da ida e volta em amostras da taxa do áudio.
func (b *RateBridge) Latency() int { return b.latency }

// Reset zera os conversores (o processador interno é zerado por quem o criou).
func (b *RateBridge) Reset() {
	b.up.reset()
	b.dn.reset()
	b.nf = 0
}

// Process processa um bloco na taxa do áudio (in e out podem ser o mesmo slice). Não aloca.
func (b *RateBridge) Process(in, out []float32) {
	k := len(in)
	nm := b.up.process(in, b.mid)
	b.inner.Process(b.mid[:nm], b.mid2[:nm])
	b.nf += b.dn.process(b.mid2[:nm], b.fifo[b.nf:])
	// a volta sempre tem pelo menos k amostras prontas (ver o comentário do tipo)
	got := min(k, b.nf)
	copy(out[:got], b.fifo[:got])
	clear(out[got:k])
	b.nf = copy(b.fifo, b.fifo[got:b.nf])
}
