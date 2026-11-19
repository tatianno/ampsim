//go:build !goexperiment.simd || !amd64

package nam

// simdKernels diz se os núcleos vetoriais estão em uso (aqui nunca: build sem o experimento).
const simdKernels = false

// convRows soma em dst (n = len(dst) amostras) uma saída da convolução:
//
//	dst[f] += Σ_kk Σ_i w[kk·nIn+i] · data[i·rowStride + base − (k−1−kk)·dil + f]
//
// (k = 1, dil = 0, base = 0 é a 1×1 sobre as linhas de uma mat).
func convRows(dst, w, data []float32, rowStride, nIn, k, dil, base int) {
	convRowsGo(dst, w, data, rowStride, nIn, k, dil, base)
}

func axpy(dst []float32, w float32, src []float32) { axpyGo(dst, w, src) }
