// Pacote audio liga uma fonte (arquivo ou guitarra) ao engine e à placa de som.
//
//	fonte.Read → engine.ProcessMono → clip de segurança → saída intercalada
//
// O render é separado do dispositivo (malgo) para poder ser testado sem placa de som.
package audio

import (
	"math"
	"sync/atomic"
	"time"

	"ampsim/cmd/ampapp/source"
	"ampsim/engine"
)

// MaxBlock é o maior bloco processado de uma vez (callbacks maiores são divididos).
const MaxBlock = 1024

// Meter é o resumo de um callback, enviado à GUI pelo Ring.
type Meter struct {
	InPeak, OutPeak float32 // pico absoluto (linear)
	OutClips        uint32  // amostras que passaram de ±1 (cortadas)
	GateOpen        bool
	Load            float32 // tempo de processamento / duração do bloco
	Frames          uint32
}

type sourceRef struct{ s source.Source }

// Renderer processa os blocos de áudio. Não aloca em Render.
type Renderer struct {
	chain    *engine.Chain
	fs       float64
	src      atomic.Pointer[sourceRef]
	mono     []float64
	meters   *Ring[Meter]
	dropped  atomic.Uint64
	callback atomic.Uint64
}

// NewRenderer cria o render para a cadeia (já preparada em fs com blocos de MaxBlock).
func NewRenderer(chain *engine.Chain, fs float64) *Renderer {
	r := &Renderer{chain: chain, fs: fs, mono: make([]float64, MaxBlock), meters: NewRing[Meter](256)}
	r.SetSource(source.Silence{})
	return r
}

// SetSource troca a fonte (seguro com o áudio rodando).
func (r *Renderer) SetSource(s source.Source) { r.src.Store(&sourceRef{s}) }

// Meters devolve a fila de medidores (a GUI consome).
func (r *Renderer) Meters() *Ring[Meter] { return r.meters }

// Callbacks devolve quantos callbacks já rodaram.
func (r *Renderer) Callbacks() uint64 { return r.callback.Load() }

// Render preenche out (intercalado, channels canais) com frames quadros.
func (r *Renderer) Render(out []float32, channels, frames int) {
	start := time.Now()
	src := r.src.Load().s
	var m Meter
	m.Frames = uint32(frames)

	for off := 0; off < frames; off += MaxBlock {
		n := min(MaxBlock, frames-off)
		buf := r.mono[:n]
		src.Read(buf)
		for _, v := range buf {
			m.InPeak = max(m.InPeak, float32(math.Abs(v)))
		}
		r.chain.ProcessMono(buf)
		base := off * channels
		for i, v := range buf {
			a := math.Abs(v)
			m.OutPeak = max(m.OutPeak, float32(a))
			if a > 1 { // proteção dos ouvidos e das caixas
				m.OutClips++
				v = math.Copysign(1, v)
			}
			f := float32(v)
			for c := 0; c < channels; c++ {
				out[base+i*channels+c] = f
			}
		}
	}

	if g, ok := r.chain.Module("gate").(*engine.Gate); ok && r.chain.Enabled("gate") {
		m.GateOpen = g.IsOpen()
	} else {
		m.GateOpen = true
	}
	budget := float64(frames) / r.fs
	m.Load = float32(time.Since(start).Seconds() / budget)
	if !r.meters.Push(m) {
		r.dropped.Add(1)
	}
	r.callback.Add(1)
}
