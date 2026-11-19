package engine

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/wav"
)

var (
	diPath = filepath.Join("..", "testdata", "guitarra_di.wav")
	irPath = filepath.Join("..", "testdata", "gabinete_2x12_sm57.wav")
)

func lerDI(t *testing.T) (float64, []float64) {
	t.Helper()
	precisa(t, diPath, irPath)
	fs, x, err := wav.ReadMonoFile(diPath)
	if err != nil {
		t.Fatal(err)
	}
	return float64(fs), x
}

// cadeia com tudo ligado e a IR carregada
func cadeiaCompleta(t *testing.T, fs float64, maxBlock int) *Chain {
	t.Helper()
	c := NewDefaultChain()
	for _, m := range c.Modules() {
		c.SetEnabled(m.ID(), true)
	}
	if err := c.Cab().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	c.Prepare(fs, maxBlock)
	return c
}

func rmsDB(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return 10 * math.Log10(s/float64(len(x))+1e-300)
}

// A saída não pode depender do tamanho de bloco do host.
func TestInvarianciaDeBloco(t *testing.T) {
	fs, di := lerDI(t)
	di = di[:44100]
	var ref []float64
	for _, bloco := range []int{512, 1, 37, 64, 500} {
		c := cadeiaCompleta(t, fs, 512)
		out := append([]float64(nil), di...)
		for i := 0; i < len(out); i += bloco {
			c.ProcessMono(out[i:min(i+bloco, len(out))])
		}
		if ref == nil {
			ref = out
			continue
		}
		for i := range out {
			if out[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere (%g != %g)", bloco, i, out[i], ref[i])
			}
		}
	}
}

func TestCadeiaSemAlocacao(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 256)
	in := [][]float32{make([]float32, 256), make([]float32, 256)}
	out := [][]float32{make([]float32, 256), make([]float32, 256)}
	for i := range in[0] {
		in[0][i] = float32(di[10000+i])
		in[1][i] = in[0][i]
	}
	liga := false
	a := testing.AllocsPerRun(50, func() {
		liga = !liga
		c.SetEnabled("chorus", liga) // inclui crossfade de bypass
		c.Process(in, out, 256)
	})
	if a != 0 {
		t.Errorf("Process aloca %.0f vezes por bloco", a)
	}
}

// Com tudo em bypass, a saída é a entrada atrasada exatamente pela latência da cadeia.
func TestBypassMantemLatencia(t *testing.T) {
	fs, di := lerDI(t)
	c := NewDefaultChain()
	for _, id := range []string{"gate", PreID, PowerID, CabID} {
		c.SetEnabled(id, false)
	}
	c.Prepare(fs, 128)
	lat := c.Latency()
	if want := dsp.PreampLatency + dsp.DriveLatency + dsp.SynthLatency; lat != want { // os únicos com latência
		t.Errorf("latência %d, esperava %d", lat, want)
	}
	x := di[:10000]
	out := append([]float64(nil), x...)
	c.ProcessMono(out)
	for i := lat; i < len(out); i++ {
		if out[i] != x[i-lat] {
			t.Fatalf("amostra %d: %g != %g", i, out[i], x[i-lat])
		}
	}
}

// O bug original: ruído no fim do áudio. Depois que a guitarra para, a saída tem que ir a silêncio.
func TestFimSemRuido(t *testing.T) {
	fs, di := lerDI(t)
	c := NewDefaultChain() // gate + amp
	if err := c.Cab().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	c.Prepare(fs, 256)
	// mesmo fade de 30 ms que o main.py aplica na DI (o arquivo termina num clique)
	fade := int(0.03 * fs)
	for i := 0; i < fade; i++ {
		g := 1 - float64(i)/float64(fade-1)
		di[len(di)-fade+i] *= g * g
	}
	out := append(append([]float64(nil), di...), make([]float64, int(fs))...)
	c.ProcessMono(out)
	tail := out[len(out)-int(0.5*fs):]
	t.Logf("pico %.2f, RMS dos últimos 0,5 s: %.1f dBFS", peak(out), rmsDB(tail))
	if rmsDB(tail) > -90 {
		t.Errorf("cauda com %.1f dBFS, esperava silêncio", rmsDB(tail))
	}
}

