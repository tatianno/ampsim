package audio

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/wav"
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

// render com a cadeia vazia (saída = entrada) e a guitarra como fonte
func renderVivo(t *testing.T) (*Renderer, *source.LiveInput) {
	t.Helper()
	const fs = 48000
	chain := engine.NewChain()
	chain.Prepare(fs, MaxBlock)
	r := NewRenderer(chain, fs)
	l := source.NewLiveInput(fs)
	r.SetLive(l)
	r.SetSource(l)
	return r, l
}

func TestRenderDuplexGuitarra(t *testing.T) {
	r, l := renderVivo(t)
	l.SetChannel(1)
	const n = 256
	in := make([]float32, 2*n)
	for i := 0; i < n; i++ {
		in[2*i+1] = 0.3 // guitarra no canal 2
	}
	out := make([]float32, 2*n)
	r.RenderDuplex(out, 2, in, 2, n)
	for i, v := range out {
		if math.Abs(float64(v)-0.3) > 1e-6 {
			t.Fatalf("saída[%d] = %v, esperava 0,3", i, v)
		}
	}
	m, _ := r.Meters().Pop()
	if math.Abs(float64(m.InRaw)-0.3) > 1e-6 || m.InClips != 0 {
		t.Errorf("medidor da placa: %.3f, %d clips", m.InRaw, m.InClips)
	}

	// conversor clipando
	for i := range in {
		in[i] = 1
	}
	r.RenderDuplex(out, 2, in, 2, n)
	if m, _ := r.Meters().Pop(); m.InClips != n {
		t.Errorf("esperava %d clips da placa, veio %d", n, m.InClips)
	}
}

