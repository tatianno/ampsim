package audio

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tatianno/ampsim/wav"
)

// Recording é uma gravação na pasta: o som processado e, se houver, a DI.
type Recording struct {
	Out, DI string
	When    time.Time // da data/hora no nome do arquivo
	Seconds float64
	Bytes   int64 // soma dos arquivos
}

// Name devolve um nome legível ("18/11 15:30:02").
func (r Recording) Name() string { return r.When.Format("02/01 15:04:05") }

// ListRecordings lista as gravações do ampsim em dir, da mais nova para a mais antiga.
func ListRecordings(dir string) ([]Recording, error) {
	files, err := filepath.Glob(filepath.Join(dir, "ampsim-*.wav"))
	if err != nil {
		return nil, err
	}
	var out []Recording
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasSuffix(base, "-DI.wav") {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(base, "ampsim-"), ".wav")
		when, err := time.ParseInLocation("2006-01-02_15-04-05", stamp, time.Local)
		if err != nil {
			continue // não é uma gravação do ampsim
		}
		r := Recording{Out: f, When: when}
		if st, err := os.Stat(f); err == nil {
			r.Bytes = st.Size()
		}
		if info, err := wav.Stat(f); err == nil {
			r.Seconds = info.Duration()
		}
		di := strings.TrimSuffix(f, ".wav") + "-DI.wav"
		if st, err := os.Stat(di); err == nil {
			r.DI = di
			r.Bytes += st.Size()
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Recording) int { return b.When.Compare(a.When) })
	return out, nil
}
