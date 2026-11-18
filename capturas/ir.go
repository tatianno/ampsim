package capturas

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/tatianno/ampsim/wav"
)

// As IRs de gabinete incluídas: gabinete_<falantes>_<microfone>.wav.
//
//go:embed gabinete_*.wav
var irFiles embed.FS

// DefaultIRName é o gabinete usado quando nenhuma IR é escolhida.
const DefaultIRName = "2x12 SM57"

type irData struct {
	fs  int
	raw []float64
	err error
}

var (
	irMu    sync.Mutex
	irCache = map[string]*irData{}
)

// irName converte "gabinete_2x12_sm57.wav" em "2x12 SM57".
func irName(file string) string {
	n := strings.TrimSuffix(strings.TrimPrefix(file, "gabinete_"), ".wav")
	spk, mic, _ := strings.Cut(n, "_")
	return strings.TrimSpace(strings.ToLower(spk) + " " + strings.ToUpper(strings.ReplaceAll(mic, "_", " ")))
}

// IRNames devolve os nomes das IRs incluídas (ex.: "2x12 SM57"), em ordem natural.
func IRNames() []string {
	ents, _ := fs.ReadDir(irFiles, ".")
	var out []string
	for _, e := range ents {
		out = append(out, irName(e.Name()))
	}
	slices.SortFunc(out, naturalCompare)
	return out
}

// LoadIR devolve uma IR incluída (mono) e a sua taxa. A decodificação é feita uma vez;
// o slice devolvido é compartilhado e não pode ser alterado.
func LoadIR(name string) (int, []float64, error) {
	irMu.Lock()
	defer irMu.Unlock()
	if d := irCache[name]; d != nil {
		return d.fs, d.raw, d.err
	}
	d := &irData{err: fmt.Errorf("IR desconhecida: %q", name)}
	ents, _ := fs.ReadDir(irFiles, ".")
	for _, e := range ents {
		if irName(e.Name()) != name {
			continue
		}
		b, _ := irFiles.ReadFile(e.Name())
		a, err := wav.Read(bytes.NewReader(b))
		if err != nil {
			d.err = fmt.Errorf("IR %q: %w", name, err)
		} else {
			d.fs, d.raw, d.err = a.SampleRate, a.Mono(), nil
		}
		break
	}
	irCache[name] = d
	return d.fs, d.raw, d.err
}

// DefaultIR devolve a IR do gabinete padrão (DefaultIRName).
func DefaultIR() (int, []float64, error) { return LoadIR(DefaultIRName) }
