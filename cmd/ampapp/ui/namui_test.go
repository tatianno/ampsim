package ui

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
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
	a.menu.btn.Click()
	h.frame(a.Layout)
	if !a.menu.open {
		t.Fatal("o menu deveria abrir")
	}
	h.frame(a.Layout) // desenha a lista
	a.menu.items[MenuAbout].Click()
	h.frame(a.Layout)
	if a.menu.open {
		t.Error("o menu deveria fechar depois de escolher")
	}
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

func TestPedalSynth(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	a.Open("synth")
	h.frame(a.Layout)
	v := a.pedals["synth"].synth
	if v == nil || v.area.X == 0 {
		t.Fatal("o editor da sequência deveria ter sido desenhado")
	}
	// clicar no alto da barra 3 e arrastar até o pé da barra 6
	v.setFrom(f32.Pt(float32(v.area.X)*2.5/8, 0))
	v.setFrom(f32.Pt(float32(v.area.X)*5.5/8, float32(v.area.Y)))
	steps := a.Chain.Module("synth").(*engine.Synth).Steps()
	if steps[2].Get() != 100 || steps[5].Get() != 0 {
		t.Errorf("passos 3 e 6 = %.0f%% e %.0f%%, esperava 100%% e 0%%", steps[2].Get(), steps[5].Get())
	}
	if a.pedals["synth"].tap == nil {
		t.Error("o synth tem tap tempo")
	}
}

func TestEQArrastarPontos(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	a.Open("eq")
	h.frame(a.Layout)
	v := a.pedals["eq"].eq
	if v.plotSize.X == 0 {
		t.Fatal("a curva deveria ter sido desenhada")
	}
	eq := a.Chain.Module("eq")
	w, hh := float32(v.plotSize.X), float32(v.plotSize.Y)

	// clicar no ponto do Médio 2 (800 Hz, 0 dB) e arrastar para 1,6 kHz, +6 dB
	p := v.handle(2)
	if v.handleAt(p) != 2 {
		t.Fatalf("o ponto do Médio 2 não foi encontrado em %v", p)
	}
	t0 := time.Unix(100, 0)
	v.press(p, t0)
	v.dragTo(v.drag, f32.Pt(eqX(1600, w), eqY(6, hh)))
	if f, g := eq.Param("f3").Get(), eq.Param("g3").Get(); math.Abs(f-1600) > 20 || math.Abs(g-6) > 0.15 {
		t.Errorf("Médio 2 em %.0f Hz / %+.1f dB, esperava 1600 Hz / +6", f, g)
	}
	h.frame(a.Layout) // o knob lê o mesmo parâmetro: já mostra o valor novo
	if k := v.freq[2]; k.P.Get() != eq.Param("f3").Get() {
		t.Error("o knob deveria acompanhar")
	}
	// a faixa da banda limita: o Graves não passa de 500 Hz
	v.dragTo(0, f32.Pt(eqX(5000, w), eqY(0, hh)))
	if f := eq.Param("f1").Get(); f != 500 {
		t.Errorf("Graves arrastado para 5 kHz: %.0f Hz, esperava o limite (500)", f)
	}
	// roda: Q do Médio 2 sobe (para cima = mais estreito); nos shelves não faz nada
	q0 := eq.Param("q3").Get()
	v.scrollQ(2, -100)
	if eq.Param("q3").Get() <= q0 {
		t.Error("rolar para cima deveria estreitar (subir o Q)")
	}
	v.scrollQ(0, -100) // Graves: shelf, sem Q
	// duplo clique zera o ganho
	p = v.handle(2)
	v.press(p, t0.Add(time.Second))
	v.press(p, t0.Add(time.Second+200*time.Millisecond))
	if g := eq.Param("g3").Get(); g != 0 {
		t.Errorf("duplo clique: ganho %+.1f dB, esperava 0", g)
	}
	// clique longe dos pontos não pega nada
	if v.handleAt(f32.Pt(-100, -100)) != -1 {
		t.Error("clique fora dos pontos não deveria pegar banda")
	}
}