func peak(x []float64) float64 {
	p := 0.0
	for _, v := range x {
		p = math.Max(p, math.Abs(v))
	}
	return p
}

// Ligar e desligar pedais e girar knobs ao vivo não pode gerar NaN nem explosão.
func TestAutomacaoAoVivo(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 128)
	ids := c.Order()
	out := append([]float64(nil), di...)
	for i, k := 0, 0; i < len(out); i, k = i+128, k+1 {
		if k%20 == 0 {
			id := ids[(k/20)%len(ids)]
			c.SetEnabled(id, !c.Enabled(id))
			for _, p := range c.Module(id).Params() {
				p.SetNormalized(float64(k%7) / 6)
			}
		}
		c.ProcessMono(out[i:min(i+128, len(out))])
	}
	for i, v := range out {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 20 {
			t.Fatalf("amostra %d inválida: %g", i, v)
		}
	}
}

func TestPresetIdaEVolta(t *testing.T) {
	precisa(t, irPath)
	a := NewDefaultChain()
	a.Module(PreID).Param("gain").Set(8.2)
	a.Module("delay").Param("time").Set(450)
	a.SetEnabled("delay", true)
	a.SetEnabled("gate", false)
	if err := a.Cab().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a.State())
	if err != nil {
		t.Fatal(err)
	}

	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	c := NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	if g := c.Module(PreID).Param("gain").Get(); g != 8.2 {
		t.Errorf("gain = %v", g)
	}
	if !c.Enabled("delay") || c.Enabled("gate") {
		t.Error("estado ligado/desligado não foi restaurado")
	}
	if c.Cab().IRPath() != irPath {
		t.Errorf("IR = %q", c.Cab().IRPath())
	}

	st.Modules = append(st.Modules, ModuleState{ID: "talkbox"})
	if err := c.ApplyState(st); err == nil {
		t.Error("módulo desconhecido deveria gerar erro")
	}
}

func TestParametroDiscreto(t *testing.T) {
	p := NewPhaser().Param("stages")
	for _, c := range []struct{ in, want float64 }{{4.9, 4}, {5.2, 6}, {7.2, 8}, {12, 8}, {0, 4}} {
		p.Set(c.in)
		if p.Get() != c.want {
			t.Errorf("Set(%v) → %v, esperava %v", c.in, p.Get(), c.want)
		}
	}
}

// Trocar a IR com o áudio rodando (outra goroutine) não pode gerar corrida de dados.
func TestTrocaDeIRAoVivo(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 128)
	fsIR, raw, err := wav.ReadMonoFile(irPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			c.Cab().LoadIR(raw[:len(raw)/(i+1)], fsIR, "")
		}
	}()
	out := append([]float64(nil), di[:44100]...)
	for i := 0; i < len(out); i += 128 {
		c.ProcessMono(out[i:min(i+128, len(out))])
	}
	<-done
	c.ProcessMono(make([]float64, 128)) // aplica a última IR pendente
}

// O crossfade de bypass precisa durar BypassFadeMs (não pode ser instantâneo).
func TestCrossfadeDeBypass(t *testing.T) {
	const fs = 48000.0
	c := NewChain(NewChorus())
	c.Module("chorus").Param("mix").Set(100)
	c.Module("chorus").Param("depth").Set(0)
	c.SetEnabled("chorus", false)
	c.Prepare(fs, 256)
	// entrada constante 1: seco = 1; chorus 100% wet com atraso fixo de 22 ms = 0 no início
	buf := make([]float64, int(0.03*fs))
	for i := range buf {
		buf[i] = 1
	}
	c.SetEnabled("chorus", true)
	c.ProcessMono(buf)
	fade := int(BypassFadeMs / 1000 * fs)
	if got := buf[fade/2]; math.Abs(got-0.5) > 0.01 {
		t.Errorf("no meio do crossfade a saída deveria ser ~0,5, veio %.3f", got)
	}
	if buf[fade+10] != 0 {
		t.Errorf("depois do crossfade deveria ser só o efeito (0), veio %.3f", buf[fade+10])
	}
}

