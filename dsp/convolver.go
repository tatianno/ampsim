package dsp

import "math/cmplx"

// Convolver faz convolução com latência zero, adequada para IRs de gabinete em tempo real.
//
// A IR é dividida em partições de P amostras:
//   - a partição 0 é feita no domínio do tempo (P multiplicações por amostra);
//   - as partições 1..K-1 usam FFT (overlap-save uniforme, "UPOLS"). A contribuição
//     delas para o bloco b só depende de blocos de entrada já completos (b-1, b-2, ...),
//     então é calculada na fronteira do bloco, sem atraso.
//
// O bloco interno P é fixo, independente do tamanho de bloco do host, então a saída é
// idêntica com qualquer tamanho de buffer.
type Convolver struct {
	p, k int
	fft  *FFT

	head []float64      // partição 0, no tempo, invertida (head[i] = ir[P-1-i]) para o produto escalar
	H    [][]complex128 // espectros das partições 1..K-1, só os bins 0..P (H[0] não é usado)

	hist []float64 // [bloco anterior | bloco atual], 2P amostras
	pos  int       // posição dentro do bloco atual

	fdl     [][]complex128 // linha de atraso no domínio da frequência: espectros X_m (bins 0..P)
	fdlPos  int
	tailOut []float64    // saída das partições 1..K-1 para o bloco atual
	work    []complex128 // área de trabalho (2P)
	acc     []complex128 // acumulador (bins 0..P)
}

// Sinais reais têm espectro com simetria conjugada (X[2P−j] = conj(X[j])): basta guardar e
// multiplicar os bins 0..P (P+1 valores em vez de 2P) e reconstruir o resto antes da IFFT.

// NewConvolver prepara a convolução com a IR ir e partições de tamanho p (potência de 2).
func NewConvolver(ir []float64, p int) *Convolver {
	if len(ir) == 0 {
		ir = []float64{1}
	}
	k := (len(ir) + p - 1) / p
	c := &Convolver{
		p: p, k: k, fft: NewFFT(2 * p),
		head:    make([]float64, p),
		H:       make([][]complex128, k),
		hist:    make([]float64, 2*p),
		fdl:     make([][]complex128, k),
		tailOut: make([]float64, p),
		work:    make([]complex128, 2*p),
		acc:     make([]complex128, p+1),
	}
	for i := 0; i < p && i < len(ir); i++ {
		c.head[p-1-i] = ir[i]
	}
	for i := 1; i < k; i++ {
		spec := make([]complex128, 2*p)
		seg := ir[i*p : min((i+1)*p, len(ir))]
		for j, v := range seg {
			spec[j] = complex(v, 0)
		}
		c.fft.Forward(spec)
		c.H[i] = spec[: p+1 : p+1]
	}
	for i := range c.fdl {
		c.fdl[i] = make([]complex128, p+1)
	}
	return c
}

// Len devolve o tamanho da IR (arredondado para partições).
func (c *Convolver) Len() int { return c.p * c.k }

// Reset zera o estado (a IR é mantida).
func (c *Convolver) Reset() {
	clear(c.hist)
	clear(c.tailOut)
	for _, x := range c.fdl {
		clear(x)
	}
	c.pos, c.fdlPos = 0, 0
}

// ProcessBlock convolui um bloco in-place.
func (c *Convolver) ProcessBlock(buf []float64) {
	p := c.p
	for i, x := range buf {
		cur := p + c.pos
		c.hist[cur] = x

		// Partição 0 no tempo: y = Σ_j ir[j] · x[t-j] = head · x[t-p+1 .. t]
		y := c.tailOut[c.pos] + dot(c.head, c.hist[cur-p+1:cur+1])
		buf[i] = y

		c.pos++
		if c.pos == p {
			c.blockDone()
		}
	}
}

// blockDone roda na fronteira de bloco: calcula o espectro do bloco que acabou e a
// contribuição das partições 1..K-1 para o próximo bloco.
func (c *Convolver) blockDone() {
	p := c.p

	// X_m = FFT([bloco anterior, bloco atual])
	c.fdlPos--
	if c.fdlPos < 0 {
		c.fdlPos = c.k - 1
	}
	w := c.work
	for j, v := range c.hist {
		w[j] = complex(v, 0)
	}
	c.fft.Forward(w)
	copy(c.fdl[c.fdlPos], w[:p+1])

	// Próximo bloco b = m+1: Y = Σ_{k≥1} X_{b-k} · H_k = Σ_{k≥1} X_{m-(k-1)} · H_k
	clear(c.acc)
	for part := 1; part < c.k; part++ {
		xm := c.fdl[(c.fdlPos+part-1)%c.k]
		hk := c.H[part]
		for j := range c.acc {
			c.acc[j] += xm[j] * hk[j]
		}
	}
	if c.k > 1 {
		copy(c.work, c.acc) // bins 0..P
		for j := 1; j < p; j++ {
			c.work[2*p-j] = cmplx.Conj(c.acc[j]) // espelho conjugado
		}
		c.fft.Inverse(c.work)
		for j := range c.tailOut {
			c.tailOut[j] = real(c.work[p+j])
		}
	}

	// desliza o histórico: bloco atual vira o anterior
	copy(c.hist[:p], c.hist[p:])
	c.pos = 0
}
