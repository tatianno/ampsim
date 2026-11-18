package ui

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gioui.org/io/key"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/engine"
)

// espera roda quadros até cond valer (os modelos carregam numa goroutine).
func espera(t *testing.T, h *harness, a *App, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		h.frame(a.Layout)
		if cond() {
			return
		}
	}
	t.Fatalf("tempo esgotado esperando %s", what)
}

func TestCabecoteModoNAM(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	pre := a.Chain.Pre()
	if pre.Mode() != engine.AmpSimulated {
		t.Fatal("o padrão é o amp simulado")
	}
	a.head.ampSel.btns[engine.AmpNAM].Click()
	h.frame(a.Layout)
	if pre.Mode() != engine.AmpNAM {
		t.Fatal("o seletor não ligou o modo NAM")
	}

	// lista de modelos: escolher uma captura incluída
	a.head.pickNAM.Click()
	h.frame(a.Layout)
	if a.Opened() != NAMListID {
		t.Fatalf("aberto %q, esperava a lista de modelos", a.Opened())
	}
	h.frame(a.Layout)
	names := capturas.Names()
	a.namList.rows[0].Click()
	espera(t, h, a, "o modelo da lista", func() bool { return pre.NAMRef() == capturas.Prefix+names[0] })
	if a.Opened() != "" {
		t.Error("a lista deveria fechar depois de escolher")
	}

	// tamanho
	a.head.sizeSel.btns[engine.NAMFull].Click()
	h.frame(a.Layout)
	if pre.Param("nam_size").Get() != engine.NAMFull {
		t.Error("o seletor de tamanho não mudou")
	}

	// abrir um arquivo .nam
	b, err := capturas.Data(names[1])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "minha captura.nam")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	a.PickNAM = func(string) (string, error) { return path, nil }
	a.namList.OnOpenFile()
	espera(t, h, a, "o arquivo .nam", func() bool { return pre.NAMRef() == path })
	if got := namLabel(pre); got != "minha captura" && got != names[1] {
		t.Errorf("rótulo %q", got)
	}

	// arquivo inválido: erro no rodapé, o modelo continua
	bad := filepath.Join(t.TempDir(), "ruim.nam")
	_ = os.WriteFile(bad, []byte("{}"), 0o644)
	a.PickNAM = func(string) (string, error) { return bad, nil }
	a.namList.OnOpenFile()
	espera(t, h, a, "o erro", func() bool { return a.statusErr })
	if pre.NAMRef() != path {
		t.Error("um arquivo inválido não pode trocar o modelo")
	}

	// de volta ao simulado
	a.head.ampSel.btns[engine.AmpSimulated].Click()
	h.frame(a.Layout)
	if pre.Mode() != engine.AmpSimulated {
		t.Error("não voltou ao simulado")
	}
}

func TestListaDeIRs(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	a.cab.pick.Click()
	h.frame(a.Layout)
	if a.Opened() != IRListID {
		t.Fatalf("aberto %q, esperava a lista de IRs", a.Opened())
	}
	h.frame(a.Layout)
	names := capturas.IRNames()
	i := slices.Index(names, "4x12 SM7")
	if i < 0 {
		t.Fatalf("IRs incluídas: %v", names)
	}
	a.irList.rows[i].Click()
	espera(t, h, a, "a IR da lista", func() bool { return a.Chain.Cab().IRPath() == capturas.Prefix+"4x12 SM7" })
	if got := a.Chain.Cab().IRName(); got != "4x12 SM7" {
		t.Errorf("nome %q", got)
	}
	// o preset guarda a IR incluída
	st := a.Chain.State()
	c := engine.NewDefaultChain()
	if err := c.ApplyState(st); err != nil || c.Cab().IRPath() != capturas.Prefix+"4x12 SM7" {
		t.Errorf("preset: %v, IR %q", err, c.Cab().IRPath())
	}
}

