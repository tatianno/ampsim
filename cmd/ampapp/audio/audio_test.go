package audio

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ampsim/cmd/ampapp/source"
	"ampsim/engine"
)

var (
	diPath = filepath.Join("..", "..", "..", "testdata", "guitarra_di.wav")
	irPath = filepath.Join("..", "..", "..", "testdata", "gabinete_2x12_sm57.wav")
)

func novoRender(t *testing.T) (*Renderer, *source.FilePlayer, *engine.Chain) {
	t.Helper()
	precisa(t, diPath, irPath)
	const fs = 48000
	chain := engine.NewDefaultChain()
	if err := chain.Cab().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	chain.Prepare(fs, MaxBlock)
	r := NewRenderer(chain, fs)
	p := source.NewFilePlayer(fs)
	if err := p.Load(diPath); err != nil {
		t.Fatal(err)
	}
	r.SetSource(p)
	p.Play()
	return r, p, chain
}

func TestRenderEstereoEMedidores(t *testing.T) {
	r, _, _ := novoRender(t)
	const frames = 3000 // maior que MaxBlock: testa a divisão em blocos
	out := make([]float32, 2*frames)
	for k := 0; k < 20; k++ { // ~1,25 s
		r.Render(out, 2, frames)
	}
	for i := 0; i < frames; i++ {
		if out[2*i] != out[2*i+1] {
			t.Fatalf("quadro %d: canais diferentes", i)
		}
	}
	var n int
	var peak float32
	for {
		m, ok := r.Meters().Pop()
		if !ok {
			break
		}
		n++
		peak = max(peak, m.OutPeak)
		if m.Frames != frames {
			t.Errorf("medidor com %d quadros", m.Frames)
		}
	}
	if n != 20 || peak == 0 {
		t.Errorf("%d medidores, pico %.3f", n, peak)
	}
}

func TestRenderClipDeSeguranca(t *testing.T) {
	r, _, chain := novoRender(t)
	chain.Module(engine.PowerID).Param("volume").Set(12) // força passar de 0 dBFS
	out := make([]float32, 2*4096)
	var clips uint32
	for k := 0; k < 30; k++ {
		r.Render(out, 2, 4096)
		for _, v := range out {
			if v > 1 || v < -1 {
				t.Fatalf("saída %v fora de ±1", v)
			}
		}
	}
	for {
		m, ok := r.Meters().Pop()
		if !ok {
			break
		}
		clips += m.OutClips
	}
	if clips == 0 {
		t.Error("esperava amostras cortadas com volume +12 dB")
	}
}

func TestRenderSemAlocacao(t *testing.T) {
	r, _, chain := novoRender(t)
	for _, m := range chain.Modules() {
		chain.SetEnabled(m.ID(), true)
	}
	out := make([]float32, 2*256)
	if a := testing.AllocsPerRun(100, func() {
		r.Render(out, 2, 256)
		r.Meters().Pop()
	}); a != 0 {
		t.Errorf("Render aloca %.0f vezes por callback", a)
	}
}

func TestRingConcorrente(t *testing.T) {
	q := NewRing[int](64)
	const n = 100000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; {
			if q.Push(i) {
				i++
			}
		}
	}()
	next := 0
	for next < n {
		if v, ok := q.Pop(); ok {
			if v != next {
				t.Fatalf("fora de ordem: %d, esperava %d", v, next)
			}
			next++
		}
	}
	wg.Wait()
}

func TestRingCheioDescarta(t *testing.T) {
	q := NewRing[int](4)
	for i := 0; i < 4; i++ {
		if !q.Push(i) {
			t.Fatal("não deveria estar cheio")
		}
	}
	if q.Push(99) {
		t.Error("fila cheia deveria recusar")
	}
}

// Bypass A/B: depois do crossfade, a saída é a entrada atrasada pela latência da cadeia.
func TestBypassAB(t *testing.T) {
	r, p, chain := novoRender(t)
	p.Pause()
	clip := p.Clip()
	r.SetSource(constSource(0.25))
	r.SetBypass(true)
	out := make([]float32, 2*2048)
	r.Render(out, 2, 2048)
	lat := chain.Latency()
	fade := int(ABFadeMs / 1000 * 48000)
	// passado o crossfade e a latência, sai exatamente o seco
	for i := fade + lat; i < 2048; i++ {
		if out[2*i] != 0.25 {
			t.Fatalf("quadro %d: %v, esperava o sinal seco 0,25", i, out[2*i])
		}
	}
	_ = clip
}

type constSource float64

func (c constSource) Read(buf []float64) {
	for i := range buf {
		buf[i] = float64(c)
	}
}

// precisa pula o teste se algum arquivo de testdata/ não existir
// (os WAVs não vão para o repositório; veja o README).
func precisa(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("arquivo de teste ausente: %s (veja README, seção testdata)", p)
		}
	}
}
