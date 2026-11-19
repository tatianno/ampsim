package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"

	"github.com/tatianno/ampsim/presets"
)

// escolhe abre o menu e clica no item.
func escolhe(h *harness, a *App, item int) {
	a.menu.btn.Click()
	h.frame(a.Layout)
	h.frame(a.Layout)
	a.menu.items[item].Click()
	h.frame(a.Layout)
}

func TestMenuAbreAudioEAfinador(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	escolhe(h, a, MenuAudio)
	if a.Opened() != AudioID {
		t.Fatalf("aberto %q, esperava o painel de áudio", a.Opened())
	}
	escolhe(h, a, MenuTuner)
	if a.Opened() != TunerID {
		t.Fatalf("aberto %q, esperava o afinador", a.Opened())
	}
	// tecla A fecha e abre o afinador
	h.r.Queue(key.Event{Name: "A", State: key.Press})
	h.frame(a.Layout)
	if a.Opened() != "" {
		t.Fatalf("a tecla A deveria fechar o afinador (aberto %q)", a.Opened())
	}
	h.r.Queue(key.Event{Name: "A", State: key.Press})
	h.frame(a.Layout)
	if a.Opened() != TunerID {
		t.Fatalf("a tecla A deveria abrir o afinador (aberto %q)", a.Opened())
	}
}

func TestMenuFechaComEscECliqueFora(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	a.menu.btn.Click()
	h.frame(a.Layout)
	h.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame(a.Layout)
	if a.menu.open {
		t.Error("Esc deveria fechar o menu")
	}
	a.menu.btn.Click()
	h.frame(a.Layout)
	h.frame(a.Layout)
	h.click(a.Layout, 300, 600)
	if a.menu.open || a.Opened() != "" {
		t.Errorf("clique fora deveria só fechar o menu (menu %v, aberto %q)", a.menu.open, a.Opened())
	}
}

// esperaStatus roda quadros até o status conter s (as ações do menu rodam numa goroutine).
func esperaStatus(t *testing.T, h *harness, a *App, s string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		h.frame(a.Layout)
		if strings.Contains(a.status, s) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status %q, esperava algo com %q", a.status, s)
}

func TestBackupERestauracaoPeloMenu(t *testing.T) {
	a, _ := novoApp(t)
	base := t.TempDir()
	dir := filepath.Join(base, "presets")
	a.UsePresetDir(dir)
	if _, err := presets.Save(dir, "Meu Som", a.Chain.State()); err != nil {
		t.Fatal(err)
	}
	a.presetBar.Reload()
	zipPath := filepath.Join(t.TempDir(), "meu-backup.zip")
	a.PickSaveZip = func(_, suggested string) (string, error) {
		if !strings.HasSuffix(suggested, ".zip") {
			t.Errorf("nome sugerido %q", suggested)
		}
		return zipPath, nil
	}
	a.PickZip = func(string) (string, error) { return zipPath, nil }
	h := newHarness(1280, 800)
	h.frame(a.Layout)

	escolhe(h, a, MenuBackup)
	esperaStatus(t, h, a, "backup salvo: 1 preset")
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, presets.FileName("Meu Som"))); err != nil {
		t.Fatal(err)
	}
	a.presetBar.Reload()
	if _, ok := presets.Find(a.presetBar.list, "Meu Som"); ok {
		t.Fatal("o preset deveria ter sumido")
	}
	escolhe(h, a, MenuRestore)
	esperaStatus(t, h, a, "restaurado: 1 preset")
	if _, ok := presets.Find(a.presetBar.list, "Meu Som"); !ok {
		t.Error("a lista deveria ter o preset restaurado")
	}
}

func TestAnalisadorNaTela(t *testing.T) {
	a, p := novoApp(t)
	h := newHarness(1280, 800)
	p.Play()
	buf := make([]float32, 512*2)
	for i := 0; i < 40; i++ {
		a.Renderer.Render(buf, 2, 512)
		h.frame(a.Layout)
	}
	if !a.Session.Analyzer.Running() || !a.spectrum.ready {
		t.Fatalf("o analisador deveria estar ligado e com espectro (ligado %v, pronto %v)",
			a.Session.Analyzer.Running(), a.spectrum.ready)
	}
	a.spectrum.mini.Click()
	h.frame(a.Layout)
	if a.Opened() != AnalyzerID {
		t.Fatalf("clicar no analisador compacto deveria abrir o grande (aberto %q)", a.Opened())
	}
	h.frame(a.Layout)
	a.spectrum.onBtn.Click()
	h.frame(a.Layout)
	if a.spectrum.Enabled() || a.Session.Analyzer.Running() {
		t.Error("desligar o analisador deveria parar a cópia do áudio")
	}
}
