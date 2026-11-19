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
}
