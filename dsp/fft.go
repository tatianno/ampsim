// Pacote dsp contém os blocos de processamento de áudio em Go puro, sem dependências.
//
// Regras para tudo que roda na thread de áudio (métodos Process*):
// não alocar memória, não travar mutex, não fazer I/O. Toda alocação acontece
// nos construtores / Prepare.
package dsp

import (
	"math"
	"math/bits"
)

// FFT é uma FFT complexa radix-2 com tabelas pré-calculadas.
type FFT struct {
	n       int
	twiddle []complex128 // e^{-2πik/n}, k < n/2
	rev     []int
}

// NewFFT cria uma FFT de tamanho n (potência de 2).
func NewFFT(n int) *FFT {
	if n < 2 || n&(n-1) != 0 {
		panic("dsp: tamanho da FFT precisa ser potência de 2")
	}
	f := &FFT{n: n, twiddle: make([]complex128, n/2), rev: make([]int, n)}
	for k := range f.twiddle {
		s, c := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		f.twiddle[k] = complex(c, s)
	}
	shift := 64 - bits.Len(uint(n-1))
	for i := range f.rev {
		f.rev[i] = int(bits.Reverse64(uint64(i)) >> shift)
	}
	return f
}

// Len devolve o tamanho da FFT.
func (f *FFT) Len() int { return f.n }

// Forward calcula a FFT direta in-place.
func (f *FFT) Forward(x []complex128) { f.transform(x, false) }

// Inverse calcula a FFT inversa in-place, já dividindo por n.
func (f *FFT) Inverse(x []complex128) {
	f.transform(x, true)
	inv := 1 / float64(f.n)
	for i := range x {
		x[i] = complex(real(x[i])*inv, imag(x[i])*inv)
	}
}

func (f *FFT) transform(x []complex128, inverse bool) {
	n := f.n
	x = x[:n]
	for i, j := range f.rev {
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				w := f.twiddle[k*step]
				if inverse {
					w = complex(real(w), -imag(w))
				}
				a := x[start+k]
				b := x[start+k+half] * w
				x[start+k] = a + b
				x[start+k+half] = a - b
			}
		}
	}
}
