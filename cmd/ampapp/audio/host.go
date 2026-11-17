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

	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/engine"
)

// MaxBlock é o maior bloco processado de uma vez (callbacks maiores são divididos).
const MaxBlock = 1024

// Meter é o resumo de um callback, enviado à GUI pelo Ring.
type Meter struct {
	InPeak, OutPeak float32 // pico absoluto (linear)
	OutClips        uint32  // amostras que passaram de ±1 (cortadas)
	GateOpen        bool
	Bypassed        bool    // bypass A/B global ligado
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

	// bypass A/B global: crossfade entre o processado e o seco (atrasado pela latência da cadeia)
	bypass  atomic.Bool
	abGain  float64 // 1 = processado, 0 = seco
	abStep  float64
	dry     []float64
	dryLine *dsp.DelayLine
	lat     int
}

// ABFadeMs é a duração do crossfade do bypass A/B.
const ABFadeMs = 10.0

// NewRenderer cria o render para a cadeia (já preparada em fs com blocos de MaxBlock).
func NewRenderer(chain *engine.Chain, fs float64) *Renderer {
	r := &Renderer{chain: chain, fs: fs, mono: make([]float64, MaxBlock), meters: NewRing[Meter](256)}
	r.SetSource(source.Silence{})
	r.abGain = 1
	r.abStep = 1 / (ABFadeMs / 1000 * fs)
	r.dry = make([]float64, MaxBlock)
	r.lat = chain.Latency()
	r.dryLine = dsp.NewDelayLine(max(1, r.lat))
	return r
}

// SetBypass liga/desliga o bypass A/B (ouve o sinal sem processamento, alinhado no tempo).
func (r *Renderer) SetBypass(on bool) { r.bypass.Store(on) }

// Bypass informa se o bypass A/B está ligado.
func (r *Renderer) Bypass() bool { return r.bypass.Load() }

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
		dry := r.dry[:n]
		copy(dry, buf)
		r.chain.ProcessMono(buf) // a cadeia sempre roda: caudas continuam e a volta do bypass é suave
		r.mixAB(buf, dry)
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
	m.Bypassed = r.bypass.Load()
	budget := float64(frames) / r.fs
	m.Load = float32(time.Since(start).Seconds() / budget)
	if !r.meters.Push(m) {
		r.dropped.Add(1)
	}
	r.callback.Add(1)
}

// mixAB aplica o crossfade do bypass A/B. O seco é atrasado pela latência da cadeia
// para ficar alinhado com o processado (a troca não "pula" no tempo).
func (r *Renderer) mixAB(wet, dry []float64) {
	target := 1.0
	if r.bypass.Load() {
		target = 0
	}
	for i, x := range dry {
		d := x
		if r.lat > 0 {
			d = r.dryLine.ReadInt(r.lat)
			r.dryLine.Write(x)
		}
		if r.abGain < target {
			r.abGain = math.Min(target, r.abGain+r.abStep)
		} else if r.abGain > target {
			r.abGain = math.Max(target, r.abGain-r.abStep)
		}
		if r.abGain != 1 {
			wet[i] = r.abGain*wet[i] + (1-r.abGain)*d
		}
	}
}
