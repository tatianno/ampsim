package ui

import (
	"github.com/tatianno/ampsim/wav"
	"image"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/engine"
)

var (
	diPath = filepath.Join("..", "..", "..", "testdata", "guitarra_di.wav")
	irPath = filepath.Join("..", "..", "..", "testdata", "gabinete_2x12_sm57.wav")
)

// harness roda quadros da GUI sem janela e injeta eventos de mouse e teclado.
type harness struct {
	r    input.Router
	ops  op.Ops
	size image.Point
	now  time.Time
	t    time.Duration
}

func newHarness(w, h int) *harness {
	return &harness{size: image.Pt(w, h), now: time.Unix(1000, 0)}
}

func (h *harness) frame(w layout.Widget) layout.Dimensions {
	h.ops.Reset()
	gtx := layout.Context{
		Ops:         &h.ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: h.size},
		Now:         h.now,
		Source:      h.r.Source(),
	}
	d := w(gtx)
	h.r.Frame(&h.ops)
	h.now = h.now.Add(16 * time.Millisecond)
	return d
}

func (h *harness) ptr(w layout.Widget, kind pointer.Kind, x, y float32, mods key.Modifiers) {
	h.t += 40 * time.Millisecond
	e := pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(x, y), Time: h.t, Modifiers: mods}
	if kind == pointer.Drag {
		e.Kind = pointer.Move // o Router transforma Move com botão apertado em Drag
	}
	if kind == pointer.Press || kind == pointer.Drag {
		e.Buttons = pointer.ButtonPrimary
	}
	h.r.Queue(e)
	h.frame(w)
}

func (h *harness) click(w layout.Widget, x, y float32) {
	h.ptr(w, pointer.Press, x, y, 0)
	h.ptr(w, pointer.Release, x, y, 0)
}

func (h *harness) scroll(w layout.Widget, x, y, dy float32) {
	h.t += 40 * time.Millisecond
	h.r.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(x, y), Scroll: f32.Pt(0, dy), Time: h.t})
	h.frame(w)
}

func TestFormatValue(t *testing.T) {
	casos := []struct {
		unit string
		step float64
		v    float64
		want string
	}{
		{"dB", 0, -12, "−12.0 dB"}, {"dB", 0, 3, "+3.0 dB"}, {"dB", 0, 0.01, "0.0 dB"},
		{"Hz", 0, 1.2, "1.20 Hz"}, {"Hz", 0, 85, "85.0 Hz"}, {"Hz", 0, 6200, "6.2 kHz"},
		{"ms", 0, 2.25, "2.25 ms"}, {"ms", 0, 380, "380 ms"}, {"%", 0, 45, "45%"},
		{"", 0, 6.5, "6.5"}, {"", 2, 6, "6"}, {"oct", 0, 3.17, "3.2 oct"}, {":1", 0, 4, "4.0:1"},
	}
	for _, c := range casos {
		if got := FormatValue(engine.ParamInfo{Unit: c.unit, Step: c.step}, c.v); got != c.want {
			t.Errorf("%v %s → %q, esperava %q", c.v, c.unit, got, c.want)
		}
	}
}

func TestKnobArrastarDuploCliqueERodinha(t *testing.T) {
	th := NewTheme()
	p := engine.NewParam(engine.ParamInfo{ID: "gain", Name: "Ganho", Min: 0, Max: 10, Default: 5})
	k := NewKnob(p)
	h := newHarness(200, 200)
	var dims layout.Dimensions
	w := func(gtx layout.Context) layout.Dimensions {
		dims = k.Layout(gtx, th, 56, rgb(0x111111), rgb(0xffffff), th.Gold)
		return dims
	}
	h.frame(w)
	cx, cy := float32(dims.Size.X)/2, float32(dims.Size.Y)/2

	// arrastar 90 px para cima = metade da faixa (180 dp)
	h.ptr(w, pointer.Press, cx, cy, 0)
	h.ptr(w, pointer.Drag, cx, cy-45, 0)
	h.ptr(w, pointer.Drag, cx, cy-90, 0)
	h.ptr(w, pointer.Release, cx, cy-90, 0)
	if math.Abs(p.Get()-10) > 1e-6 {
		t.Errorf("depois de arrastar 90 px para cima: %.3f, esperava 10", p.Get())
	}
	// com Shift: 5x mais fino
	h.t += time.Second
	h.ptr(w, pointer.Press, cx, cy, 0)
	h.ptr(w, pointer.Drag, cx, cy+90, key.ModShift)
	h.ptr(w, pointer.Release, cx, cy+90, key.ModShift)
	if math.Abs(p.Get()-9) > 1e-6 {
		t.Errorf("arrasto fino de 90 px para baixo: %.3f, esperava 9", p.Get())
	}
	// duplo clique volta ao padrão
	h.t += time.Second
	h.click(w, cx, cy)
	h.click(w, cx, cy)
	if p.Get() != 5 {
		t.Errorf("duplo clique: %.3f, esperava o padrão 5", p.Get())
	}
	// rodinha para baixo diminui 1% da faixa
	h.scroll(w, cx, cy, 10)
	if math.Abs(p.Get()-4.9) > 1e-6 {
		t.Errorf("rodinha: %.3f, esperava 4,9", p.Get())
	}
}

