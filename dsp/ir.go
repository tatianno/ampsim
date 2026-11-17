package dsp

import "math"

// PrepareIR prepara uma resposta ao impulso de gabinete (porte de preparar_impulse_response):
//  1. corta o silêncio do início (a partir de 5% do pico, menos 2 amostras);
//  2. reamostra para fsTarget;
//  3. limita a maxMs milissegundos;
//  4. fade-out quadrático nos últimos 25 ms;
//  5. normaliza a energia (norma L2 = 1).
//
// Aloca memória: chamar fora da thread de áudio.
func PrepareIR(ir []float64, fsIR, fsTarget int, maxMs float64) []float64 {
	peak := 0.0
	for _, v := range ir {
		peak = math.Max(peak, math.Abs(v))
	}
	start := 0
	for i, v := range ir {
		if math.Abs(v) >= 0.05*peak {
			start = max(0, i-2)
			break
		}
	}
	out := append([]float64(nil), ir[start:]...)

	if fsIR != fsTarget {
		out = ResampleRate(out, fsIR, fsTarget)
	}

	if maxLen := int(maxMs / 1000 * float64(fsTarget)); len(out) > maxLen {
		out = out[:maxLen]
	}

	fade := min(len(out), int(0.025*float64(fsTarget)))
	for i := 0; i < fade; i++ {
		g := 1.0
		if fade > 1 {
			g = 1 - float64(i)/float64(fade-1)
		}
		out[len(out)-fade+i] *= g * g
	}

	energy := 0.0
	for _, v := range out {
		energy += v * v
	}
	if energy > 0 {
		inv := 1 / math.Sqrt(energy)
		for i := range out {
			out[i] *= inv
		}
	}
	return out
}