func TestPedaleira(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	pb, b := a.Presets(), a.Pedalboard()
	// footswitch 2 do banco 1 = 2º preset (Country); o banco 1 é o dos limpos
	if b.BankName(b.Bank()) != "Limpos" {
		t.Errorf("nome do banco 1: %q", b.BankName(b.Bank()))
	}
	b.sw[1].Click()
	h.frame(a.Layout)
	if c, _ := pb.Current(); c.Name != "Country" {
		t.Errorf("footswitch 2: %q, esperava Country", c.Name)
	}
	// tecla 4 = 4º preset do banco
	h.r.Queue(key.Event{Name: "4", State: key.Press})
	h.frame(a.Layout)
	if c, _ := pb.Current(); c.Name != pb.list[3].Name {
		t.Errorf("tecla 4: %q, esperava %q", c.Name, pb.list[3].Name)
	}
	// próximo banco: footswitch 1 = 7º preset; e o banco acompanha a troca por outro caminho
	b.next.Click()
	h.frame(a.Layout)
	b.sw[0].Click()
	h.frame(a.Layout)
	if c, _ := pb.Current(); c.Name != pb.list[BoardSlots].Name {
		t.Errorf("banco 2, footswitch 1: %q, esperava %q", c.Name, pb.list[BoardSlots].Name)
	}
	if b.BankName(b.Bank()) != "Drive e rock" {
		t.Errorf("nome do banco 2: %q", b.BankName(b.Bank()))
	}
	pb.Load(0)
	h.frame(a.Layout)
	if b.Bank() != 0 {
		t.Errorf("carregar o 1º preset por fora deveria voltar ao banco 1 (está no %d)", b.Bank()+1)
	}
	b.SetBank(-1) // dá a volta
	if b.Bank() != b.banks()-1 {
		t.Errorf("banco antes do 1º = o último; está no %d", b.Bank()+1)
	}
}

// Configurar os footswitches pela matriz: escolher o preset de uma célula, salvar, reabrir,
// preset apagado e restaurar o padrão. Trocar um preset mantém os nomes dos bancos.
func TestPedaleiraConfiguravel(t *testing.T) {
	a, _ := novoApp(t)
	cfg := filepath.Join(t.TempDir(), "pedaleira.json")
	a.pedalboard = NewPedalboard(a.Presets(), cfg, a.setStatus, func(id string) { a.open = id })
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	pb, b := a.Presets(), a.pedalboard
	if b.Custom() {
		t.Fatal("começa na ordem padrão")
	}
	// Editar abre a matriz; clicar na célula 1 do banco 1 abre a escolha
	b.edit.Click()
	h.frame(a.Layout)
	if a.Opened() != PedalboardEditID {
		t.Fatalf("aberto %q, esperava a edição da pedaleira", a.Opened())
	}
	h.frame(a.Layout)
	b.cells[0].Click()
	h.frame(a.Layout)
	if b.pick != 0 {
		t.Fatalf("a célula 1 deveria abrir a escolha (pick %d)", b.pick)
	}
	h.frame(a.Layout)
	metal := slices.IndexFunc(pb.list, func(p presets.Preset) bool { return p.Name == "Metal" })
	b.rows[metal].Click()
	h.frame(a.Layout)
	if b.pick != -1 || a.Opened() != PedalboardEditID {
		t.Fatalf("escolher deveria voltar à matriz (pick %d, aberto %q)", b.pick, a.Opened())
	}
	if b.slotName(0) != "Metal" || !b.Custom() {
		t.Fatalf("footswitch 1 = %q", b.slotName(0))
	}
	if b.BankName(0) != "Limpos" || b.BankName(2) != "Efeitos e texturas" {
		t.Errorf("trocar um preset não pode apagar os nomes: %q, %q", b.BankName(0), b.BankName(2))
	}
	a.open = ""
	b.Press(0)
	if c, _ := pb.Current(); c.Name != "Metal" {
		t.Errorf("footswitch 1 carregou %q, esperava Metal", c.Name)
	}
	if b.slotName(1) != "Country" {
		t.Errorf("footswitch 2 = %q, esperava o padrão (Country)", b.slotName(1))
	}
	// salvo em disco: uma pedaleira nova lê a mesma configuração, com os nomes
	if c := NewPedalboard(pb, cfg, nil, nil); c.slotName(0) != "Metal" || c.BankName(1) != "Drive e rock" {
		t.Errorf("reaberto: footswitch 1 = %q, banco 2 = %q", c.slotName(0), c.BankName(1))
	}
	// preset que não existe mais: aparece apagado e não faz nada
	if err := b.Assign(2, "Não Existe"); err != nil {
		t.Fatal(err)
	}
	antes, _ := pb.Current()
	b.Press(2)
	if c, _ := pb.Current(); c.Name != antes.Name || b.slotPreset(2) != -1 {
		t.Error("footswitch com preset apagado não deveria trocar de preset")
	}
	// restaurar o padrão (na matriz): 1º clique arma, 2º confirma; o arquivo some
	b.OpenEditor()
	h.frame(a.Layout)
	b.reset.Click()
	h.frame(a.Layout)
	if !b.Custom() {
		t.Fatal("o 1º clique só arma a restauração")
	}
	b.reset.Click()
	h.frame(a.Layout)
	if b.Custom() || b.slotName(0) != "Clean" || b.BankName(0) != "Limpos" {
		t.Errorf("restaurado: footswitch 1 = %q, banco 1 = %q", b.slotName(0), b.BankName(0))
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Error("restaurar deveria apagar pedaleira.json")
	}
}