func boardHarness(t *testing.T) (*harness, *Board, *engine.Chain, layout.Widget) {
	t.Helper()
	th := NewTheme()
	c := engine.NewDefaultChain()
	b := NewBoard(c)
	h := newHarness(1000, 200)
	w := func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) }
	h.frame(w)
	return h, b, c, w
}

// centro do bloco i na faixa
func chipCenter(b *Board, i int) (float32, float32) {
	return float32(b.slotX(i)) + float32(b.chipW)/2, float32(b.top) + float32(b.chipH)*0.7
}

func TestBoardArrastarParaForaDoLoop(t *testing.T) {
	h, b, c, w := boardHarness(t)
	i := slices.Index(c.Order(), "delay")
	x, y := chipCenter(b, i)
	endX, _ := chipCenter(b, len(c.Order())-1)
	h.ptr(w, pointer.Press, x, y, 0)
	h.ptr(w, pointer.Drag, x+60, y, 0)
	if b.Dragging() != "delay" {
		t.Fatal("deveria estar arrastando o delay")
	}
	h.ptr(w, pointer.Drag, endX+40, y, 0)
	h.ptr(w, pointer.Release, endX+40, y, 0)
	got := c.Order()
	if got[len(got)-1] != "delay" {
		t.Errorf("delay deveria ir para depois do gabinete: %v", got)
	}
	if c.SectionOf("delay") != engine.SectionPost {
		t.Errorf("delay em %q", c.SectionOf("delay"))
	}

	// e de volta para a frente do amp (antes do PRÉ)
	i = slices.Index(c.Order(), "delay")
	x, y = chipCenter(b, i)
	h.ptr(w, pointer.Press, x, y, 0)
	h.ptr(w, pointer.Drag, x-100, y, 0)
	h.ptr(w, pointer.Drag, 5, y, 0)
	h.ptr(w, pointer.Release, 5, y, 0)
	if c.Order()[0] != "delay" || c.SectionOf("delay") != engine.SectionInput {
		t.Errorf("delay deveria estar na entrada: %v", c.Order())
	}
}

func TestBoardBlocoDoAmpNaoArrasta(t *testing.T) {
	h, b, c, w := boardHarness(t)
	antes := c.Order()
	x, y := chipCenter(b, slices.Index(antes, engine.PreID))
	h.ptr(w, pointer.Press, x, y, 0)
	h.ptr(w, pointer.Drag, x+300, y, 0)
	h.ptr(w, pointer.Release, x+300, y, 0)
	if !slices.Equal(c.Order(), antes) {
		t.Errorf("o PRÉ não pode ser arrastado: %v", c.Order())
	}
}

func TestBoardCliqueAbreELEDLiga(t *testing.T) {
	h, b, c, w := boardHarness(t)
	var aberto string
	b.OnOpen = func(id string) { aberto = id }
	i := slices.Index(c.Order(), "chorus")
	x, y := chipCenter(b, i)
	h.click(w, x, y)
	if aberto != "chorus" {
		t.Errorf("clique deveria abrir o chorus, abriu %q", aberto)
	}
	if c.Enabled("chorus") {
		t.Fatal("chorus começa desligado")
	}
	lc := b.ledCenter()
	h.t += time.Second
	h.click(w, float32(b.slotX(i))+lc.X, float32(b.top)+lc.Y)
	if !c.Enabled("chorus") {
		t.Error("clique no LED deveria ligar o chorus")
	}
}

