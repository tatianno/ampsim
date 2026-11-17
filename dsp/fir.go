package dsp

import "math"

// besselI0 é a função de Bessel modificada de ordem zero (série de potências).
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	y := x * x / 4
	for k := 1; k < 500; k++ {
		term *= y / float64(k*k)
		sum += term
		if term < sum*1e-17 {
			break
		}
	}
	return sum
}

// Kaiser devolve uma janela de Kaiser simétrica de n pontos (igual a scipy.signal.windows.kaiser).
func Kaiser(n int, beta float64) []float64 {
	w := make([]float64, n)
	if n == 1 {
		w[0] = 1
		return w
	}
	alpha := float64(n-1) / 2
	den := besselI0(beta)
	for i := range w {
		r := (float64(i) - alpha) / alpha
		w[i] = besselI0(beta*math.Sqrt(math.Max(0, 1-r*r))) / den
	}
	return w
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// LowpassFIR projeta um FIR passa-baixa de fase linear por janela de Kaiser, com ganho DC 1.
// cutoff é relativo a Nyquist (0..1), como em scipy.signal.firwin(n, cutoff, window=("kaiser", beta)).
func LowpassFIR(n int, cutoff, beta float64) []float64 {
	h := make([]float64, n)
	w := Kaiser(n, beta)
	alpha := float64(n-1) / 2
	sum := 0.0
	for i := range h {
		m := float64(i) - alpha
		h[i] = cutoff * sinc(cutoff*m) * w[i]
		sum += h[i]
	}
	for i := range h {
		h[i] /= sum
	}
	return h
}
