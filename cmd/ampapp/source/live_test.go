package source

import (
	"math"
	"testing"
)

// buffer intercalado de 2 canais: canal 0 = 0,1·i, canal 1 = −0,2·i
func estereo(n int) []float32 {
	in := make([]float32, 2*n)
	for i := 0; i < n; i++ {
		in[2*i] = 0.1 * float32(i)
		in[2*i+1] = -0.2 * float32(i)
	}
	return in
}

func TestLiveInputCanais(t *testing.T) {
	l := NewLiveInput(48000)
	in := estereo(4)
	buf := make([]float64, 4)
	casos := []struct {
		canal int
		i3    float64 // amostra 3 esperada
	}{{0, 0.3}, {1, -0.6}, {ChannelMix, -0.15}, {7, 0.3}} // canal inexistente cai no 0
	for _, c := range casos {
		l.SetChannel(c.canal)
		l.Feed(in, 2, 4)
		l.Read(buf)
		if math.Abs(buf[3]-c.i3) > 1e-6 {
			t.Errorf("canal %d: amostra 3 = %v, esperava %v", c.canal, buf[3], c.i3)
		}
	}
}

func TestLiveInputTrimEPico(t *testing.T) {
	l := NewLiveInput(48000)
	l.Trim().Set(20 * math.Log10(2)) // +6,02 dB = ×2
	in := []float32{0.25, 0, 1, 0, -0.5, 0}
	l.Feed(in, 2, 3)
	buf := make([]float64, 3)
	l.Read(buf)
	if math.Abs(buf[0]-0.5) > 1e-6 || math.Abs(buf[2]+1) > 1e-6 {
		t.Errorf("trim ×2: %v", buf)
	}
	pk, clips := l.RawPeak()
	if pk != 1 || clips != 1 {
		t.Errorf("pico %.2f e %d clips, esperava 1 e 1 (o pico é antes do trim)", pk, clips)
	}
}

// Read em pedaços menores que o callback continua de onde parou; depois do fim, silêncio.
func TestLiveInputLeituraEmPedacos(t *testing.T) {
	l := NewLiveInput(48000)
	l.Feed(estereo(5), 2, 5)
	a, b := make([]float64, 3), make([]float64, 4)
	l.Read(a)
	l.Read(b)
	if math.Abs(a[2]-0.2) > 1e-6 || math.Abs(b[0]-0.3) > 1e-6 || math.Abs(b[1]-0.4) > 1e-6 || b[2] != 0 || b[3] != 0 {
		t.Errorf("pedaços: %v %v", a, b)
	}
}

func TestLiveInputSemAlocacao(t *testing.T) {
	l := NewLiveInput(48000)
	in := estereo(256)
	buf := make([]float64, 256)
	if a := testing.AllocsPerRun(100, func() {
		l.Feed(in, 2, 256)
		l.Read(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes", a)
	}
}
