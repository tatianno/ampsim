package presets

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/engine"
)

// Backup e restauração dos presets do usuário.
//
// O arquivo é um .zip com:
//
//	presets/<nome>.json    os presets do usuário
//	pedaleira.json         a configuração da pedaleira (se existir)
//	arquivos/<nome>        os modelos .nam e IRs do usuário usados pelos presets
//
// Nos presets dentro do zip, o caminho de um arquivo do usuário vira "arquivos/<nome>"; na
// restauração, os arquivos vão para a pasta ArquivosDir e os presets passam a apontar para lá.
// As capturas e IRs incluídas no app ("incluída:...") ficam como estão.

// Pastas e nomes dentro do zip.
const (
	zipPresets   = "presets/"
	zipFiles     = "arquivos/"
	zipPedaleira = "pedaleira.json"
)

// maxZipEntry limita o tamanho de cada item lido de um backup (protege contra zips malformados).
const maxZipEntry = 256 << 20

// extraFileKeys são as chaves de Extra que guardam caminhos de arquivo.
var extraFileKeys = []string{"nam", "ir"}

// BackupResult resume um backup.
type BackupResult struct {
	Presets   int
	Pedaleira bool
	Files     int      // arquivos .nam/IR incluídos
	Missing   []string // arquivos citados pelos presets que não existem mais
}

// RestoreResult resume uma restauração.
type RestoreResult struct {
	Presets   int
	Pedaleira bool
	Files     int
	Safety    string // backup automático do estado anterior ("" se não havia nada para guardar)
}

// ArquivosDir é onde a restauração põe os .nam e IRs do usuário (ao lado da pasta de presets).
func ArquivosDir(dir string) string { return filepath.Join(filepath.Dir(dir), "arquivos") }

// BackupsDir é onde ficam os backups automáticos feitos antes de restaurar.
func BackupsDir(dir string) string { return filepath.Join(filepath.Dir(dir), "backups") }

// BackupName sugere o nome do arquivo de backup.
func BackupName(t time.Time) string { return "ampsim-presets-" + t.Format("2006-01-02") + ".zip" }

// isUserFile diz se o valor de Extra é um arquivo do usuário (e não um item incluído no app).
func isUserFile(v string) bool { return v != "" && !strings.HasPrefix(v, capturas.Prefix) }

