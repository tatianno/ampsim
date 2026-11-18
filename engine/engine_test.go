package engine

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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
	if lat != 32 {
		t.Errorf("latência %d, esperava 32", lat)
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

	st.Modules = append(st.Modules, ModuleState{ID: "wah"})
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
		"faltando":        {"gate", PreID, PowerID, CabID},
		"repetido":        {"gate", PreID, "chorus", "chorus", "phaser", "delay", "reverb", PowerID, CabID},
		"desconhecido":    {"gate", PreID, "chorus", "wah", "phaser", "delay", "reverb", PowerID, CabID},
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
	want := []string{"gate", "comp", "chorus", PreID, "flanger", "phaser", "reverb", PowerID, CabID, "delay"}
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
	if !slices.Equal(c.Order(), DefaultOrder) {
		t.Error("preset v1 não define ordem: tem que ficar a padrão")
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
	c.SetEnabled("delay", true)
	if err := c.MoveAfter("reverb", CabID); err != nil {
		t.Fatal(err)
	}
	want := "Gate → Compressor (off) → Pré-amp → [loop: Chorus (off) → Flanger (off) → Phaser (off) → Delay] → Power amp → Gabinete → Reverb (off)"
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

func TestMergeOrder(t *testing.T) {
	atual := DefaultOrder // gate, comp, pre, chorus, flanger, phaser, delay, reverb, power, cab
	casos := []struct {
		preset, want []string
	}{
		// preset antigo (sem compressor), com o delay depois do gabinete
		{[]string{"gate", PreID, "chorus", "flanger", "phaser", "reverb", PowerID, CabID, "delay"},
			[]string{"gate", "comp", PreID, "chorus", "flanger", "phaser", "reverb", PowerID, CabID, "delay"}},
		// preset completo: igual
		{DefaultOrder, DefaultOrder},
		// id desconhecido é ignorado; o resto completa
		{[]string{"wah", PreID, PowerID, CabID}, []string{"gate", "comp", PreID, "chorus", "flanger", "phaser", "delay", "reverb", PowerID, CabID}},
	}
	for _, c := range casos {
		if got := MergeOrder(c.preset, atual); !slices.Equal(got, c.want) {
			t.Errorf("MergeOrder(%v)\n got %v\nwant %v", c.preset, got, c.want)
		}
	}
}

// Preset salvo antes do compressor existir: a ordem do preset vale e o compressor entra no lugar padrão.
func TestPresetAntigoSemCompressor(t *testing.T) {
	st := State{Version: 2}
	for _, id := range []string{"gate", PreID, "chorus", "flanger", "phaser", "reverb", PowerID, CabID, "delay"} {
		st.Modules = append(st.Modules, ModuleState{ID: id, Enabled: id == "delay" || id == "gate" || id == PreID || id == PowerID || id == CabID})
	}
	c := NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	want := []string{"gate", "comp", PreID, "chorus", "flanger", "phaser", "reverb", PowerID, CabID, "delay"}
	if !slices.Equal(c.Order(), want) {
		t.Errorf("ordem %v, esperava %v", c.Order(), want)
	}
	if c.Enabled("comp") {
		t.Error("o compressor não estava no preset: deveria continuar como estava (desligado)")
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