func TestRenderDuplexSemAlocacao(t *testing.T) {
	r, _ := renderVivo(t)
	in := make([]float32, 2*128)
	out := make([]float32, 2*128)
	if a := testing.AllocsPerRun(100, func() {
		r.RenderDuplex(out, 2, in, 2, 128)
		r.Meters().Pop()
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por callback", a)
	}
}

// loopback simulado: a entrada recebe a saída de atraso quadros atrás (mais um ruído baixo)
func rodarLoopback(t *testing.T, r *Renderer, atraso int, ligado bool) LatencyResult {
	t.Helper()
	const n = 256
	hist := make([]float32, 0, 1<<20)
	in := make([]float32, 2*n)
	out := make([]float32, 2*n)
	for k := 0; k < 4000 && !r.LatencyResult().Done; k++ {
		base := len(hist)
		for i := 0; i < n; i++ {
			v := float32(0.001 * math.Sin(float64(base+i)))
			if j := base + i - atraso; ligado && j >= 0 {
				v += 0.8 * hist[j]
			}
			in[2*i], in[2*i+1] = v, v
		}
		r.RenderDuplex(out, 2, in, 2, n)
		r.Meters().Pop()
		for i := 0; i < n; i++ {
			hist = append(hist, out[2*i])
		}
	}
	return r.LatencyResult()
}

func TestTesteDeLatencia(t *testing.T) {
	r, _ := renderVivo(t)
	if err := r.StartLatencyTest(); err != nil {
		t.Fatal(err)
	}
	if !r.LatencyResult().Running {
		t.Error("deveria indicar que está rodando")
	}
	res := rodarLoopback(t, r, 1234, true)
	if !res.OK || res.Frames != 1234 || res.Successes != LatencyAttempts {
		t.Fatalf("resultado %+v, esperava 1234 amostras em %d medições", res, LatencyAttempts)
	}
	if math.Abs(res.Ms-1234.0/48) > 1e-9 {
		t.Errorf("%.3f ms", res.Ms)
	}
	// sem o cabo: o pulso nunca volta
	r2, _ := renderVivo(t)
	r2.StartLatencyTest()
	if res := rodarLoopback(t, r2, 0, false); !res.Done || res.OK {
		t.Errorf("sem loopback deveria falhar: %+v", res)
	}
}

func TestTesteDeLatenciaPrecisaDeEntrada(t *testing.T) {
	chain := engine.NewChain()
	chain.Prepare(48000, MaxBlock)
	if err := NewRenderer(chain, 48000).StartLatencyTest(); err != ErrNoInput {
		t.Errorf("esperava ErrNoInput, veio %v", err)
	}
}

// Bug relatado: com a fonte em Arquivo tocando, o áudio que já saiu volta pelo cabo durante
// o silêncio inicial e infla a medida do ruído; o limiar fica acima do pulso e o teste falha.
func TestLatenciaComArquivoTocando(t *testing.T) {
	r, _, _ := novoRender(t) // cadeia completa + arquivo tocando
	r.SetLive(source.NewLiveInput(48000))
	const atraso = 4000 // 83 ms: maior que a margem inicial antiga de 50 ms
	// toca um pouco do arquivo com o cabo ligado, para haver áudio "em trânsito"
	const n = 256
	hist := make([]float32, 0, 1<<20)
	in := make([]float32, 2*n)
	out := make([]float32, 2*n)
	passo := func() {
		base := len(hist)
		for i := 0; i < n; i++ {
			v := float32(0)
			if j := base + i - atraso; j >= 0 {
				v = 0.8 * hist[j]
			}
			in[2*i], in[2*i+1] = v, v
		}
		r.RenderDuplex(out, 2, in, 2, n)
		r.Meters().Pop()
		for i := 0; i < n; i++ {
			hist = append(hist, out[2*i])
		}
	}
	for k := 0; k < 100; k++ { // ~0,5 s de música
		passo()
	}
	if err := r.StartLatencyTest(); err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 4000 && !r.LatencyResult().Done; k++ {
		passo()
	}
	res := r.LatencyResult()
	if !res.OK || res.Frames != atraso {
		t.Fatalf("resultado %+v, esperava %d amostras", res, atraso)
	}
}

// Retorno fraco, ruído de fundo e eco repetido (monitor direto da placa ligado):
// a detecção pelo pico da janela continua achando a primeira chegada.
func TestLatenciaRetornoFracoComEco(t *testing.T) {
	r, _ := renderVivo(t)
	r.StartLatencyTest()
	const n, atraso = 256, 2000
	hist := make([]float32, 0, 1<<20)
	in := make([]float32, 2*n)
	out := make([]float32, 2*n)
	for k := 0; k < 4000 && !r.LatencyResult().Done; k++ {
		base := len(hist)
		for i := 0; i < n; i++ {
			v := float32(0.004 * math.Sin(float64(base+i)*0.37)) // ruído/hum de fundo (−48 dBFS)
			if j := base + i - atraso; j >= 0 {
				v += 0.1 * hist[j] // volta só 10% (−20 dB)
			}
			if j := base + i - 2*atraso; j >= 0 {
				v += 0.05 * hist[j] // eco
			}
			in[2*i], in[2*i+1] = v, v
		}
		r.RenderDuplex(out, 2, in, 2, n)
		r.Meters().Pop()
		for i := 0; i < n; i++ {
			hist = append(hist, out[2*i])
		}
	}
	res := r.LatencyResult()
	if !res.OK || res.Frames != atraso {
		t.Fatalf("resultado %+v, esperava %d amostras", res, atraso)
	}
	t.Logf("pico de volta %.1f dBFS, ruído %.1f dBFS", res.PeakDB, res.NoiseDB)
}

// Com mais de 2 canais de saída (JACK ligado a várias placas), só o primeiro par toca.
func TestSaidaSoNoPrimeiroPar(t *testing.T) {
	r, _ := renderVivo(t)
	const n = 64
	in := make([]float32, 2*n)
	for i := range in {
		in[i] = 0.25
	}
	out := make([]float32, 4*n)
	for i := range out {
		out[i] = 9 // lixo: tem que ser sobrescrito
	}
	r.RenderDuplex(out, 4, in, 2, n)
	for i := 0; i < n; i++ {
		f := out[4*i : 4*i+4]
		if f[0] != 0.25 || f[1] != 0.25 || f[2] != 0 || f[3] != 0 {
			t.Fatalf("quadro %d: %v, esperava [0,25 0,25 0 0]", i, f)
		}
	}
}

// Um som forte (voz) entrando numa das janelas é confundido com o pulso naquela medição:
// a mediana ainda acerta, mas o resultado é marcado como inconsistente.
func TestLatenciaComVozNaJanela(t *testing.T) {
	r, _ := renderVivo(t)
	r.StartLatencyTest()
	const n, atraso = 256, 2000
	hist := make([]float32, 0, 1<<20)
	in := make([]float32, 2*n)
	out := make([]float32, 2*n)
	inicioVoz := -1
	for k := 0; k < 4000 && !r.LatencyResult().Done; k++ {
		base := len(hist)
		// "voz" alta logo depois do 2º pulso (antes de ele voltar)
		if inicioVoz < 0 && r.lt.attempt == 1 && r.lt.state == ltWait {
			inicioVoz = base + 500
		}
		for i := 0; i < n; i++ {
			v := float32(0)
			if j := base + i - atraso; j >= 0 {
				v += 0.3 * hist[j]
			}
			if inicioVoz >= 0 && base+i >= inicioVoz && base+i < inicioVoz+2400 {
				v += 0.9 * float32(math.Sin(float64(base+i)*0.05))
			}
			in[2*i], in[2*i+1] = v, v
		}
		r.RenderDuplex(out, 2, in, 2, n)
		r.Meters().Pop()
		for i := 0; i < n; i++ {
			hist = append(hist, out[2*i])
		}
	}
	res := r.LatencyResult()
	if !res.OK || res.Frames != atraso {
		t.Fatalf("a mediana deveria acertar: %+v", res)
	}
	if res.Consistent {
		t.Errorf("deveria marcar como inconsistente (variação %.1f ms)", res.SpreadMs)
	}
}

type senoSource struct {
	f, fs, amp float64
	n          int
}

func (s *senoSource) Read(buf []float64) {
	for i := range buf {
		buf[i] = s.amp * math.Sin(2*math.Pi*s.f*float64(s.n)/s.fs)
		s.n++
	}
}

// O afinador ouve a entrada (antes da cadeia) e acha a nota; com a saída muda, nada sai.
func TestAfinadorEMudo(t *testing.T) {
	r, _ := renderVivo(t)
	tu := NewTuner(r)
	f := 110 * math.Pow(2, 15.0/1200) // Lá 2, 15 cents acima
	r.SetSource(&senoSource{f: f, fs: 48000, amp: 0.3})
	tu.Start()
	defer tu.Stop()
	r.SetMute(true)
	out := make([]float32, 2*256)
	var rd Reading
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.Render(out, 2, 256)
		r.Meters().Pop()
		time.Sleep(time.Millisecond)
		if rd = tu.Reading(); rd.OK {
			break
		}
	}
	if !rd.OK || rd.Note != "A" || rd.Octave != 2 || math.Abs(rd.Cents-15) > 1 {
		t.Fatalf("leitura %+v, esperava A2 +15 cents", rd)
	}
	for _, v := range out {
		if v != 0 {
			t.Fatal("com a saída muda, nada deveria sair")
		}
	}
	tu.SetA4(432)
	if tu.A4() != 432 {
		t.Error("A4 não mudou")
	}
	tu.SetA4(500)
	if tu.A4() != 450 {
		t.Errorf("A4 deveria ficar limitado a 450, veio %v", tu.A4())
	}
}

