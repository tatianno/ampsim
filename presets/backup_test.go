package presets

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tatianno/ampsim/capturas"
)

// montaUsuario cria, em base/presets, um preset que usa um .nam do usuário e uma IR incluída,
// e uma pedaleira.json.
func montaUsuario(t *testing.T, base string) (dir, ped, nam string) {
	t.Helper()
	dir = filepath.Join(base, "presets")
	ped = filepath.Join(base, "pedaleira.json")
	nam = filepath.Join(t.TempDir(), "Meu Amp.nam")
	if err := os.WriteFile(nam, []byte("modelo"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Factory()[0].State
	st.Modules = cloneModules(st.Modules)
	for i, m := range st.Modules {
		if m.ID == "pre" {
			st.Modules[i].Extra = map[string]string{"nam": nam}
		}
	}
	if _, err := Save(dir, "Meu Lead", st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ped, []byte(`{"slots":["Meu Lead","Metal"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, ped, nam
}

func TestBackupERestauracao(t *testing.T) {
	src := t.TempDir()
	dir, ped, _ := montaUsuario(t, src)
	zipPath := filepath.Join(t.TempDir(), "backup.zip")
	res, err := Backup(zipPath, dir, ped)
	if err != nil {
		t.Fatal(err)
	}
	if res.Presets != 1 || !res.Pedaleira || res.Files != 1 || len(res.Missing) != 0 {
		t.Fatalf("backup = %+v", res)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	zr.Close()
	for _, want := range []string{"presets/meu-lead.json", "arquivos/Meu Amp.nam", "pedaleira.json"} {
		if !strings.Contains(strings.Join(names, "|"), want) {
			t.Errorf("zip sem %s: %v", want, names)
		}
	}

	// restaura numa instalação vazia (sem backup automático)
	dst := t.TempDir()
	dir2, ped2 := filepath.Join(dst, "presets"), filepath.Join(dst, "pedaleira.json")
	rr, err := Restore(zipPath, dir2, ped2)
	if err != nil {
		t.Fatal(err)
	}
	if rr.Presets != 1 || !rr.Pedaleira || rr.Files != 1 || rr.Safety != "" {
		t.Fatalf("restauração = %+v", rr)
	}
	p, err := Load(filepath.Join(dir2, "meu-lead.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, m := range p.State.Modules {
		if m.ID == "pre" {
			got = m.Extra["nam"]
		}
		if m.ID == "cab" && !strings.HasPrefix(m.Extra["ir"], capturas.Prefix) {
			t.Errorf("IR incluída deveria continuar incluída: %q", m.Extra["ir"])
		}
	}
	if want := filepath.Join(ArquivosDir(dir2), "Meu Amp.nam"); got != want {
		t.Errorf("nam = %q, quer %q", got, want)
	}
	if b, _ := os.ReadFile(got); string(b) != "modelo" {
		t.Errorf("conteúdo do .nam restaurado = %q", b)
	}
	if b, _ := os.ReadFile(ped2); !strings.Contains(string(b), "Meu Lead") {
		t.Errorf("pedaleira restaurada = %q", b)
	}

	// restaurar por cima de dados existentes guarda um backup automático antes
	if err := os.WriteFile(ped2, []byte(`{"slots":["Clean"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rr, err = Restore(zipPath, dir2, ped2)
	if err != nil {
		t.Fatal(err)
	}
	if rr.Safety == "" {
		t.Fatal("deveria ter feito o backup automático")
	}
	if _, err := os.Stat(rr.Safety); err != nil {
		t.Fatal(err)
	}
}

func TestBackupArquivoSumiu(t *testing.T) {
	dir, ped, nam := montaUsuario(t, t.TempDir())
	os.Remove(nam)
	res, err := Backup(filepath.Join(t.TempDir(), "b.zip"), dir, ped)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 0 || len(res.Missing) != 1 || res.Missing[0] != nam {
		t.Fatalf("backup = %+v", res)
	}
}

func TestRestauraArquivoQueNaoEBackup(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "x.zip")
	os.WriteFile(bad, []byte("não é zip"), 0o644)
	if _, err := Restore(bad, t.TempDir(), ""); err == nil {
		t.Fatal("deveria recusar")
	}
	// zip válido, mas sem nada do AmpSim
	empty := filepath.Join(t.TempDir(), "vazio.zip")
	f, _ := os.Create(empty)
	zw := zip.NewWriter(f)
	writeZip(zw, "outra/coisa.txt", []byte("oi"))
	zw.Close()
	f.Close()
	if _, err := Restore(empty, t.TempDir(), ""); err == nil {
		t.Fatal("deveria recusar zip sem presets")
	}
}
