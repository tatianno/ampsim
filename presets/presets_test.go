package presets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tatianno/ampsim/engine"
)

func TestFabricaCompletaESemIR(t *testing.T) {
	n := len(engine.NewDefaultChain().Modules())
	for _, p := range Factory() {
		if len(p.State.Modules) != n {
			t.Errorf("%s: %d módulos, esperava %d", p.Name, len(p.State.Modules), n)
		}
		for _, m := range p.State.Modules {
			if m.Extra != nil {
				t.Errorf("%s: módulo %s com estado extra (IR) %v", p.Name, m.ID, m.Extra)
			}
		}
		c := engine.NewDefaultChain()
		if err := c.ApplyState(p.State); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
		if !Equal(c.State(), p.State) {
			t.Errorf("%s: aplicar e ler de volta deveria dar o mesmo estado", p.Name)
		}
	}
	if p, ok := Find(Factory(), "lead"); !ok || p.State.Modules[0].ID != "gate" {
		t.Error("deveria achar o Lead sem diferenciar maiúsculas")
	}
}

func TestSalvarListarApagar(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "presets") // ainda não existe
	if l, err := List(dir); err != nil || len(l) != len(Factory()) {
		t.Fatalf("pasta inexistente: %d presets, erro %v", len(l), err)
	}
	c := engine.NewDefaultChain()
	c.Module(engine.PreID).Param("gain").Set(7.7)
	c.SetEnabled("delay", true)
	p, err := Save(dir, "Meu Lead!", c.State())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p.Path) != "meu-lead.json" {
		t.Errorf("arquivo %q", p.Path)
	}
	if _, err := Save(dir, "clean", c.State()); err != ErrFactoryName {
		t.Errorf("nome de fábrica deveria dar ErrFactoryName, deu %v", err)
	}
	os.WriteFile(filepath.Join(dir, "quebrado.json"), []byte("{"), 0o644)
	l, err := List(dir)
	if err == nil {
		t.Error("o arquivo quebrado deveria gerar erro (sem impedir o resto)")
	}
	q, ok := Find(l, "meu lead!")
	if !ok || q.Factory || q.State.Name != "Meu Lead!" {
		t.Fatalf("preset salvo não encontrado: %+v", q)
	}
	d := engine.NewDefaultChain()
	if err := d.ApplyState(q.State); err != nil {
		t.Fatal(err)
	}
	if d.Module(engine.PreID).Param("gain").Get() != 7.7 || !d.Enabled("delay") {
		t.Error("o preset não restaurou o som")
	}
	if err := Delete(q); err != nil {
		t.Fatal(err)
	}
	if err := Delete(Factory()[0]); err == nil {
		t.Error("preset de fábrica não pode ser apagado")
	}
}

func TestFileNameEEqual(t *testing.T) {
	for in, want := range map[string]string{"Meu Lead!": "meu-lead.json", "  Ação  Rítmica ": "ação-rítmica.json", "???": "preset.json"} {
		if got := FileName(in); got != want {
			t.Errorf("FileName(%q) = %q, esperava %q", in, got, want)
		}
	}
	a := engine.NewDefaultChain().State()
	b := engine.NewDefaultChain()
	if !Equal(a, b.State()) {
		t.Error("duas cadeias padrão deveriam ser iguais")
	}
	b.Module("delay").Param("mix").Set(36)
	if Equal(a, b.State()) {
		t.Error("parâmetro diferente deveria contar como modificado")
	}
}

func TestResolve(t *testing.T) {
	if st, err := Resolve("crunch"); err != nil || st.Name != "Crunch" {
		t.Errorf("por nome: %v %v", st.Name, err)
	}
	dir := t.TempDir()
	p, _ := Save(dir, "X", engine.NewDefaultChain().State())
	if st, err := Resolve(p.Path); err != nil || st.Name != "X" {
		t.Errorf("por arquivo: %v %v", st.Name, err)
	}
	if _, err := Resolve("não existe"); err == nil {
		t.Error("deveria dar erro")
	}
}