func TestRenderSemAlocacaoComAfinador(t *testing.T) {
	r, _ := renderVivo(t)
	tu := NewTuner(r)
	tu.Start()
	defer tu.Stop()
	r.SetMute(true)
	out := make([]float32, 2*256)
	if a := testing.AllocsPerRun(100, func() {
		r.Render(out, 2, 256)
		r.Meters().Pop()
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por callback", a)
	}
}

// Grava saída e DI de um seno conhecido: os dois arquivos têm o tamanho do que passou,
// a DI é a entrada exata e nada se perde.
func TestGravarSaidaEDI(t *testing.T) {
	r, _ := renderVivo(t) // cadeia vazia: saída = entrada
	rec := NewRecorder(r)
	src := &senoSource{f: 220, fs: 48000, amp: 0.4}
	r.SetSource(src)
	dir := t.TempDir()
	if _, err := rec.Start(dir, true); err != nil {
		t.Fatal(err)
	}
	out := make([]float32, 2*256)
	const blocos = 375 // 2 s
	for k := 0; k < blocos; k++ {
		r.Render(out, 2, 256)
		r.Meters().Pop()
		if k%20 == 0 {
			time.Sleep(time.Millisecond) // dá tempo à goroutine de esvaziar as filas
		}
	}
	take, err := rec.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if take.Dropped != 0 || math.Abs(take.Seconds-2) > 1e-9 {
		t.Errorf("take %+v: esperava 2 s sem perdas", take)
	}
	for _, path := range []string{take.Out, take.DI} {
		fs, x, err := wav.ReadMonoFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if fs != 48000 || len(x) != blocos*256 {
			t.Fatalf("%s: %d Hz, %d amostras", path, fs, len(x))
		}
		want := 0.4 * math.Sin(2*math.Pi*220*1000/48000)
		if math.Abs(x[1000]-want) > 1e-6 {
			t.Errorf("%s: amostra 1000 = %v, esperava %v", path, x[1000], want)
		}
	}
	if rec.Recording() {
		t.Error("deveria ter parado")
	}
	if _, err := rec.Stop(); err == nil {
		t.Error("parar sem estar gravando deveria dar erro")
	}
}

func TestRenderSemAlocacaoGravando(t *testing.T) {
	r, _ := renderVivo(t)
	rec := NewRecorder(r)
	if _, err := rec.Start(t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
	defer rec.Stop()
	out := make([]float32, 2*256)
	if a := testing.AllocsPerRun(100, func() {
		r.Render(out, 2, 256)
		r.Meters().Pop()
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por callback", a)
	}
}

func TestListarGravacoes(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, n int) {
		if err := wav.WriteMonoFile(filepath.Join(dir, name), 48000, make([]float64, n), RecordFormat); err != nil {
			t.Fatal(err)
		}
	}
	mk("ampsim-2026-11-18_10-00-00.wav", 48000)
	mk("ampsim-2026-11-18_10-00-00-DI.wav", 48000)
	mk("ampsim-2026-11-18_11-30-00.wav", 96000) // sem DI
	mk("outro-arquivo.wav", 10)
	l, err := ListRecordings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 2 {
		t.Fatalf("%d gravações: %+v", len(l), l)
	}
	if l[0].Seconds != 2 || l[0].DI != "" || l[1].Seconds != 1 || l[1].DI == "" {
		t.Errorf("ordem/DI/duração errados: %+v", l)
	}
	if l[1].Name() != "18/11 10:00:00" {
		t.Errorf("nome %q", l[1].Name())
	}
}
