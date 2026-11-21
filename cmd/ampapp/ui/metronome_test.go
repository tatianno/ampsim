package ui

import (
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
)

// Tecla M liga e desliga o metrônomo sem abrir o painel; o botão da barra abre o painel.
func TestMetronomoTeclaEBotao(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	m := a.Session.Metro
	h.r.Queue(key.Event{Name: "M", State: key.Press})
	h.frame(a.Layout)
	if !m.Running() || a.Opened() != "" {
		t.Fatalf("M deveria ligar o metrônomo sem abrir nada (ligado %v, aberto %q)", m.Running(), a.Opened())
	}
	h.r.Queue(key.Event{Name: "M", State: key.Press})
	h.frame(a.Layout)
	if m.Running() {
		t.Fatal("M de novo deveria desligar")
	}
	a.metroBtn.Click()
	h.frame(a.Layout)
	if a.Opened() != MetroID {
		t.Fatalf("aberto %q, esperava o metrônomo", a.Opened())
	}
	a.metro.startBtn.Click()
	h.frame(a.Layout)
	if !m.Running() {
		t.Fatal("Ligar deveria ligar o metrônomo")
	}
	h.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frame(a.Layout)
	if a.Opened() != "" || !m.Running() {
		t.Errorf("fechar o painel não desliga o metrônomo (aberto %q, ligado %v)", a.Opened(), m.Running())
	}
}

// Com o painel aberto, a tecla T acerta o andamento do metrônomo (e não o dos pedais).
func TestMetronomoTap(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	a.Open(MetroID)
	h.frame(a.Layout)
	t0 := time.Now()
	for i := range 4 {
		a.Tap(t0.Add(time.Duration(i) * 600 * time.Millisecond)) // 100 BPM
	}
	if bpm := a.Session.Metro.BPM.Get(); bpm != 100 {
		t.Errorf("andamento %.1f, esperava 100", bpm)
	}
}

// Andamento, compasso e volume ficam em ajustes.json ao fechar o painel e voltam num app novo.
func TestMetronomoSalvaAjustes(t *testing.T) {
	a, _ := novoApp(t)
	dir := t.TempDir()
	a.UsePresetDir(dir)
	h := newHarness(1280, 800)
	a.Open(MetroID)
	h.frame(a.Layout)
	m := a.Session.Metro
	m.BPM.Set(87)
	m.Beats.Set(3)
	m.Volume.Set(-20)
	a.open = ""
	h.frame(a.Layout)

	b, _ := novoApp(t)
	b.UsePresetDir(dir)
	n := b.Session.Metro
	if n.BPM.Get() != 87 || n.Beats.Get() != 3 || n.Volume.Get() != -20 {
		t.Errorf("voltou com %.0f BPM, compasso %.0f, %.0f dB", n.BPM.Get(), n.Beats.Get(), n.Volume.Get())
	}
	if n.Running() {
		t.Error("o metrônomo sempre abre desligado")
	}
}

// O painel desenha com o metrônomo ligado, em todos os compassos, sem estourar a largura.
func TestMetronomoPainelDesenha(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	a.Open(MetroID)
	a.Session.Metro.Start()
	for b := 1.0; b <= 7; b++ {
		a.Session.Metro.Beats.Set(b)
		h.frame(a.Layout)
	}
	d := h.frame(func(gtx layout.Context) layout.Dimensions { return a.metro.Layout(gtx, a.th) })
	if d.Size.X != MetroWidthDp {
		t.Errorf("painel com %d px, esperava %d", d.Size.X, MetroWidthDp)
	}
}