func TestOrdemValidacao(t *testing.T) {
	c := NewDefaultChain()
	ruins := map[string][]string{
		"sem o power":     {"gate", PreID, CabID},
		"repetido":        {"gate", PreID, "chorus", "chorus", "phaser", "delay", "reverb", PowerID, CabID},
		"desconhecido":    {"gate", PreID, "chorus", "talkbox", "phaser", "delay", "reverb", PowerID, CabID},
		"power antes pre": {"gate", PowerID, "chorus", "flanger", "phaser", "delay", "reverb", PreID, CabID},
		"cab antes power": {"gate", PreID, "chorus", "flanger", "phaser", "delay", "reverb", CabID, PowerID},
	}
	for nome, ids := range ruins {
		if err := c.SetOrder(ids); err == nil {
			t.Errorf("%s: deveria dar erro", nome)
		}
	}
	if err := c.Move(PowerID, 0); err == nil {
		t.Error("mover o power para antes do pré deveria dar erro")
	}
	if got := c.Order(); !slices.Equal(got, DefaultOrder) {
		t.Errorf("ordem inválida não pode mudar nada: %v", got)
	}

	// delay depois do gabinete, chorus na frente do amp
	if err := c.MoveAfter("delay", CabID); err != nil {
		t.Fatal(err)
	}
	if err := c.MoveBefore("chorus", PreID); err != nil {
		t.Fatal(err)
	}
	want := slices.DeleteFunc(slices.Clone(DefaultOrder), func(id string) bool { return id == "delay" || id == "chorus" })
	want = append(want, "delay")
	want = slices.Insert(want, slices.Index(want, PreID), "chorus")
	if got := c.Order(); !slices.Equal(got, want) {
		t.Errorf("ordem %v, esperava %v", got, want)
	}
	secoes := map[string]Section{"chorus": SectionInput, "reverb": SectionLoop, "delay": SectionPost, PreID: SectionAmp}
	for id, s := range secoes {
		if got := c.SectionOf(id); got != s {
			t.Errorf("%s está em %q, esperava %q", id, got, s)
		}
	}
}

// Reordenar com o áudio rodando: a troca acontece com rampa até o silêncio, sem NaN.
func TestReordenarAoVivo(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 128)
	lat := c.Latency()
	out := append([]float64(nil), di[:44100]...)
	troca := 20000
	for i := 0; i < len(out); i += 128 {
		if i == troca/128*128 {
			if err := c.MoveAfter("delay", CabID); err != nil { // tira o delay do loop
				t.Fatal(err)
			}
		}
		c.ProcessMono(out[i:min(i+128, len(out))])
	}
	if c.Latency() != lat {
		t.Error("a latência não pode mudar com a ordem")
	}
	// procura o ponto de silêncio da rampa logo depois do pedido
	fade := int(ReorderFadeMs / 1000 * fs)
	zeros := 0
	for i := troca; i < troca+3*fade+256; i++ {
		if out[i] == 0 {
			zeros++
		}
	}
	if zeros == 0 {
		t.Error("esperava a rampa até o silêncio na troca de ordem")
	}
	// depois da rampa o som volta
	if rmsDB(out[troca+4*fade+256:troca+4*fade+2256]) < -60 {
		t.Error("o som não voltou depois da troca de ordem")
	}
	for i, v := range out {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("amostra %d inválida", i)
		}
	}
}

// O mesmo delay no loop e depois do gabinete soa diferente (o loop passa pelo power e pela IR).
func TestLoopDeEfeitos(t *testing.T) {
	fs, di := lerDI(t)
	render := func(mover func(c *Chain) error) []float64 {
		c := NewDefaultChain()
		if err := c.Cab().LoadIRFile(irPath); err != nil {
			t.Fatal(err)
		}
		c.SetEnabled("delay", true)
		if err := mover(c); err != nil {
			t.Fatal(err)
		}
		c.Prepare(fs, 256)
		out := append([]float64(nil), di...)
		c.ProcessMono(out)
		return out
	}
	noLoop := render(func(c *Chain) error { return c.MoveBefore("delay", PowerID) })
	depois := render(func(c *Chain) error { return c.MoveAfter("delay", CabID) })
	diff := make([]float64, len(noLoop))
	for i := range diff {
		diff[i] = noLoop[i] - depois[i]
	}
	t.Logf("diferença loop × pós-gabinete: %.1f dB", rmsDB(diff)-rmsDB(noLoop))
	if rmsDB(diff)-rmsDB(noLoop) < -40 {
		t.Error("loop e pós-gabinete deram exatamente o mesmo resultado")
	}
}

