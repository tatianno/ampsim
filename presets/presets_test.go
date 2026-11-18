package presets

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/wav"
)

// Os de fábrica: só o amp e os pedais usados (ligados), o modelo NAM e a IR sempre das
// incluídas (nunca um arquivo do disco), e aplicar e ler de volta dá o mesmo estado.
func TestFabricaCompletaComCapturasIncluidas(t *testing.T) {
	for _, p := range Factory() {
		// só o amp e os pedais usados, todos ligados
		for _, m := range p.State.Modules {
			if !m.Enabled {
				t.Errorf("%s: %s está na cadeia desligado", p.Name, m.ID)
			}
		}
		for _, m := range p.State.Modules {
			for k, v := range m.Extra {
				if !strings.HasPrefix(v, capturas.Prefix) {
					t.Errorf("%s: %s.%s = %q não é uma captura/IR incluída", p.Name, m.ID, k, v)
				}
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
	if p, ok := Find(Factory(), "solo hard rock"); !ok || p.State.Modules[0].ID != "gate" {
		t.Error("deveria achar o Solo Hard Rock sem diferenciar maiúsculas")
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

// Todos os presets de fábrica perto de −18 dBFS RMS com a DI de teste (trocar de preset não
// dá salto de volume), sem passar de −1 dBFS de pico.
func TestFabricaNoMesmoVolume(t *testing.T) {
	fs, x, err := wav.ReadMonoFile(filepath.Join("..", "testdata", "guitarra_di.wav"))
	if err != nil {
		t.Skip("sem a DI de teste:", err)
	}
	x = dsp.ResampleRate(x, fs, 48000) // a taxa das capturas NAM
	for _, p := range Factory() {
		y, err := engine.RenderOffline(p.State, x, 48000, 0, 256, nil)
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		s, pk := 0.0, 0.0
		for _, v := range y[4800:] {
			s += v * v
			pk = math.Max(pk, math.Abs(v))
		}
		rms := 10 * math.Log10(s/float64(len(y)-4800))
		t.Logf("%-16s rms %6.1f dBFS  pico %5.1f dBFS", p.Name, rms, 20*math.Log10(pk))
		// −18 dBFS RMS; os limpos e dinâmicos podem ficar até ~3 dB abaixo, limitados pelo pico
		if rms > -16.5 || rms < -21.5 {
			t.Errorf("%s: %.1f dBFS RMS, esperava −18 (−21,5 a −16,5)", p.Name, rms)
		}
		if db := 20 * math.Log10(pk); db > -0.9 {
			t.Errorf("%s: pico %.1f dBFS (o limite é −1)", p.Name, db)
		}
	}
}