func TestSobre(t *testing.T) {
	a, _ := novoApp(t)
	a.SetVersion("9.9.9")
	var opened []string
	a.about.OpenURL = func(u string) error { opened = append(opened, u); return nil }
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	a.aboutBtn.Click()
	h.frame(a.Layout)
	if a.Opened() != AboutID {
		t.Fatalf("aberto %q, esperava o Sobre", a.Opened())
	}
	h.frame(a.Layout)
	a.about.repo.Click()
	a.about.email.Click()
	h.frame(a.Layout)
	if !slices.Equal(opened, []string{RepoURL, "mailto:" + Contact + "?subject=ampsim"}) {
		t.Errorf("links abertos: %v", opened)
	}
}

func TestTapTempoDoTremolo(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	tap := func(n int, every time.Duration) {
		for i := 0; i < n; i++ {
			h.r.Queue(key.Event{Name: "T", State: key.Press})
			h.frame(a.Layout)
			h.now = h.now.Add(every - 16*time.Millisecond) // frame() já anda 16 ms
		}
	}
	rate := a.Chain.Module("tremolo").Param("rate")
	delay := a.Chain.Module("delay").Param("time")

	// pedal do tremolo aberto: T só mexe nele (120 BPM em colcheia, a subdivisão inicial = 4 Hz)
	a.Open("tremolo")
	h.frame(a.Layout)
	antes := delay.Get()
	tap(5, 500*time.Millisecond)
	if math.Abs(rate.Get()-4) > 0.01 {
		t.Fatalf("tremolo a 120 BPM: %.3f Hz, esperava 4", rate.Get())
	}
	if delay.Get() != antes {
		t.Error("com o tremolo aberto, T não deveria mexer no delay")
	}
	// semínima mantém os 120 BPM: 2 Hz
	tr := a.pedals["tremolo"].tap
	tr.subs[0].Click()
	h.frame(a.Layout)
	if math.Abs(rate.Get()-2) > 0.01 || math.Abs(tr.BPM()-120) > 0.5 {
		t.Errorf("1/4 a 120 BPM: %.3f Hz (%.1f BPM), esperava 2 Hz", rate.Get(), tr.BPM())
	}

	// nada aberto: T marca o andamento dos dois (100 BPM)
	a.open = ""
	h.now = h.now.Add(3 * time.Second) // começa uma contagem nova
	tap(5, 600*time.Millisecond)
	if math.Abs(delay.Get()-600) > 1 || math.Abs(tr.BPM()-100) > 0.5 {
		t.Errorf("andamento global de 100 BPM: delay %.1f ms, tremolo %.1f BPM", delay.Get(), tr.BPM())
	}
}

func TestAdicionarERemoverPedal(t *testing.T) {
	a, _ := novoApp(t)
	if err := a.Chain.SetOrder(engine.StartOrder); err != nil {
		t.Fatal(err)
	}
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	a.board.add.Click()
	h.frame(a.Layout)
	if a.Opened() != AddPedalID {
		t.Fatalf("aberto %q, esperava a lista de adicionar", a.Opened())
	}
	h.frame(a.Layout)
	a.addPedal.btns["delay"].Click()
	h.frame(a.Layout)
	if !a.Chain.InChain("delay") || !a.Chain.Enabled("delay") {
		t.Fatal("o delay deveria entrar na cadeia, ligado")
	}
	if a.Opened() != "delay" {
		t.Errorf("depois de adicionar, o pedal abre; aberto %q", a.Opened())
	}
	h.frame(a.Layout)
	a.pedals["delay"].rm.Click()
	h.frame(a.Layout)
	if a.Chain.InChain("delay") || a.Opened() != "" {
		t.Errorf("remover: na cadeia %v, aberto %q", a.Chain.InChain("delay"), a.Opened())
	}
	// com todos na cadeia, a lista avisa (e não quebra)
	a.Chain.SetOrder(engine.DefaultOrder)
	a.Open(AddPedalID)
	h.frame(a.Layout)
}
