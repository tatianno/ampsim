package ttl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/plugin"
	"github.com/tatianno/ampsim/presets"
)

// fakeHome aponta o HOME e o .so do sistema para pastas temporárias.
func fakeHome(t *testing.T) (bundle, syslib string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := SystemLibrary
	SystemLibrary = filepath.Join(t.TempDir(), Binary)
	t.Cleanup(func() { SystemLibrary = old })
	b, err := UserBundle()
	if err != nil {
		t.Fatal(err)
	}
	return b, SystemLibrary
}

func userList(t *testing.T, names ...string) []plugin.Preset {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if _, err := presets.Save(dir, n, engine.NewDefaultChain().State()); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := plugin.Presets(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSyncSemPluginNaoFazNada(t *testing.T) {
	bundle, _ := fakeHome(t)
	if err := Sync(userList(t, "Meu Som")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bundle); !os.IsNotExist(err) {
		t.Errorf("criou o bundle sem o plugin instalado: %v", err)
	}
}

func TestSyncComSoNoBundle(t *testing.T) { // .tar.gz ou build.sh
	bundle, _ := fakeHome(t)
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, Binary), []byte("so"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Sync(userList(t, "Meu Som")); err != nil {
		t.Fatal(err)
	}
	m := read(t, filepath.Join(bundle, "manifest.ttl"))
	if !strings.Contains(m, URIBase+"#usuario-meu-som>") || !strings.Contains(m, URIBase+"#fabrica-clean>") {
		t.Errorf("manifest sem os presets:\n%s", m)
	}
	if !strings.Contains(read(t, filepath.Join(bundle, "ampsim.ttl")), `doap:name "AmpSim: Meu Som"`) {
		t.Error("ampsim.ttl sem o nome do preset do usuário")
	}
	// apagou o preset: sai da lista
	if err := Sync(userList(t)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, filepath.Join(bundle, "manifest.ttl")), "usuario-meu-som") {
		t.Error("o preset apagado continua no manifest")
	}
	if read(t, filepath.Join(bundle, Binary)) != "so" {
		t.Error("o .so do bundle mudou")
	}
	clap, _ := UserCLAP()
	if dst, err := os.Readlink(clap); err != nil || dst != filepath.Join(bundle, Binary) {
		t.Errorf("link do CLAP: %q, %v", dst, err)
	}
}

func TestSyncComSoDoSistema(t *testing.T) { // .deb
	bundle, syslib := fakeHome(t)
	if err := os.WriteFile(syslib, []byte("so"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Sync(userList(t)); err != nil {
		t.Fatal(err)
	}
	if dst, err := os.Readlink(filepath.Join(bundle, Binary)); err != nil || dst != syslib {
		t.Fatalf("link do .so: %q, %v", dst, err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "manifest.ttl")); err != nil {
		t.Fatal(err)
	}
	// o .deb foi removido: o link ficou quebrado e o bundle sai
	if err := os.Remove(syslib); err != nil {
		t.Fatal(err)
	}
	if err := Sync(userList(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(bundle); !os.IsNotExist(err) {
		t.Errorf("o bundle com o link quebrado continua: %v", err)
	}
	if clap, _ := UserCLAP(); func() bool { _, err := os.Lstat(clap); return !os.IsNotExist(err) }() {
		t.Error("o link do CLAP continua sem o bundle")
	}
}

// Os plugins têm portas com símbolos válidos (identificadores C, sem repetir) e nenhuma lista
// de opções (a lilv embaralha a ordem; o Audacity mostra a opção errada).
func TestPlugins(t *testing.T) {
	pl := Plugins(plugin.FromList(presets.Factory()))
	pedals := 0
	uris := map[string]bool{}
	for _, p := range pl {
		if uris[p.URI()] {
			t.Errorf("URI repetida: %s", p.URI())
		}
		uris[p.URI()] = true
		if p.Pedal != "" {
			pedals++
		}
		seen := map[string]bool{"latencia": true, "in": true, "out": true, "in_l": true, "in_r": true, "out_l": true, "out_r": true}
		for _, c := range p.Controls {
			ok := c.Symbol != "" && !seen[c.Symbol]
			for i, r := range c.Symbol {
				ok = ok && (r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9')
			}
			if !ok {
				t.Errorf("%s: símbolo inválido ou repetido %q", p.ID, c.Symbol)
			}
			seen[c.Symbol] = true
		}
		if got, ok := Find(p.URI(), plugin.FromList(presets.Factory())); !ok || got.ID != p.ID {
			t.Errorf("Find(%s) = %v, %v", p.URI(), got.ID, ok)
		}
	}
	if pedals != 2*len(plugin.Pedals(false)) || len(pl) != pedals+2*len(presets.Factory()) {
		t.Errorf("%d plugins, %d pedais", len(pl), pedals)
	}
	if d := Describe(pl); strings.Contains(d, "lv2:enumeration") {
		t.Error("o .ttl tem lv2:enumeration")
	}
}

func TestVarianteEstereo(t *testing.T) {
	ps := plugin.FromList(presets.Factory())
	m, ok1 := Find(URIBase+"#pedal-delay", ps)
	st, ok2 := Find(URIBase+"#pedal-delay-estereo", ps)
	if !ok1 || !ok2 || m.Stereo || !st.Stereo || st.Name != "AmpSim Pedal: Delay (estéreo)" {
		t.Fatalf("delay: %+v / %+v", m.ID, st.ID)
	}
	if len(st.Controls) != len(m.Controls)+2 { // Largura e Estéreo (ping-pong)
		t.Errorf("delay estéreo com %d controles, mono %d", len(st.Controls), len(m.Controls))
	}
	if st.NumPorts() != len(st.Controls)+5 || st.PortOut(1) != st.NumPorts()-1 || st.PortIn(1) != st.PortIn(0)+1 {
		t.Errorf("portas do estéreo: %d, in %d %d, out %d %d", st.NumPorts(), st.PortIn(0), st.PortIn(1), st.PortOut(0), st.PortOut(1))
	}
	c, ok := Find(URIBase+"#fabrica-clean-estereo", ps)
	if !ok || !c.Stereo || c.Name != "AmpSim: Clean (estéreo)" || c.Preset.Name != "Clean" {
		t.Errorf("Clean estéreo: %+v", c.ID)
	}
	if !strings.Contains(Describe([]Plugin{c}), `lv2:symbol "out_r"`) {
		t.Error("sem a porta out_r")
	}
}
