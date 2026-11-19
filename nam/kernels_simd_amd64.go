//go:build goexperiment.simd

package nam

import "simd/archsimd"

// Núcleos com AVX2 + FMA (8 float32 por instrução), do pacote experimental simd/archsimd do
// Go: só entram compilando com GOEXPERIMENT=simd. Em CPUs sem AVX2/FMA ficam os escalares.
// O FMA arredonda uma vez por multiplicação-soma e a ordem das somas muda, então o resultado
// difere do escalar nos últimos bits (dentro da tolerância da comparação com a NAM core).

var simdKernels = archsimd.X86.AVX2() && archsimd.X86.FMA()

// convRows soma em dst (n = len(dst) amostras) uma saída da convolução:
//
//	dst[f] += Σ_kk Σ_i w[kk·nIn+i] · data[i·rowStride + base − (k−1−kk)·dil + f]
//
// (k = 1, dil = 0, base = 0 é a 1×1 sobre as linhas de uma mat). Ao contrário do escalar,
// percorre dst em pedaços de 32 amostras que ficam em 4 registradores enquanto todos os
// pares (tap, entrada) são somados: dst é lido e gravado uma vez só.
func convRows(dst, w, data []float32, rowStride, nIn, k, dil, base int) {
	if !simdKernels {
		convRowsGo(dst, w, data, rowStride, nIn, k, dil, base)
		return
	}
	n := len(dst)
	total := k * nIn
	w = w[:total]
	f := 0
	for ; f+32 <= n; f += 32 {
		a0 := archsimd.LoadFloat32x8Slice(dst[f:])
		a1 := archsimd.LoadFloat32x8Slice(dst[f+8:])
		a2 := archsimd.LoadFloat32x8Slice(dst[f+16:])
		a3 := archsimd.LoadFloat32x8Slice(dst[f+24:])
		j := 0
		for kk := range k {
			start := base - (k-1-kk)*dil + f
			for i := range nIn {
				p := i*rowStride + start
				s := data[p : p+32 : p+32]
				v := archsimd.BroadcastFloat32x8(w[j])
				a0 = archsimd.LoadFloat32x8Slice(s).MulAdd(v, a0)
				a1 = archsimd.LoadFloat32x8Slice(s[8:]).MulAdd(v, a1)
				a2 = archsimd.LoadFloat32x8Slice(s[16:]).MulAdd(v, a2)
				a3 = archsimd.LoadFloat32x8Slice(s[24:]).MulAdd(v, a3)
				j++
			}
		}
		a0.StoreSlice(dst[f:])
		a1.StoreSlice(dst[f+8:])
		a2.StoreSlice(dst[f+16:])
		a3.StoreSlice(dst[f+24:])
	}
	// de 8 em 8 e o resto (menos de 8) copiado para um vetor cheio na pilha, nas mesmas
	// instruções: todas as amostras passam pela mesma conta (silêncio na entrada dá saída
	// constante). Não usa LoadFloat32x8SlicePart: o checkptr do -race o acusa em fatias curtas.
	for ; f < n; f += 8 {
		m := min(8, n-f)
		var t [8]float32
		copy(t[:], dst[f:f+m])
		a := archsimd.LoadFloat32x8(&t)
		j := 0
		for kk := range k {
			start := base - (k-1-kk)*dil + f
			for i := range nIn {
				p := i*rowStride + start
				var u [8]float32
				copy(u[:], data[p:p+m])
				a = archsimd.LoadFloat32x8(&u).MulAdd(archsimd.BroadcastFloat32x8(w[j]), a)
				j++
			}
		}
		a.Store(&t)
		copy(dst[f:f+m], t[:m])
	}
}

func axpy(dst []float32, w float32, src []float32) {
	if !simdKernels {
		axpyGo(dst, w, src)
		return
	}
	n := len(dst)
	src = src[:n]
	vw := archsimd.BroadcastFloat32x8(w)
	for i := 0; i < n; i += 8 {
		m := min(8, n-i)
		var t, u [8]float32
		copy(t[:], dst[i:i+m])
		copy(u[:], src[i:i+m])
		archsimd.LoadFloat32x8(&u).MulAdd(vw, archsimd.LoadFloat32x8(&t)).Store(&t)
		copy(dst[i:i+m], t[:m])
	}
}
