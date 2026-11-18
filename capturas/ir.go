package capturas

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/tatianno/ampsim/wav"
)

//go:embed gabinete_2x12_sm57.wav
var defaultIR []byte

// DefaultIRName é o nome do gabinete interno, usado quando nenhuma IR é escolhida.
const DefaultIRName = "2x12 SM57"

var (
	irOnce sync.Once
	irFS   int
	irData []float64
	irErr  error
)

// DefaultIR devolve a IR do gabinete interno (mono) e a sua taxa. A decodificação é
// feita uma vez; o slice devolvido é compartilhado e não pode ser alterado.
func DefaultIR() (int, []float64, error) {
	irOnce.Do(func() {
		a, err := wav.Read(bytes.NewReader(defaultIR))
		if err != nil {
			irErr = fmt.Errorf("IR interna: %w", err)
			return
		}
		irFS, irData = a.SampleRate, a.Mono()
	})
	return irFS, irData, irErr
}