func TestPresetGuardaOrdem(t *testing.T) {
	a := NewDefaultChain()
	if err := a.MoveAfter("reverb", CabID); err != nil {
		t.Fatal(err)
	}
	if err := a.MoveBefore("phaser", PreID); err != nil {
		t.Fatal(err)
	}
	b := NewDefaultChain()
	if err := b.ApplyState(a.State()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Order(), b.Order()) {
		t.Errorf("ordem %v, esperava %v", b.Order(), a.Order())
	}
}

func TestPresetV1Migracao(t *testing.T) {
	precisa(t, irPath)
	v1 := `{"version":1,"modules":[
	  {"id":"gate","enabled":false,"params":{"threshold":-50}},
	  {"id":"amp","enabled":true,"params":{"gain":8,"bass":6,"presence":7,"volume":-9},"extra":{"ir":"` + irPath + `"}},
	  {"id":"delay","enabled":true,"params":{"time":500}}]}`
	var st State
	if err := json.Unmarshal([]byte(v1), &st); err != nil {
		t.Fatal(err)
	}
	c := NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	checa := func(mod, par string, want float64) {
		if got := c.Module(mod).Param(par).Get(); got != want {
			t.Errorf("%s.%s = %v, esperava %v", mod, par, got, want)
		}
	}
	checa(PreID, "gain", 8)
	checa(PreID, "bass", 6)
	checa(PowerID, "presence", 7)
	checa(PowerID, "volume", -9)
	checa("delay", "time", 500)
	if c.Cab().IRPath() != irPath {
		t.Errorf("IR = %q", c.Cab().IRPath())
	}
	// v1 não tinha ordem: ficam na cadeia o amp e os pedais ligados, na ordem padrão
	if want := []string{PreID, "delay", PowerID, CabID}; !slices.Equal(c.Order(), want) {
		t.Errorf("ordem %v, esperava %v", c.Order(), want)
	}
}

