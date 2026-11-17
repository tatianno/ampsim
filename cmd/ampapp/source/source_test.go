package source

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

var diPath = filepath.Join("..", "..", "..", "testdata", "guitarra_di.wav")

// clip sintético: rampa 1, 2, 3, ... para conferir posições
func rampa(n int) *Clip {
	c := &Clip{Name: "rampa", FS: 1000, Samples: make([]float64, n)}
	for i := range c.Samples {
		c.Samples[i] = float64(i + 1)
	}
	return c
}

func TestCarregaEReamostra(t *testing.T) {
	precisa(t, diPath)
	p := NewFilePlayer(48000)
	if err := p.Load(diPath); err != nil {
		t.Fatal(err)
	}
	c := p.Clip()
	want := (162831*48000 + 44099) / 44100
	if len(c.Samples) != want {
		t.Errorf("%d amostras a 48 kHz, esperava %d", len(c.Samples), want)
	}
	if math.Abs(c.Duration()-3.692) > 0.001 {
		t.Errorf("duração %.3f s", c.Duration())
	}
}

func TestPausadoESilencio(t *testing.T) {
	p := NewFilePlayer(1000)
	p.SetClip(rampa(100))
	buf := make([]float64, 10)
	p.Read(buf)
	for _, v := range buf {
		if v != 0 {
			t.Fatal("pausado deveria dar silêncio")
		}
	}
}

func TestTocaAteOFimSemLoop(t *testing.T) {
	p := NewFilePlayer(1000) // rampa de 5 amostras a 1 kHz
	p.SetClip(rampa(100))
	p.SetLoop(false)
	p.Play()
	buf := make([]float64, 150)
	p.Read(buf)
	// depois da rampa de entrada, sai o próprio clip
	for i := 10; i < 100; i++ {
		if buf[i] != float64(i+1) {
			t.Fatalf("amostra %d = %v, esperava %v", i, buf[i], i+1)
		}
	}
	for i := 100; i < 150; i++ {
		if buf[i] != 0 {
			t.Fatalf("depois do fim deveria ser silêncio, amostra %d = %v", i, buf[i])
		}
	}
	if p.Playing() {
		t.Error("deveria ter parado no fim")
	}
	if p.Position() != 0 {
		t.Errorf("deveria voltar ao início, posição %.3f", p.Position())
	}
}

func TestLoopComTrecho(t *testing.T) {
	p := NewFilePlayer(1000)
	p.SetClip(rampa(100))
	if err := p.SetLoopRegion(0.040, 0.080); err != nil { // amostras 40..79
		t.Fatal(err)
	}
	p.Seek(0.040)
	p.Play()
	buf := make([]float64, 200)
	p.Read(buf)
	// meio do trecho, longe das rampas: tem que repetir 40..79 (valores 41..80)
	for k := 0; k < 5; k++ {
		i := k*40 + 20
		if buf[i] != 61 {
			t.Errorf("volta %d: amostra %d = %v, esperava 61", k, i, buf[i])
		}
	}
	for i, v := range buf {
		if v > 80 {
			t.Fatalf("amostra %d = %v saiu do trecho do loop", i, v)
		}
	}
}

func TestSeekEPausaComRampa(t *testing.T) {
	p := NewFilePlayer(1000)
	p.SetClip(rampa(1000))
	p.Play()
	p.Seek(0.5)
	buf := make([]float64, 20)
	p.Read(buf)
	// rampa de 5 amostras: a primeira sai com ganho 0,2
	if math.Abs(buf[0]-501*0.2) > 1e-9 || buf[19] != 520 {
		t.Errorf("seek com rampa: início %v (esperava %v), amostra 19 = %v (esperava 520)", buf[0], 501*0.2, buf[19])
	}
	p.Pause()
	p.Read(buf)
	if buf[0] == 0 || buf[19] != 0 {
		t.Errorf("pausa deveria descer em rampa: %v ... %v", buf[0], buf[19])
	}
}

func TestReadSemAlocacao(t *testing.T) {
	precisa(t, diPath)
	p := NewFilePlayer(48000)
	if err := p.Load(diPath); err != nil {
		t.Fatal(err)
	}
	p.Play()
	buf := make([]float64, 256)
	if a := testing.AllocsPerRun(100, func() { p.Read(buf) }); a != 0 {
		t.Errorf("Read aloca %.0f vezes", a)
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
