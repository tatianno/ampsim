package nam

// Núcleos escalares do WaveNet (as convoluções são ~80% do tempo do modelo). São a
// referência e o caminho usado sem SIMD: em builds sem GOEXPERIMENT=simd, fora do amd64 ou
// em CPUs sem AVX2/FMA (ver kernels_simd_amd64.go).

// convRowsGo é a versão escalar do convRows: soma em dst uma saída da convolução, os pares
// (tap, entrada) de 4 em 4 numa só passada.
func convRowsGo(dst, w, data []float32, rowStride, nIn, k, dil, base int) {
	n := len(dst)
	total := k * nIn
	src := func(j int) []float32 {
		kk, i := j/nIn, j%nIn
		s := i*rowStride + base - (k-1-kk)*dil
		return data[s : s+n]
	}
	j := 0
	for ; j+4 <= total; j += 4 {
		axpy4Go(dst, w[j], src(j), w[j+1], src(j+1), w[j+2], src(j+2), w[j+3], src(j+3))
	}
	for ; j+2 <= total; j += 2 {
		axpy2Go(dst, w[j], src(j), w[j+1], src(j+1))
	}
	if j < total {
		axpyGo(dst, w[j], src(j))
	}
}

// axpyGo faz dst += w·src.
func axpyGo(dst []float32, w float32, src []float32) {
	src = src[:len(dst)]
	i := 0
	for ; i+4 <= len(dst); i += 4 {
		d, s := dst[i:i+4:i+4], src[i:i+4:i+4]
		d[0] += w * s[0]
		d[1] += w * s[1]
		d[2] += w * s[2]
		d[3] += w * s[3]
	}
	for ; i < len(dst); i++ {
		dst[i] += w * src[i]
	}
}

// axpy2Go faz dst += w0·s0 + w1·s1.
func axpy2Go(dst []float32, w0 float32, s0 []float32, w1 float32, s1 []float32) {
	s0, s1 = s0[:len(dst)], s1[:len(dst)]
	for f := range dst {
		dst[f] += w0*s0[f] + w1*s1[f]
	}
}

// axpy4Go faz dst += w0·s0 + w1·s1 + w2·s2 + w3·s3.
func axpy4Go(dst []float32, w0 float32, s0 []float32, w1 float32, s1 []float32,
	w2 float32, s2 []float32, w3 float32, s3 []float32) {
	n := len(dst)
	s0, s1, s2, s3 = s0[:n], s1[:n], s2[:n], s3[:n]
	for f := range dst {
		dst[f] += (w0*s0[f] + w1*s1[f]) + (w2*s2[f] + w3*s3[f])
	}
}

// SIMD diz se o WaveNet usa os núcleos vetoriais (AVX2 + FMA): build com GOEXPERIMENT=simd
// num amd64 com essas instruções. Sem eles o modelo completo custa ~3× mais.
func SIMD() bool { return simdKernels }