// Backup grava em zipPath os presets do usuário (pasta dir), a pedaleira (pedaleiraPath, pode
// ser "") e os arquivos .nam/IR que os presets usam.
func Backup(zipPath, dir, pedaleiraPath string) (BackupResult, error) {
	var res BackupResult
	list, err := List(dir)
	if err != nil {
		return res, fmt.Errorf("lendo os presets: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(zipPath), ".ampsim-backup-*.zip")
	if err != nil {
		return res, err
	}
	defer os.Remove(tmp.Name()) // some se der erro; depois do Rename não existe mais
	zw := zip.NewWriter(tmp)

	files := map[string]string{} // caminho original → nome dentro de arquivos/
	used := map[string]bool{}    // nomes já usados em arquivos/
	names := map[string]bool{}   // nomes de preset já usados em presets/
	for _, p := range list {
		if p.Factory {
			continue
		}
		st := p.State
		st.Modules = cloneModules(st.Modules)
		for i := range st.Modules {
			for _, k := range extraFileKeys {
				v := st.Modules[i].Extra[k]
				if !isUserFile(v) {
					continue
				}
				name, ok := files[v]
				if !ok {
					if _, err := os.Stat(v); err != nil {
						res.Missing = append(res.Missing, v)
						continue // fica o caminho original
					}
					name = uniqueName(filepath.Base(v), used)
					files[v] = name
				}
				st.Modules[i].Extra[k] = zipFiles + name
			}
		}
		b, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return res, err
		}
		fn := uniqueName(filepath.Base(p.Path), names)
		if err := writeZip(zw, zipPresets+fn, append(b, '\n')); err != nil {
			return res, err
		}
		res.Presets++
	}
	for orig, name := range files {
		b, err := os.ReadFile(orig)
		if err != nil {
			return res, err
		}
		if err := writeZip(zw, zipFiles+name, b); err != nil {
			return res, err
		}
		res.Files++
	}
	if pedaleiraPath != "" {
		if b, err := os.ReadFile(pedaleiraPath); err == nil {
			if err := writeZip(zw, zipPedaleira, b); err != nil {
				return res, err
			}
			res.Pedaleira = true
		}
	}
	if err := zw.Close(); err != nil {
		return res, err
	}
	if err := tmp.Close(); err != nil {
		return res, err
	}
	return res, os.Rename(tmp.Name(), zipPath)
}

// Restore lê um backup: os presets do zip substituem os de mesmo nome, a pedaleira é
// substituída (se o zip tiver uma) e os arquivos .nam/IR vão para ArquivosDir(dir). Antes de
// mexer em qualquer coisa, o estado atual é guardado em BackupsDir(dir).
func Restore(zipPath, dir, pedaleiraPath string) (RestoreResult, error) {
	var res RestoreResult
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return res, fmt.Errorf("não é um backup válido: %w", err)
	}
	defer zr.Close()

	// lê e valida tudo antes de gravar qualquer coisa
	type presetFile struct {
		name string
		st   engine.State
	}
	var pres []presetFile
	var ped []byte
	files := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		switch {
		case strings.HasPrefix(f.Name, zipPresets):
			name := filepath.Base(f.Name)
			if !strings.EqualFold(filepath.Ext(name), ".json") || f.Name != zipPresets+name {
				continue
			}
			b, err := readZip(f)
			if err != nil {
				return res, err
			}
			var st engine.State
			if err := json.Unmarshal(b, &st); err != nil {
				return res, fmt.Errorf("%s: %w", f.Name, err)
			}
			pres = append(pres, presetFile{name, st})
		case strings.HasPrefix(f.Name, zipFiles):
			name := filepath.Base(f.Name)
			if f.Name != zipFiles+name || name == "." || name == ".." {
				continue // ignora subpastas e nomes estranhos
			}
			b, err := readZip(f)
			if err != nil {
				return res, err
			}
			files[name] = b
		case f.Name == zipPedaleira:
			b, err := readZip(f)
			if err != nil {
				return res, err
			}
			var probe any
			if err := json.Unmarshal(b, &probe); err != nil {
				return res, fmt.Errorf("%s: %w", f.Name, err)
			}
			ped = b
		}
	}
	if len(pres) == 0 && ped == nil {
		return res, errors.New("o arquivo não tem presets nem pedaleira do AmpSim")
	}

	// backup automático do que existe hoje
	if hasUserData(dir, pedaleiraPath) {
		if err := os.MkdirAll(BackupsDir(dir), 0o755); err != nil {
			return res, err
		}
		safety := filepath.Join(BackupsDir(dir), "antes-de-restaurar-"+time.Now().Format("2006-01-02-150405")+".zip")
		if _, err := Backup(safety, dir, pedaleiraPath); err != nil {
			return res, fmt.Errorf("backup automático antes de restaurar: %w", err)
		}
		res.Safety = safety
	}

	if len(files) > 0 {
		if err := os.MkdirAll(ArquivosDir(dir), 0o755); err != nil {
			return res, err
		}
		for name, b := range files {
			if err := os.WriteFile(filepath.Join(ArquivosDir(dir), name), b, 0o644); err != nil {
				return res, err
			}
			res.Files++
		}
	}
	if len(pres) > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return res, err
		}
	}
	for _, p := range pres {
		for i := range p.st.Modules {
			for _, k := range extraFileKeys {
				if v := p.st.Modules[i].Extra[k]; strings.HasPrefix(v, zipFiles) {
					p.st.Modules[i].Extra[k] = filepath.Join(ArquivosDir(dir), filepath.Base(v))
				}
			}
		}
		b, err := json.MarshalIndent(p.st, "", "  ")
		if err != nil {
			return res, err
		}
		if err := os.WriteFile(filepath.Join(dir, p.name), append(b, '\n'), 0o644); err != nil {
			return res, err
		}
		res.Presets++
	}
	if ped != nil && pedaleiraPath != "" {
		if err := os.WriteFile(pedaleiraPath, ped, 0o644); err != nil {
			return res, err
		}
		res.Pedaleira = true
	}
	return res, nil
}

// hasUserData diz se há presets do usuário ou pedaleira para guardar.
func hasUserData(dir, pedaleiraPath string) bool {
	if pedaleiraPath != "" {
		if _, err := os.Stat(pedaleiraPath); err == nil {
			return true
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			return true
		}
	}
	return false
}

func cloneModules(ms []engine.ModuleState) []engine.ModuleState {
	out := make([]engine.ModuleState, len(ms))
	for i, m := range ms {
		out[i] = m
		if m.Extra != nil {
			out[i].Extra = make(map[string]string, len(m.Extra))
			for k, v := range m.Extra {
				out[i].Extra[k] = v
			}
		}
	}
	return out
}

// uniqueName devolve name, ou name-2, name-3... se já foi usado (sem diferenciar maiúsculas).
func uniqueName(name string, used map[string]bool) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	out := name
	for n := 2; used[strings.ToLower(out)]; n++ {
		out = fmt.Sprintf("%s-%d%s", stem, n, ext)
	}
	used[strings.ToLower(out)] = true
	return out
}

func writeZip(zw *zip.Writer, name string, b []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readZip(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxZipEntry {
		return nil, fmt.Errorf("%s: grande demais", f.Name)
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, maxZipEntry+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	if len(b) > maxZipEntry {
		return nil, fmt.Errorf("%s: grande demais", f.Name)
	}
	return b, nil
}
