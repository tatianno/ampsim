package nam

import (
	"math"
	"math/rand/v2"
	"testing"
)

// O convRows (vetorial no build com GOEXPERIMENT=simd) bate com o escalar em convoluções
// com kernel e dilatação, 1×1, blocos com resto (n não múltiplo de 8 nem de 32) e acumulando
// sobre o que já está em dst. O axpy também.
func TestConvRowsIgualAoEscalar(t *testing.T) {
	t.Logf("SIMD: %v", SIMD())
	rng := rand.New(rand.NewPCG(11, 12))
	rnd := func(n int) []float32 {
		v := make([]float32, n)
		for i := range v {
			v[i] = float32(rng.NormFloat64())
		}
		return v
	}
	for _, c := range []struct{ nIn, k, dil, n int }{
		{16, 3, 1, 256}, {16, 3, 512, 256}, {8, 3, 7, 300}, {12, 1, 0, 64},
		{5, 2, 3, 37}, {1, 6, 2, 1}, {3, 3, 1, 7}, {16, 1, 0, 1024},
	} {
		rowStride := c.n + c.dil*(c.k-1) + 9
		data := rnd(c.nIn * rowStride)
		w := rnd(c.k * c.nIn)
		base := c.dil * (c.k - 1)
		init := rnd(c.n)
		got, want := append([]float32(nil), init...), append([]float32(nil), init...)
		convRows(got, w, data, rowStride, c.nIn, c.k, c.dil, base)
		convRowsGo(want, w, data, rowStride, c.nIn, c.k, c.dil, base)
		for f := range want {
			// erro relativo à soma dos módulos dos termos (o arredondamento cresce com ela)
			var mag float64
			for kk := range c.k {
				for i := range c.nIn {
					mag += math.Abs(float64(w[kk*c.nIn+i] * data[i*rowStride+base-(c.k-1-kk)*c.dil+f]))
				}
			}
			if d := math.Abs(float64(got[f] - want[f])); d > 1e-5*(mag+math.Abs(float64(init[f]))) {
				t.Fatalf("%+v: amostra %d: %g != %g", c, f, got[f], want[f])
			}
		}
		src := rnd(c.n)
		got, want = append([]float32(nil), init...), append([]float32(nil), init...)
		axpy(got, 0.7, src)
		axpyGo(want, 0.7, src)
		for f := range want {
			if d := math.Abs(float64(got[f] - want[f])); d > 1e-6*(1+math.Abs(float64(want[f]))) {
				t.Fatalf("axpy %+v: amostra %d: %g != %g", c, f, got[f], want[f])
			}
		}
	}
}