func novoApp(t *testing.T) (*App, *source.FilePlayer) {
	t.Helper()
	precisa(t, diPath, irPath)
	const fs = 48000
	chain := engine.NewDefaultChain()
	if err := chain.Cab().LoadIRFile(irPath); err != nil {
		t.Fatal(err)
	}
	s := audio.NewSession(chain, fs) // sem abrir a placa
	t.Cleanup(s.Close)
	if err := s.Player.Load(diPath); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s)
	a.UsePresetDir(t.TempDir()) // nunca mexe nos presets de verdade do usuário
	return a, s.Player
}

func TestSeekNaBarra(t *testing.T) {
	a, p := novoApp(t)
	tr := a.transport
	h := newHarness(500, 40)
	dur := p.Clip().Duration()
	w := func(gtx layout.Context) layout.Dimensions { return tr.bar(gtx, a.th, dur) }
	h.frame(w)
	h.click(w, 250, 13)
	// o seek é aplicado pelo áudio; o pedido fica registrado e a posição pública muda no próximo Read
	buf := make([]float64, 16)
	p.Read(buf)
	if got := p.Position(); math.Abs(got-dur/2) > 0.01 {
		t.Errorf("posição %.3f s, esperava %.3f s", got, dur/2)
	}

	// Shift + arrastar escolhe o trecho do loop
	h.t += time.Second
	h.ptr(w, pointer.Press, 100, 13, key.ModShift)
	h.ptr(w, pointer.Drag, 300, 13, key.ModShift)
	h.ptr(w, pointer.Release, 300, 13, key.ModShift)
	ls, le := p.LoopRegion()
	if math.Abs(ls-0.2*dur) > 0.01 || math.Abs(le-0.6*dur) > 0.01 {
		t.Errorf("trecho %.2f–%.2f s, esperava %.2f–%.2f s", ls, le, 0.2*dur, 0.6*dur)
	}
	// duplo clique volta para o arquivo inteiro
	h.t += time.Second
	h.click(w, 50, 13)
	h.click(w, 50, 13)
	if ls, le := p.LoopRegion(); ls != 0 || math.Abs(le-dur) > 1e-6 {
		t.Errorf("duplo clique deveria voltar ao arquivo inteiro: %.2f–%.2f", ls, le)
	}
}

func TestAppSobreposicaoDoPedal(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	a.Open("delay")
	h.frame(a.Layout)
	if a.Opened() != "delay" {
		t.Fatal("o delay deveria estar aberto")
	}
	// clique no centro (corpo do pedal) não fecha
	h.click(a.Layout, 640, 400)
	if a.Opened() != "delay" {
		t.Error("clicar no pedal não deveria fechar")
	}
	// clique fora fecha
	h.click(a.Layout, 10, 10)
	if a.Opened() != "" {
		t.Error("clicar fora deveria fechar")
	}
	// Esc fecha
	a.Open("reverb")
	h.frame(a.Layout)
	h.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame(a.Layout)
	if a.Opened() != "" {
		t.Error("Esc deveria fechar")
	}
}

func TestAppCarregaArquivoEscolhido(t *testing.T) {
	a, p := novoApp(t)
	outro := irPath // qualquer WAV serve
	a.Pick = func(string) (string, error) { return outro, nil }
	invalidou := make(chan struct{}, 1)
	a.Invalidate = func() { invalidou <- struct{}{} }
	a.transport.OnOpen()
	select {
	case <-invalidou:
	case <-time.After(5 * time.Second):
		t.Fatal("o seletor não terminou")
	}
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	if p.Clip().Name != "gabinete_2x12_sm57.wav" {
		t.Errorf("arquivo carregado: %q", p.Clip().Name)
	}
	if a.status != "carregado: gabinete_2x12_sm57.wav" || a.statusErr {
		t.Errorf("status %q (erro=%v)", a.status, a.statusErr)
	}
}

