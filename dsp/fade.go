package dsp

// Rampas aplicadas nas bordas de um arquivo carregado. Gravações muitas vezes começam ou
// terminam num corte abrupto (a DI de teste termina num clique); passando pela distorção do
// amp, esse corte vira um estalo. A rampa de saída é quadrática (desce mais rápido no fim).
const (
	FileFadeInMs  = 5.0
	FileFadeOutMs = 20.0
)

// FadeEdges aplica, in-place, rampa de entrada (linear) e de saída (quadrática) em x.
func FadeEdges(x []float64, fs, inMs, outMs float64) {
	n := len(x)
	in := min(n, int(inMs/1000*fs))
	for i := 0; i < in; i++ {
		x[i] *= float64(i) / float64(in)
	}
	out := min(n, int(outMs/1000*fs))
	for i := 0; i < out; i++ {
		g := float64(i) / float64(out) // 0 na última amostra
		x[n-1-i] *= g * g
	}
}
