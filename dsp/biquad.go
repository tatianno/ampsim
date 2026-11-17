package dsp

import "math"

// Coefs são os coeficientes de um biquad normalizado (a0 = 1).
// Um filtro de 1ª ordem é um biquad com B2 = A2 = 0.
type Coefs struct {
	B0, B1, B2, A1, A2 float64
}

// Biquad é um filtro IIR de 2ª ordem na forma direta II transposta
// (a mesma estrutura usada por scipy.signal.sosfilt).
type Biquad struct {
	Coefs
	z1, z2 float64
}

// Process filtra uma amostra.
func (f *Biquad) Process(x float64) float64 {
	y := f.B0*x + f.z1
	f.z1 = f.B1*x - f.A1*y + f.z2
	f.z2 = f.B2*x - f.A2*y
	return y
}

// ProcessBlock filtra um bloco in-place.
func (f *Biquad) ProcessBlock(buf []float64) {
	for i, x := range buf {
		buf[i] = f.Process(x)
	}
}

// SetCoefs troca os coeficientes mantendo o estado (para parâmetros que mudam ao vivo).
func (f *Biquad) SetCoefs(c Coefs) { f.Coefs = c }

// Reset zera o estado.
func (f *Biquad) Reset() { f.z1, f.z2 = 0, 0 }

// Cascade é uma série de biquads.
type Cascade []Biquad

// NewCascade cria uma cascata a partir de uma lista de coeficientes.
func NewCascade(cs []Coefs) Cascade {
	c := make(Cascade, len(cs))
	for i := range cs {
		c[i].Coefs = cs[i]
	}
	return c
}

// Process filtra uma amostra pela cascata.
func (c Cascade) Process(x float64) float64 {
	for i := range c {
		x = c[i].Process(x)
	}
	return x
}

// ProcessBlock filtra um bloco in-place.
func (c Cascade) ProcessBlock(buf []float64) {
	for i := range c {
		c[i].ProcessBlock(buf)
	}
}

// Reset zera o estado de todos os estágios.
func (c Cascade) Reset() {
	for i := range c {
		c[i].Reset()
	}
}

// ---- Projetos de 1ª ordem (transformada bilinear com pre-warping) ----

// OnePoleLowpass é um passa-baixa de 1ª ordem (Butterworth de ordem 1).
func OnePoleLowpass(fs, f float64) Coefs {
	k := math.Tan(math.Pi * f / fs)
	return Coefs{B0: k / (k + 1), B1: k / (k + 1), A1: (k - 1) / (k + 1)}
}

// OnePoleHighpass é um passa-alta de 1ª ordem (Butterworth de ordem 1).
func OnePoleHighpass(fs, f float64) Coefs {
	k := math.Tan(math.Pi * f / fs)
	return Coefs{B0: 1 / (k + 1), B1: -1 / (k + 1), A1: (k - 1) / (k + 1)}
}

// ---- Projetos de 2ª ordem (RBJ Audio EQ Cookbook) ----

func rbj(fs, f, q float64) (cosw, alpha float64) {
	w0 := 2 * math.Pi * f / fs
	s, c := math.Sincos(w0)
	return c, s / (2 * q)
}

func normalize(b0, b1, b2, a0, a1, a2 float64) Coefs {
	return Coefs{B0: b0 / a0, B1: b1 / a0, B2: b2 / a0, A1: a1 / a0, A2: a2 / a0}
}

// Lowpass2 é um passa-baixa de 2ª ordem. Com q = 1/√2 é um Butterworth.
func Lowpass2(fs, f, q float64) Coefs {
	c, a := rbj(fs, f, q)
	return normalize((1-c)/2, 1-c, (1-c)/2, 1+a, -2*c, 1-a)
}

// Highpass2 é um passa-alta de 2ª ordem. Com q = 1/√2 é um Butterworth.
func Highpass2(fs, f, q float64) Coefs {
	c, a := rbj(fs, f, q)
	return normalize((1+c)/2, -(1 + c), (1+c)/2, 1+a, -2*c, 1-a)
}

// Peaking é um equalizador paramétrico (sino) com ganho em dB.
func Peaking(fs, f, q, gainDB float64) Coefs {
	c, a := rbj(fs, f, q)
	A := math.Pow(10, gainDB/40)
	return normalize(1+a*A, -2*c, 1-a*A, 1+a/A, -2*c, 1-a/A)
}

// LowShelf é um shelf de graves com ganho em dB.
func LowShelf(fs, f, q, gainDB float64) Coefs {
	c, a := rbj(fs, f, q)
	A := math.Pow(10, gainDB/40)
	sa := 2 * math.Sqrt(A) * a
	return normalize(
		A*((A+1)-(A-1)*c+sa),
		2*A*((A-1)-(A+1)*c),
		A*((A+1)-(A-1)*c-sa),
		(A+1)+(A-1)*c+sa,
		-2*((A-1)+(A+1)*c),
		(A+1)+(A-1)*c-sa,
	)
}

// HighShelf é um shelf de agudos com ganho em dB.
func HighShelf(fs, f, q, gainDB float64) Coefs {
	c, a := rbj(fs, f, q)
	A := math.Pow(10, gainDB/40)
	sa := 2 * math.Sqrt(A) * a
	return normalize(
		A*((A+1)+(A-1)*c+sa),
		-2*A*((A-1)+(A+1)*c),
		A*((A+1)+(A-1)*c-sa),
		(A+1)-(A-1)*c+sa,
		2*((A-1)-(A+1)*c),
		(A+1)-(A-1)*c-sa,
	)
}

// ---- Butterworth de ordem N como cascata (equivalente a scipy.signal.butter) ----

// ButterworthSections devolve quantos estágios tem um Butterworth de ordem N.
func ButterworthSections(order int) int { return order/2 + order%2 }

func butterworth(order int, fs, f float64, high bool) []Coefs {
	cs := make([]Coefs, ButterworthSections(order))
	butterworthOrderInto(cs, order, fs, f, high)
	return cs
}

func butterworthOrderInto(cs []Coefs, order int, fs, f float64, high bool) {
	i := 0
	if order%2 == 1 {
		if high {
			cs[i] = OnePoleHighpass(fs, f)
		} else {
			cs[i] = OnePoleLowpass(fs, f)
		}
		i++
	}
	for k := 0; k < order/2; k++ {
		// Q de cada par de polos: 1 / (2 sen(θ)), θ = π(2k+1)/(2N)
		q := 1 / (2 * math.Sin(math.Pi*float64(2*k+1)/float64(2*order)))
		if high {
			cs[i] = Highpass2(fs, f, q)
		} else {
			cs[i] = Lowpass2(fs, f, q)
		}
		i++
	}
}

// ButterworthLowpass devolve os estágios de um passa-baixa Butterworth de ordem N.
func ButterworthLowpass(order int, fs, f float64) []Coefs { return butterworth(order, fs, f, false) }

// ButterworthHighpass devolve os estágios de um passa-alta Butterworth de ordem N.
func ButterworthHighpass(order int, fs, f float64) []Coefs { return butterworth(order, fs, f, true) }

// ButterworthLowpassInto escreve os estágios em dst (len = ButterworthSections(order)), sem alocar.
func ButterworthLowpassInto(dst []Coefs, order int, fs, f float64) {
	butterworthOrderInto(dst, order, fs, f, false)
}

// ButterworthHighpassInto escreve os estágios em dst (len = ButterworthSections(order)), sem alocar.
func ButterworthHighpassInto(dst []Coefs, order int, fs, f float64) {
	butterworthOrderInto(dst, order, fs, f, true)
}
