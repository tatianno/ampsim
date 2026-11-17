package dsp

// ResamplePoly reamostra x por up/down (offline, aloca memória).
//
// É um porte de scipy.signal.resample_poly com a janela padrão (Kaiser β=5):
// FIR de 2·10·max(up,down)+1 coeficientes, saída de fase zero (sem atraso).
// Usado para preparar a IR e para carregar arquivos WAV — nunca na thread de áudio.
func ResamplePoly(x []float64, up, down int) []float64 {
	g := gcd(up, down)
	up, down = up/g, down/g
	if up == 1 && down == 1 {
		return append([]float64(nil), x...)
	}
	maxRate := max(up, down)
	halfLen := 10 * maxRate
	h := LowpassFIR(2*halfLen+1, 1/float64(maxRate), 5.0)
	for i := range h {
		h[i] *= float64(up)
	}

	nIn := len(x)
	nOut := nIn * up / down
	if nIn*up%down != 0 {
		nOut++
	}
	y := make([]float64, nOut)
	// y[k] = Σ_n x[n] · h[halfLen + k·down − n·up]
	for k := range y {
		center := k*down + halfLen
		nMin := ceilDiv(center-2*halfLen, up)
		nMax := center / up
		nMin = max(nMin, 0)
		nMax = min(nMax, nIn-1)
		acc := 0.0
		for n := nMin; n <= nMax; n++ {
			acc += x[n] * h[center-n*up]
		}
		y[k] = acc
	}
	return y
}

// ResampleRate reamostra de fsIn para fsOut.
func ResampleRate(x []float64, fsIn, fsOut int) []float64 {
	return ResamplePoly(x, fsOut, fsIn)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func ceilDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a > 0) == (b > 0) {
		q++
	}
	return q
}
