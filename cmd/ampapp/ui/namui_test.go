package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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
