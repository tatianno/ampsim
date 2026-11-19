// Pacote capturas traz os modelos NAM incluídos no app (embutidos no binário).
//
// São capturas do autor do projeto: as da raiz são só do amplificador (sem gabinete; no app
// elas passam pelo gabinete por IR) e as de pedais/ são só de pedais de drive (para o Drive
// no tipo NAM, na frente do amp). Cada arquivo é um SlimmableContainer com um modelo lite
// (3 canais) e um completo (8 canais). Veja o README desta pasta.
package capturas

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/tatianno/ampsim/nam"
)

//go:embed *.nam pedais/*.nam
var files embed.FS

// pedalDir é a pasta das capturas de pedais.
const pedalDir = "pedais"

// Prefix marca, no estado do engine, um modelo embutido (ex.: "embutida:Clean V10 B5 M5 T5 P5").
const Prefix = "embutida:"

// Names devolve os nomes das capturas de amp (o nome do arquivo sem .nam), em ordem natural
// (V7 antes de V10).
func Names() []string { return list(".") }

// PedalNames devolve os nomes das capturas de pedais, em ordem natural.
func PedalNames() []string { return list(pedalDir) }

func list(dir string) []string {
	ents, _ := fs.ReadDir(files, dir)
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".nam"); ok {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, naturalCompare)
	return out
}

// Data devolve o conteúdo do .nam de uma captura (de amp ou de pedal: os nomes não se repetem).
func Data(name string) ([]byte, error) {
	if !strings.ContainsAny(name, `/\`) {
		for _, dir := range []string{".", pedalDir} {
			if b, err := files.ReadFile(path.Join(dir, name+".nam")); err == nil {
				return b, nil
			}
		}
	}
	return nil, fmt.Errorf("captura desconhecida: %q", name)
}

// Load carrega e valida uma captura.
func Load(name string) (*nam.Model, error) {
	b, err := Data(name)
	if err != nil {
		return nil, err
	}
	m, err := nam.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("captura %q: %w", name, err)
	}
	return m, nil
}

// naturalCompare compara textos tratando sequências de dígitos como números.
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if c := len(na) - len(nb); c != 0 {
				return c
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		a, b = a[1:], b[1:]
	}
	return len(a) - len(b)
}

func digits(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}
