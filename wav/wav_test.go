package wav

import (
	"bytes"
	"math"
	"path/filepath"
	"testing"
)

func TestRoundTripFloat32(t *testing.T) {
	in := [][]float64{{0, 0.5, -0.25, 0.999}, {0.1, -0.1, 0.2, -1}}
	var buf bytes.Buffer
	if err := Write(&buf, 48000, in, Float32); err != nil {
		t.Fatal(err)
	}
	a, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if a.SampleRate != 48000 || len(a.Channels) != 2 || a.Len() != 4 {
		t.Fatalf("cabeçalho errado: %d Hz, %d canais, %d amostras", a.SampleRate, len(a.Channels), a.Len())
	}
	for c := range in {
		for i := range in[c] {
			if math.Abs(a.Channels[c][i]-in[c][i]) > 1e-7 {
				t.Errorf("canal %d amostra %d: %v != %v", c, i, a.Channels[c][i], in[c][i])
			}
		}
	}
}

func TestRoundTripPCM16Clip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, 44100, [][]float64{{0.5, 2, -2}}, PCM16); err != nil {
		t.Fatal(err)
	}
	a, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := a.Channels[0]
	if math.Abs(got[0]-0.5) > 1e-4 || got[1] < 0.999 || got[2] > -0.999 {
		t.Errorf("clip/escala errados: %v", got)
	}
}

// A DI de teste é estéreo int16: a média precisa ficar na faixa ±1 (o bug do protótipo Python).
func TestDIEstereoNormalizada(t *testing.T) {
	a, err := ReadFile(filepath.Join("..", "testdata", "guitarra_di.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Channels) != 2 || a.SampleRate != 44100 {
		t.Fatalf("esperava estéreo 44100 Hz, veio %d canais %d Hz", len(a.Channels), a.SampleRate)
	}
	peak := 0.0
	for _, v := range a.Mono() {
		peak = math.Max(peak, math.Abs(v))
	}
	if peak > 1 || peak < 0.1 {
		t.Errorf("pico mono %.3f fora da faixa esperada", peak)
	}
}

// A IR é PCM 32 bits a 48 kHz e tem chunks extras que precisam ser pulados.
func TestIR32Bits(t *testing.T) {
	fs, ir, err := ReadMonoFile(filepath.Join("..", "testdata", "gabinete_2x12_sm57.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if fs != 48000 || len(ir) != 24000 {
		t.Fatalf("esperava 24000 amostras a 48 kHz, veio %d a %d Hz", len(ir), fs)
	}
}
