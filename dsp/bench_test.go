package dsp

import (
	"math"
	"math/rand"
	"testing"
)

// Benchmarks com blocos de 64 amostras a 48 kHz (o caso mais apertado ao vivo).
// ns/op é por bloco; o orçamento de um bloco de 64 é 1,33 ms (1 333 333 ns).
const benchFS, benchBlock = 48000.0, 64

func benchBuf() []float64 {
	b := make([]float64, benchBlock)
	for i := range b {
		b[i] = 0.3 * math.Sin(float64(i)*0.07)
	}
	return b
}

func benchIR() []float64 {
	rng := rand.New(rand.NewSource(1))
	ir := make([]float64, 7200) // 150 ms
	for i := range ir {
		ir[i] = rng.NormFloat64() * math.Exp(-float64(i)/800)
	}
	return ir
}

func bench(b *testing.B, f func([]float64)) {
	buf := benchBuf()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f(buf)
	}
}

func BenchmarkConvolver(b *testing.B) { bench(b, NewConvolver(benchIR(), 64).ProcessBlock) }
func BenchmarkPreamp(b *testing.B) {
	p := NewPreamp(benchFS)
	p.SetDrive(0.65)
	bench(b, p.ProcessBlock)
}
func BenchmarkToneStack(b *testing.B) {
	t := NewToneStack(benchFS)
	t.Set(5, 5, 5)
	bench(b, t.ProcessBlock)
}
func BenchmarkGate(b *testing.B) { bench(b, NewGate(benchFS).ProcessBlock) }
func BenchmarkCompressor(b *testing.B) {
	c := NewCompressor(benchFS)
	c.Set(-30, 4, 10, 150, 6, 1)
	bench(b, c.ProcessBlock)
}
func BenchmarkChorus(b *testing.B) {
	c := NewChorus(benchFS)
	c.Set(1, 2, 20, 0.5)
	bench(b, c.ProcessBlock)
}
func BenchmarkFlanger(b *testing.B) {
	f := NewFlanger(benchFS)
	f.Set(0.3, 0.5, 4, 0.6, 0.5)
	bench(b, f.ProcessBlock)
}
func BenchmarkPhaser(b *testing.B) {
	p := NewPhaser(benchFS)
	p.Set(0.5, 200, 1800, 8, 0.5, 0.5)
	bench(b, p.ProcessBlock)
}
func BenchmarkDelay(b *testing.B) {
	d := NewDelay(benchFS)
	d.Set(380, 0.45, 0.35, 0.35)
	bench(b, d.ProcessBlock)
}
func BenchmarkReverb(b *testing.B) {
	r := NewReverb(benchFS)
	r.Set(0.8, 0.4, 0.35)
	bench(b, r.ProcessBlock)
}