// Girar o low cut / high cut do gabinete ao vivo não aloca.
func TestCabFiltrosAoVivoSemAlocacao(t *testing.T) {
	c := NewChain(NewCab())
	c.Prepare(48000, 256)
	buf := make([]float64, 256)
	k := 0.0
	if a := testing.AllocsPerRun(50, func() {
		k++
		c.Module(CabID).Param("lowcut").Set(60 + k)
		c.Module(CabID).Param("highcut").Set(5000 + 10*k)
		c.ProcessMono(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes", a)
	}
}

func TestDescricaoDaCadeia(t *testing.T) {
	c := NewDefaultChain()
	if err := c.SetOrder([]string{"gate", "comp", PreID, "eq", "chorus", "delay", "reverb", PowerID, CabID}); err != nil {
		t.Fatal(err)
	}
	c.SetEnabled("delay", true)
	if err := c.MoveAfter("reverb", CabID); err != nil {
		t.Fatal(err)
	}
	want := "Gate → Compressor (off) → Pré-amp → [loop: EQ (off) → Chorus (off) → Delay] → Power amp → Gabinete → Reverb (off)"
	if got := c.String(); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
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

// Preset salvo antes do compressor existir: a ordem do preset vale e o compressor entra no lugar padrão.
// Preset da versão 2 (listava todos os módulos): ficam na cadeia os ligados e o amp, na
// ordem do preset; os desligados saem (não mudavam o som).
func TestPresetAntigoSemCompressor(t *testing.T) {
	st := State{Version: 2}
	for _, id := range []string{"gate", PreID, "chorus", "flanger", "phaser", "reverb", PowerID, CabID, "delay"} {
		st.Modules = append(st.Modules, ModuleState{ID: id, Enabled: id == "delay" || id == "gate" || id == PreID || id == PowerID || id == CabID})
	}
	c := NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	want := []string{"gate", PreID, PowerID, CabID, "delay"}
	if !slices.Equal(c.Order(), want) {
		t.Errorf("ordem %v, esperava %v", c.Order(), want)
	}
}

func TestCompressorNaCadeia(t *testing.T) {
	c := NewChain(NewCompressor())
	c.Module("comp").Param("threshold").Set(-30)
	c.Module("comp").Param("level").Set(0)
	c.Prepare(48000, 256)
	buf := make([]float64, 4800)
	for i := range buf {
		buf[i] = 0.5 * math.Sin(float64(i)*0.06) // −6 dBFS
	}
	c.ProcessMono(buf)
	gr := c.Module("comp").(GainReducer).GainReductionDB()
	// 24 dB acima do limiar, razão 4:1 → 18 dB de redução
	if math.Abs(gr-18) > 1.5 {
		t.Errorf("redução de ganho %.1f dB, esperava ≈ 18 dB", gr)
	}
}

func TestTapTempo(t *testing.T) {
	var tt TapTempo
	t0 := time.Unix(100, 0)
	if _, ok := tt.Tap(t0); ok {
		t.Error("um toque só não define andamento")
	}
	ms, ok := tt.Tap(t0.Add(500 * time.Millisecond))
	if !ok || ms != 500 {
		t.Errorf("2 toques a 500 ms: %v %v", ms, ok)
	}
	// média dos últimos 4 intervalos: 500, 520, 480, 500 → 500
	at := t0.Add(500 * time.Millisecond)
	for _, d := range []int{520, 480, 500} {
		at = at.Add(time.Duration(d) * time.Millisecond)
		ms, _ = tt.Tap(at)
	}
	if math.Abs(ms-500) > 1e-9 {
		t.Errorf("média %v, esperava 500", ms)
	}
	// pausa longa: recomeça
	if _, ok := tt.Tap(at.Add(3 * time.Second)); ok {
		t.Error("depois de 3 s parado, o próximo toque recomeça a contagem")
	}
	s, err := FindSubdivision("1/8.")
	if err != nil || DelayMs(120, s) != 375 || BPM(375, s) != 120 {
		t.Errorf("120 BPM em colcheia pontuada = 375 ms: %v %v", DelayMs(120, s), err)
	}
	if _, err := FindSubdivision("1/3"); err == nil {
		t.Error("subdivisão desconhecida deveria dar erro")
	}
}

// A exportação offline é igual a processar a mesma entrada numa cadeia com o mesmo estado,
// alinhada pela latência, e não mexe na cadeia de origem.
func TestRenderOffline(t *testing.T) {
	fs, di := lerDI(t)
	di = di[:20000]
	ao := NewDefaultChain()
	if err := ao.Cab().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	ao.SetEnabled("delay", true)
	ao.Module(PreID).Param("gain").Set(8)
	st := ao.State()

	var prog []float64
	got, err := RenderOffline(st, di, fs, 0.5, 256, func(f float64) { prog = append(prog, f) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(di)+int(0.5*fs) {
		t.Fatalf("%d amostras, esperava %d", len(got), len(di)+int(0.5*fs))
	}
	ref := NewDefaultChain()
	if err := ref.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	ref.Prepare(fs, 256)
	buf := append(append([]float64(nil), di...), make([]float64, int(0.5*fs)+ref.Latency())...)
	ref.ProcessMono(buf)
	for i := range got {
		if got[i] != buf[i+ref.Latency()] {
			t.Fatalf("amostra %d difere", i)
		}
	}
	if len(prog) < 2 || prog[len(prog)-1] != 1 {
		t.Errorf("progresso deveria terminar em 1: %v", prog)
	}
	if ao.Module(PreID).Param("gain").Get() != 8 || !ao.Enabled("delay") {
		t.Error("a cadeia de origem não pode mudar")
	}
}

// Trocar o tone stack ao vivo: sem NaN, sem salto (crossfade), o som muda, e a saída
// continua independente do tamanho de bloco.
func TestToneStackModeladoNaCadeia(t *testing.T) {
	fs, di := lerDI(t)
	di = di[:30000]
	render := func(bloco int, troca bool) []float64 {
		c := NewChain(NewPre())
		c.Prepare(fs, 512)
		out := append([]float64(nil), di...)
		for i := 0; i < len(out); i += bloco {
			if troca && i >= 10000 {
				c.Module(PreID).Param("stack").Set(StackAmerican)
			}
			c.ProcessMono(out[i:min(i+bloco, len(out))])
		}
		return out
	}
	moderno := render(512, false)
	fender := render(512, true)
	for _, v := range fender {
		if math.IsNaN(v) || math.Abs(v) > 4 {
			t.Fatal("valor inválido depois da troca")
		}
	}
	diff := 0.0
	for i := 20000; i < 30000; i++ {
		diff += (fender[i] - moderno[i]) * (fender[i] - moderno[i])
	}
	if diff < 1e-3 {
		t.Error("trocar para o Fender deveria mudar o som")
	}
	// Invariância de bloco: com a troca no mesmo ponto (10240 é múltiplo de todos os blocos
	// abaixo), a saída tem que ser idêntica bit a bit, inclusive durante o crossfade.
	ref := renderTroca(t, fs, di, 512)
	for _, b := range []int{1, 16, 64, 1024} {
		r := renderTroca(t, fs, di, b)
		for i := range r {
			if r[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere", b, i)
			}
		}
	}
}

func renderTroca(t *testing.T, fs float64, di []float64, bloco int) []float64 {
	t.Helper()
	c := NewChain(NewPre())
	c.Prepare(fs, 1024)
	out := append([]float64(nil), di...)
	for i := 0; i < len(out); i += bloco {
		if i == 10240 {
			c.Module(PreID).Param("stack").Set(StackBritish)
		}
		c.ProcessMono(out[i:min(i+bloco, len(out))])
	}
	return out
}

func TestToneStackSemAlocacao(t *testing.T) {
	c := NewChain(NewPre())
	c.Prepare(48000, 256)
	buf := make([]float64, 256)
	k := 0
	if a := testing.AllocsPerRun(60, func() {
		k++
		c.Module(PreID).Param("stack").Set(float64(k / 10 % 4)) // troca a cada 10 blocos, inclusive o High Gain
		c.Module(PreID).Param("bass").Set(float64(k % 10))
		c.ProcessMono(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}

// Sem IR escolhida, o gabinete usa a IR interna (a 2x12 SM57 embutida, a mesma de
// testdata/): o som é idêntico ao de carregar o arquivo, e o caminho da IR fica vazio.
func TestGabineteInternoSemIR(t *testing.T) {
	fs, di := lerDI(t)
	render := func(cab, file bool) []float64 {
		c := NewDefaultChain()
		c.SetEnabled(CabID, cab)
		if file {
			if err := c.Cab().LoadIRFile(irPath); err != nil {
				t.Fatal(err)
			}
		}
		c.Prepare(fs, 256)
		x := append([]float64(nil), di[:40000]...)
		c.ProcessMono(x)
		return x
	}
	com, sem, arq := render(true, false), render(false, false), render(true, true)
	d := 0.0
	for i := range com {
		d += (com[i] - sem[i]) * (com[i] - sem[i])
		if com[i] != arq[i] {
			t.Fatalf("amostra %d: a IR interna difere do arquivo de testdata", i)
		}
	}
	if d < 1e-3 {
		t.Error("o gabinete interno deveria mudar o som")
	}
	if NewDefaultChain().Cab().IRPath() != "" {
		t.Error("sem IR carregada, IRPath é vazio")
	}
}

// Adicionar e remover pedais: posição padrão, partes do amp fixas, estado v3 só com os
// presentes, latência só dos presentes e o pedal que volta começa limpo.
func TestAdicionarERemover(t *testing.T) {
	c := NewDefaultChain()
	if err := c.SetOrder([]string{"gate", PreID, PowerID, CabID}); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("delay"); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("comp"); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("chorus"); err != nil {
		t.Fatal(err)
	}
	want := []string{"gate", "comp", PreID, "chorus", "delay", PowerID, CabID}
	if !slices.Equal(c.Order(), want) || !c.Enabled("delay") {
		t.Fatalf("ordem %v (delay ligado: %v), esperava %v", c.Order(), c.Enabled("delay"), want)
	}
	if err := c.Remove(PowerID); err == nil {
		t.Error("o power não pode sair da cadeia")
	}
	if err := c.Remove("chorus"); err != nil || c.InChain("chorus") {
		t.Errorf("remover o chorus: %v", err)
	}
	av := c.Available()
	if len(av)+len(c.Order()) != len(c.AllModules()) {
		t.Error("disponíveis + na cadeia = todos")
	}
	// estado só com os presentes, e volta igual
	st := c.State()
	if st.Version != StateVersion || len(st.Modules) != len(c.Order()) {
		t.Fatalf("estado v%d com %d módulos", st.Version, len(st.Modules))
	}
	d := NewDefaultChain()
	if err := d.ApplyState(st); err != nil || !slices.Equal(d.Order(), c.Order()) {
		t.Errorf("aplicar o estado: %v, ordem %v", err, d.Order())
	}
	// latência: só dos presentes (o pré tem a do oversampling)
	if c.Latency() != NewChain(NewPre()).Latency() {
		t.Errorf("latência %d", c.Latency())
	}
	// ligar pela lista adiciona
	if err := c.EnableList("reverb", true); err != nil || !c.InChain("reverb") {
		t.Errorf("on reverb: %v", err)
	}
}

// Ao vivo: o pedal que entra começa limpo (sem a cauda de quando saiu) e não aloca.
func TestAdicionarAoVivo(t *testing.T) {
	c := NewChain(NewGate(), NewDelay()) // só o delay soa (o gate fica desligado)
	c.SetEnabled("gate", false)
	c.Module("delay").Param("mix").Set(100)
	c.Prepare(48000, 256)
	buf := make([]float64, 256)
	buf[0] = 1
	c.ProcessMono(buf) // impulso entra no delay
	if err := c.Remove("delay"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ { // rampa de troca
		clear(buf)
		c.ProcessMono(buf)
	}
	if err := c.Add("delay"); err != nil {
		t.Fatal(err)
	}
	peak := 0.0
	for i := 0; i < 200; i++ { // 1 s: o eco do impulso antigo não pode aparecer
		clear(buf)
		c.ProcessMono(buf)
		for _, v := range buf {
			peak = math.Max(peak, math.Abs(v))
		}
	}
	if peak > 1e-6 {
		t.Errorf("o delay voltou com a cauda antiga (pico %g)", peak)
	}
	// trocar de pedais com o áudio rodando não aloca na thread de áudio
	d := NewDefaultChain()
	d.Prepare(48000, 256)
	ords := [][]string{{PreID, PowerID, CabID}, {"gate", PreID, "delay", PowerID, CabID}}
	for _, o := range ords { // as ordens já validadas (SetOrder aloca, mas fora do áudio)
		d.SetOrder(o)
		for i := 0; i < 5; i++ {
			d.ProcessMono(buf)
		}
	}
	if a := testing.AllocsPerRun(30, func() { d.ProcessMono(buf) }); a != 0 {
		t.Errorf("processar aloca %.0f vezes", a)
	}
}

// Preset da versão 3: o grave dos tone stacks TMB vai para a curva nova do knob, com o pot na
// mesma posição (o som não muda); no Moderno (outra curva) fica como está.
func TestPresetV3GraveTMB(t *testing.T) {
	for _, tc := range []struct {
		stack float64
		model dsp.TMBModel
	}{{StackAmerican, dsp.TMBFender}, {StackBritish, dsp.TMBMarshall}, {StackHighGain, dsp.TMBHighGain}, {StackModern, dsp.TMBModel{}}} {
		for _, old := range []float64{0, 3, 5, 10} {
			params := map[string]float64{"bass": old, "stack": tc.stack}
			st := State{Version: 3, Modules: []ModuleState{{ID: PreID, Enabled: true, Params: params}}}
			c := NewDefaultChain()
			if err := c.ApplyState(st); err != nil {
				t.Fatal(err)
			}
			if params["bass"] != old {
				t.Fatal("a migração mudou o estado original")
			}
			got := c.Module(PreID).Param("bass").Get()
			if tc.stack == StackModern {
				if got != old {
					t.Errorf("Moderno: grave %v virou %v", old, got)
				}
				continue
			}
			if d := math.Abs(math.Log(tc.model.BassPot(got) / dsp.LegacyBassTaper(old))); d > 0.01 {
				t.Errorf("%s grave %v → %v: pot %.4f, antes %.4f", tc.model.Name, old, got, tc.model.BassPot(got), dsp.LegacyBassTaper(old))
			}
			// e a versão atual não é convertida de novo
			d := NewDefaultChain()
			if err := d.ApplyState(c.State()); err != nil || d.Module(PreID).Param("bass").Get() != got {
				t.Errorf("reaplicar o estado mudou o grave: %v", err)
			}
		}
	}
}