// Nomes dos bancos: renomear pela matriz (com os atalhos desligados enquanto digita), banco
// sem nome mostra "BANCO n/N", e bancos novos/removidos.
func TestPedaleiraNomesEBancos(t *testing.T) {
	a, _ := novoApp(t)
	cfg := filepath.Join(t.TempDir(), "pedaleira.json")
	a.pedalboard = NewPedalboard(a.Presets(), cfg, a.setStatus, func(id string) { a.open = id })
	b := a.pedalboard
	h := newHarness(1280, 800)
	h.frame(a.Layout)
	if got := b.bankLabel(); got != "LIMPOS" {
		t.Errorf("rótulo do banco 1: %q", got)
	}
	b.OpenEditor()
	h.frame(a.Layout)
	// digitar no nome do banco 1: "1" vira texto, não aciona o footswitch
	h.r.Source().Execute(key.FocusCmd{Tag: &b.nameEds[0]})
	h.frame(a.Layout)
	antes, _ := a.Presets().Current()
	h.r.Queue(key.Event{Name: "1", State: key.Press})
	h.frame(a.Layout)
	if c, _ := a.Presets().Current(); c.Name != antes.Name {
		t.Error("digitando o nome do banco, a tecla 1 não deveria trocar de preset")
	}
	b.nameEds[0].SetText("Meus limpos")
	h.frame(a.Layout) // SetText não gera ChangeEvent: grava direto
	if err := b.SetBankName(0, b.nameEds[0].Text()); err != nil {
		t.Fatal(err)
	}
	if b.BankName(0) != "Meus limpos" || !b.Custom() {
		t.Fatalf("nome do banco 1 = %q", b.BankName(0))
	}
	// sem nome: volta a mostrar a posição
	if err := b.SetBankName(1, "  "); err != nil {
		t.Fatal(err)
	}
	b.SetBank(1)
	if got := b.bankLabel(); got != "BANCO 2/3" {
		t.Errorf("banco sem nome: %q", got)
	}
	// nome comprido é cortado
	if err := b.SetBankName(2, "Um nome comprido demais para caber"); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(b.BankName(2))); n != boardNameMax {
		t.Errorf("nome com %d letras, máximo %d", n, boardNameMax)
	}
	// + Banco e − Último banco
	if b.CanRemoveBank() {
		t.Error("com 3 bancos não dá para remover")
	}
	b.addBank.Click()
	h.frame(a.Layout)
	if b.banks() != 4 || !b.CanRemoveBank() {
		t.Fatalf("depois de + Banco: %d bancos", b.banks())
	}
	if err := b.Assign(3*BoardSlots, "Clean"); err != nil {
		t.Fatal(err)
	}
	if b.CanRemoveBank() {
		t.Error("banco com preset não pode ser removido")
	}
	if err := b.Assign(3*BoardSlots, ""); err != nil {
		t.Fatal(err)
	}
	b.delBank.Click()
	h.frame(a.Layout)
	if b.banks() != 3 {
		t.Errorf("depois de − Último banco: %d bancos", b.banks())
	}
	// configuração antiga (sem nomes): bancos sem nome
	old := filepath.Join(t.TempDir(), "antiga.json")
	if err := os.WriteFile(old, []byte(`{"slots":["Metal","Clean"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := NewPedalboard(a.Presets(), old, nil, nil); c.BankName(0) != "" || c.bankLabel() != "BANCO 1/3" {
		t.Errorf("configuração antiga: nome %q, rótulo %q", c.BankName(0), c.bankLabel())
	}
}

func TestPedaleiraEdicaoVisual(t *testing.T) {
	a, _ := novoApp(t)
	h := newHarness(1280, 800)
	a.pedalboard.OpenEditor()
	h.frame(a.Layout)
	a.pedalboard.pick = 7 // escolha aberta: desenha sem quebrar
	h.frame(a.Layout)
	a.pedalboard.back.Click()
	h.frame(a.Layout)
	if a.pedalboard.pick != -1 || a.Opened() != PedalboardEditID {
		t.Error("Voltar deveria retornar à matriz")
	}
}
