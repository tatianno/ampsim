// Pacote capturas traz os modelos NAM incluídos no app (embutidos no binário).
//
// São capturas do autor do projeto, só do amplificador (sem gabinete): no app elas
// passam pelo gabinete por IR. Cada arquivo é um SlimmableContainer com um modelo lite
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

//go:embed *.nam
var files embed.FS

// Prefix marca, no estado do engine, um modelo embutido (ex.: "embutida:Clean V10 B5 M5 T5 P5").
const Prefix = "embutida:"

// Names devolve os nomes das capturas (o nome do arquivo sem .nam), em ordem natural
// (V7 antes de V10).
func Names() []string {
	ents, _ := fs.ReadDir(files, ".")
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".nam"); ok {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, naturalCompare)
	return out
}

// Data devolve o conteúdo do .nam de uma captura.
func Data(name string) ([]byte, error) {
	b, err := files.ReadFile(path.Clean(name) + ".nam")
	if err != nil {
		return nil, fmt.Errorf("captura desconhecida: %q", name)
	}
	return b, nil
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