// Desenhar a tela inteira em vários tamanhos não pode dar pânico.
func TestAppDesenhaEmVariosTamanhos(t *testing.T) {
	a, p := novoApp(t)
	p.Play()
	out := make([]float32, 2*512)
	for _, sz := range []image.Point{{1280, 800}, {1024, 700}, {800, 600}, {1920, 1080}} {
		h := newHarness(sz.X, sz.Y)
		for i := 0; i < 3; i++ {
			a.Renderer.Render(out, 2, 512)
			h.frame(a.Layout)
		}
	}
	for _, id := range []string{"gate", "comp", "chorus", "flanger", "phaser", "delay", "reverb"} {
		a.Open(id)
		h := newHarness(1280, 800)
		h.frame(a.Layout)
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

func TestPainelDeEntradaTrocaFonte(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 120)
	w := func(gtx layout.Context) layout.Dimensions { return a.input.Layout(gtx, a.th, &a.meters) }
	y := float32(h.frame(w).Size.Y) / 2
	// os botões Arquivo/Guitarra ficam logo depois do rótulo ENTRADA; acha pelo clique varrendo a linha
	clicou := false
	for x := float32(60); x < 300 && !clicou; x += 6 {
		h.click(w, x, y)
		h.t += time.Second
		clicou = a.Session.Source() == audio.SourceGuitar
	}
	if !clicou {
		t.Fatal("não achei o botão Guitarra")
	}
	// e o Arquivo volta
	for x := float32(60); x < 300 && a.Session.Source() == audio.SourceGuitar; x += 6 {
		h.click(w, x, y)
		h.t += time.Second
	}
	if a.Session.Source() != audio.SourceFile {
		t.Error("não voltou para Arquivo")
	}
}

func TestPainelDeAudioAbreELatencia(t *testing.T) {
	a, _ := novoApp(t)
	a.input.OnAudio()
	if a.Opened() != AudioID {
		t.Fatal("o painel de áudio deveria abrir")
	}
	h := newHarness(1280, 800)
	h.frame(a.Layout) // placa fechada: o painel desenha mesmo assim
	h.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame(a.Layout)
	if a.Opened() != "" {
		t.Error("Esc deveria fechar o painel de áudio")
	}
	if a.audioSet.Measured() != 0 {
		t.Error("sem medição ainda")
	}
}

// O painel de entrada é uma linha: não pode ocupar a altura da janela (bug visto na captura de tela).
func TestPainelDeEntradaAlturaDeUmaLinha(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	d := h.frame(func(gtx layout.Context) layout.Dimensions { return a.input.Layout(gtx, a.th, &a.meters) })
	if d.Size.Y > 120 {
		t.Errorf("painel de entrada com %d px de altura", d.Size.Y)
	}
}

func TestPresetsNavegarModificarSalvarExcluir(t *testing.T) {
	a, _ := novoApp(t)
	pb := a.Presets()
	h := newHarness(1280, 800)
	h.frame(a.Layout)

	pb.Load(0) // Clean (fábrica)
	if c, ok := pb.Current(); !ok || c.Name != "Clean" || !c.Factory {
		t.Fatalf("preset atual: %+v", c)
	}
	if g := a.Chain.Module(engine.PreID).Param("gain").Get(); g != 2 {
		t.Errorf("Clean deveria ter ganho 2, tem %v", g)
	}
	h.now = h.now.Add(time.Second)
	h.frame(a.Layout)
	if pb.Dirty() {
		t.Error("recém-carregado não está modificado")
	}
	a.Chain.Module(engine.PreID).Param("gain").Set(3)
	h.now = h.now.Add(time.Second)
	h.frame(a.Layout)
	if !pb.Dirty() {
		t.Error("mexer num knob deveria marcar como modificado")
	}

	// Salvar como pelo campo de texto: digita e aperta Enter
	a.Open(SaveAsID)
	h.frame(a.Layout) // aplica o foco no campo
	h.r.Queue(key.EditEvent{Text: "Meu Clean"})
	h.frame(a.Layout)
	h.r.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame(a.Layout)
	h.frame(a.Layout)
	c, ok := pb.Current()
	if !ok || c.Name != "Meu Clean" || c.Factory {
		t.Fatalf("depois de salvar, o atual deveria ser \"Meu Clean\": %+v (status %q)", c, a.status)
	}
	if a.Opened() != "" {
		t.Error("salvar deveria fechar a janela")
	}
	if pb.Dirty() {
		t.Error("recém-salvo não está modificado")
	}

	// Excluir: o 1º clique só arma; apagar de fato vem no 2º
	n := len(pb.list)
	pb.confirmDel = h.now.Add(3 * time.Second)
	pb.del.Click()
	h.frame(a.Layout)
	if len(pb.list) != n-1 {
		t.Errorf("deveria ter excluído: %d → %d presets", n, len(pb.list))
	}
}

func TestPresetsListaAbreECarrega(t *testing.T) {
	a, _ := novoApp(t)
	a.Open(PresetListID)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	pb := a.Presets()
	pb.rows[2].Click() // Lead
	h.frame(a.Layout)
	if c, _ := pb.Current(); c.Name != "Lead" {
		t.Errorf("deveria ter carregado o Lead, atual %q", c.Name)
	}
	if a.Opened() != "" {
		t.Error("escolher um preset deveria fechar a lista")
	}
	if !a.Chain.Enabled("delay") {
		t.Error("o Lead liga o delay")
	}
}

type senoFonte struct{ f, n float64 }

func (s *senoFonte) Read(buf []float64) {
	for i := range buf {
		buf[i] = 0.3 * math.Sin(2*math.Pi*s.f*s.n/48000)
		s.n++
	}
}

func TestAfinadorAbreMostraNotaEFecha(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	a.Open(TunerID)
	h.frame(a.Layout)
	if !a.Session.Tuner.Running() || !a.Renderer.Muted() {
		t.Fatal("abrir o afinador liga a detecção e o mudo")
	}
	a.Renderer.SetSource(&senoFonte{f: 196})
	out := make([]float32, 2*256)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && a.tuner.shown.Note == "" {
		a.Renderer.Render(out, 2, 256)
		time.Sleep(2 * time.Millisecond)
		h.frame(a.Layout)
	}
	if a.tuner.shown.Note != "G" || a.tuner.shown.Octave != 3 {
		t.Fatalf("esperava G3 (196 Hz), mostrou %+v", a.tuner.shown)
	}
	h.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame(a.Layout)
	if a.Session.Tuner.Running() || a.Renderer.Muted() {
		t.Error("fechar o afinador desliga a detecção e devolve a saída")
	}
}

func TestTapTempoPelaTeclaT(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	for i := 0; i < 5; i++ {
		h.r.Queue(key.Event{Name: "T", State: key.Press})
		h.frame(a.Layout)
		h.now = h.now.Add(500*time.Millisecond - 16*time.Millisecond) // frame() já anda 16 ms
	}
	tempo := a.Chain.Module("delay").Param("time")
	if math.Abs(tempo.Get()-500) > 1 {
		t.Fatalf("tap a cada 500 ms: delay %.1f ms, esperava 500", tempo.Get())
	}
	tap := a.pedals["delay"].tap
	if math.Abs(tap.BPM()-120) > 0.5 {
		t.Errorf("andamento %.1f BPM", tap.BPM())
	}
	// colcheia pontuada mantém os 120 BPM: 375 ms
	a.Open("delay")
	h.frame(a.Layout)
	tap.subs[1].Click()
	h.frame(a.Layout)
	if math.Abs(tempo.Get()-375) > 1 || math.Abs(tap.BPM()-120) > 0.5 {
		t.Errorf("1/8. a 120 BPM: %.1f ms (%.1f BPM), esperava 375 ms", tempo.Get(), tap.BPM())
	}

	// digitando o nome de um preset, "T" não toca o tap
	antes := tempo.Get()
	a.Open(SaveAsID)
	h.frame(a.Layout)
	for i := 0; i < 3; i++ {
		h.r.Queue(key.Event{Name: "T", State: key.Press})
		h.frame(a.Layout)
		h.now = h.now.Add(300 * time.Millisecond)
	}
	if tempo.Get() != antes {
		t.Error("com o campo de nome aberto, T não deveria mexer no delay")
	}
}

// Os controles de arquivo só aparecem (e a barra de espaço só funciona) com a fonte em Arquivo.
func TestControlesDeArquivoSoComFonteArquivo(t *testing.T) {
	a, p := novoApp(t)
	h := newHarness(1280, 800)
	a.Session.SetSource(audio.SourceGuitar)
	a.transport.barW = 0
	h.frame(a.Layout)
	if a.transport.barW != 0 {
		t.Error("com a fonte em Guitarra, os controles de arquivo não deveriam ser desenhados")
	}
	h.r.Queue(key.Event{Name: key.NameSpace, State: key.Press})
	h.frame(a.Layout)
	if p.Playing() {
		t.Error("com a fonte em Guitarra, espaço não deveria dar play no arquivo")
	}
	a.Session.SetSource(audio.SourceFile)
	h.frame(a.Layout)
	if a.transport.barW == 0 {
		t.Error("com a fonte em Arquivo, os controles aparecem")
	}
	h.r.Queue(key.Event{Name: key.NameSpace, State: key.Press})
	h.frame(a.Layout)
	if !p.Playing() {
		t.Error("com a fonte em Arquivo, espaço dá play")
	}
}

func TestGravarPeloRodape(t *testing.T) {
	a, p := novoApp(t)
	a.RecordDir = t.TempDir()
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	p.Play()
	a.recBtn.Click()
	h.frame(a.Layout)
	if !a.Session.Recorder.Recording() {
		t.Fatal("o botão Gravar deveria começar a gravação")
	}
	out := make([]float32, 2*256)
	for k := 0; k < 100; k++ {
		a.Renderer.Render(out, 2, 256)
		if k%20 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	a.recBtn.Click()
	h.frame(a.Layout)
	if a.Session.Recorder.Recording() {
		t.Fatal("o segundo clique termina a gravação")
	}
	if a.statusErr || !strings.Contains(a.status, "gravado") || !strings.Contains(a.status, "+ DI") {
		t.Errorf("status %q", a.status)
	}
	arquivos, _ := filepath.Glob(filepath.Join(a.RecordDir, "*.wav"))
	if len(arquivos) != 2 {
		t.Errorf("esperava a gravação e a DI, achei %v", arquivos)
	}
}

func TestExportarArquivo(t *testing.T) {
	a, _ := novoApp(t)
	destino := filepath.Join(t.TempDir(), "saida.wav")
	a.PickSave = func(title, sugerido string) (string, error) {
		if sugerido != "guitarra_di-ampsim.wav" {
			t.Errorf("nome sugerido %q", sugerido)
		}
		return destino, nil
	}
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	a.transport.export.Click()
	h.frame(a.Layout)
	if !a.exporting {
		t.Fatal("deveria estar exportando")
	}
	deadline := time.Now().Add(20 * time.Second)
	for a.exporting && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		h.frame(a.Layout)
	}
	if a.statusErr || !strings.Contains(a.status, "exportado") {
		t.Fatalf("status %q", a.status)
	}
	fs, x, err := wav.ReadMonoFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	clip := a.Player.Clip()
	if fs != clip.FS || len(x) != len(clip.Samples)+2*clip.FS {
		t.Errorf("%d Hz, %d amostras; esperava %d Hz e %d", fs, len(x), clip.FS, len(clip.Samples)+2*clip.FS)
	}
}

// Regressão: a DI termina num corte abrupto; exportada sem rampa, virava um estalo de
// −8,6 dBFS no ponto em que ela acaba (relatado em 18/11). Com a rampa, ali só há a cauda.
func TestExportarSemEstaloNoFimDaDI(t *testing.T) {
	a, _ := novoApp(t)
	destino := filepath.Join(t.TempDir(), "saida.wav")
	a.PickSave = func(string, string) (string, error) { return destino, nil }
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	a.transport.export.Click()
	h.frame(a.Layout)
	for deadline := time.Now().Add(20 * time.Second); a.exporting && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		h.frame(a.Layout)
	}
	fs, x, err := wav.ReadMonoFile(destino)
	if err != nil {
		t.Fatal(err)
	}
	fim := len(a.Player.Clip().Samples)
	antes, depois := 0.0, 0.0
	for i := fim - fs/20; i < fim-fs/50; i++ { // 30 ms antes do fim (sem a rampa)
		antes = math.Max(antes, math.Abs(x[i]))
	}
	for i := fim - fs/100; i < fim+fs/20; i++ { // em volta do fim
		depois = math.Max(depois, math.Abs(x[i]))
	}
	db := func(v float64) float64 { return 20 * math.Log10(v+1e-12) }
	if db(depois) > db(antes)+6 {
		t.Errorf("estalo no fim da DI: pico %.1f dBFS (antes do fim: %.1f dBFS)", db(depois), db(antes))
	}
}
