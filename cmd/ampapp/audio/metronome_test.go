package audio

import (
	"math"
	"testing"
	"time"

	"github.com/tatianno/ampsim/wav"
)

// onsets devolve os índices onde um clique começa no canal L (depois de pelo menos 100 ms
// de silêncio).
func onsets(out []float32, channels int) []int {
	var idx []int
	quiet := 1 << 30
	for i := 0; i < len(out)/channels; i++ {
		if math.Abs(float64(out[i*channels])) > 1e-4 {
			if quiet > 4800 {
				idx = append(idx, i)
			}
			quiet = 0
		} else {
			quiet++
		}
	}
	return idx
}

// O clique sai na placa no andamento escolhido, com acento no 1º tempo.
func TestMetronomoAndamentoEAcento(t *testing.T) {
	r, _ := renderVivo(t) // entrada ao vivo sem sinal: a saída é só o clique
	m := NewMetronome(r)
	m.BPM.Set(120)
	m.Beats.Set(4)
	m.Start()
	const n, blocos = 256, 562 // pouco menos de 3 s
	out := make([]float32, 2*n*blocos)
	for k := 0; k < blocos; k++ {
		r.Render(out[2*n*k:2*n*(k+1)], 2, n)
		r.Meters().Pop()
	}
	on := onsets(out, 2)
	if len(on) != 6 {
		t.Fatalf("%d cliques em 3 s a 120 BPM, esperava 6: %v", len(on), on)
	}
	for i, x := range on {
		if want := i*24000 + 1; x != want { // a amostra 0 do clique é zero (ataque)
			t.Errorf("clique %d na amostra %d, esperava %d", i, x, want)
		}
	}
	// acento (1600 Hz) no 1º e no 5º clique: conta as passagens por zero nos 10 ms iniciais
	cross := func(at int) int {
		c := 0
		for i := at + 1; i < at+480; i++ {
			if (out[2*i] > 0) != (out[2*(i-1)] > 0) {
				c++
			}
		}
		return c
	}
	if a, b := cross(on[0]), cross(on[1]); a <= b+5 {
		t.Errorf("1º tempo com %d passagens por zero e o 2º com %d: o acento deveria ser mais agudo", a, b)
	}
	if a := cross(on[4]); math.Abs(float64(a-cross(on[0]))) > 1 {
		t.Errorf("o 5º clique (1º tempo do 2º compasso) deveria ter o acento")
	}
	if l, rr := out[2*on[0]+40], out[2*on[0]+41]; l != rr {
		t.Errorf("clique deveria ser igual em L e R: %v %v", l, rr)
	}
	if c, beat := m.Count(); c != 6 || beat != 1 {
		t.Errorf("Count = %d, tempo %d; esperava 6 e 1", c, beat)
	}
}

// O clique toca na placa durante a gravação, mas o arquivo gravado fica sem ele.
func TestMetronomoNaoEntraNaGravacao(t *testing.T) {
	r, _ := renderVivo(t)
	src := &senoSource{f: 220, fs: 48000, amp: 0.3}
	r.SetSource(src)
	m := NewMetronome(r)
	m.Volume.Set(-6)
	m.Start()
	rec := NewRecorder(r)
	if _, err := rec.Start(t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	const n, blocos = 256, 188 // ~1 s
	out := make([]float32, 2*n*blocos)
	for k := 0; k < blocos; k++ {
		r.Render(out[2*n*k:2*n*(k+1)], 2, n)
		r.Meters().Pop()
		if k%20 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	take, err := rec.Stop()
	if err != nil {
		t.Fatal(err)
	}
	_, x, err := wav.ReadMonoFile(take.Out)
	if err != nil {
		t.Fatal(err)
	}
	var diffOut, diffRec float64
	for i := range x {
		want := 0.3 * math.Sin(2*math.Pi*220*float64(i)/48000)
		diffOut = math.Max(diffOut, math.Abs(float64(out[2*i])-want))
		diffRec = math.Max(diffRec, math.Abs(x[i]-want))
	}
	if diffOut < 0.1 {
		t.Errorf("a placa deveria ouvir o clique (diferença máx. %.3f)", diffOut)
	}
	if diffRec > 1e-5 {
		t.Errorf("o clique entrou na gravação (diferença máx. %.6f)", diffRec)
	}
}

// Com a saída muda (afinador), o clique não sai, mas o andamento segue contando.
func TestMetronomoMudo(t *testing.T) {
	r, _ := renderVivo(t)
	m := NewMetronome(r)
	m.Start()
	r.SetMute(true)
	out := make([]float32, 2*256)
	for k := 0; k < 200; k++ {
		r.Render(out, 2, 256)
		r.Meters().Pop()
		for _, v := range out {
			if v != 0 {
				t.Fatal("clique com a saída muda")
			}
		}
	}
	if c, _ := m.Count(); c == 0 {
		t.Error("o andamento deveria seguir contando com a saída muda")
	}
}

func TestRenderSemAlocacaoComMetronomo(t *testing.T) {
	r, _ := renderVivo(t)
	m := NewMetronome(r)
	m.BPM.Set(300)
	m.Start()
	out := make([]float32, 2*256)
	if a := testing.AllocsPerRun(200, func() {
		r.Render(out, 2, 256)
		r.Meters().Pop()
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por callback", a)
	}
}
