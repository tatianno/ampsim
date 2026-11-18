package source

import (
	"math"
	"sync/atomic"

	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/engine"
)

// ChannelMix seleciona a média de todos os canais de entrada.
const ChannelMix = -1

// ClipLevel é o nível (linear) a partir do qual consideramos que o conversor da placa clipou.
const ClipLevel = 0.999

// LiveInput é a entrada ao vivo (a guitarra plugada na placa).
//
// A cada callback o render chama Feed com o buffer de entrada da placa (intercalado);
// depois o engine lê com Read, como de qualquer outra fonte. Feed só guarda a referência
// do buffer (válido durante o callback), sem copiar nem alocar.
type LiveInput struct {
	channel atomic.Int32 // canal (0, 1, ...) ou ChannelMix
	trim    *engine.Param
	gain    dsp.Smoother

	// estado do callback atual (thread de áudio)
	in      []float32
	ch, n   int
	pos     int
	rawPeak float32 // pico antes do trim, no canal escolhido
	clips   uint32  // amostras que bateram no limite do conversor
}

// NewLiveInput cria a entrada ao vivo para a taxa fs.
func NewLiveInput(fs float64) *LiveInput {
	l := &LiveInput{
		trim: engine.NewParam(engine.ParamInfo{ID: "trim", Name: "Trim", Unit: "dB", Min: -24, Max: 24, Default: 0}),
		gain: dsp.NewSmoother(fs, 20),
	}
	return l
}

// Trim devolve o parâmetro de ganho de entrada (dB), para a GUI e o console.
func (l *LiveInput) Trim() *engine.Param { return l.trim }

// SetChannel escolhe o canal de entrada (0, 1, ...) ou ChannelMix.
func (l *LiveInput) SetChannel(c int) { l.channel.Store(int32(c)) }

// Channel devolve o canal escolhido.
func (l *LiveInput) Channel() int { return int(l.channel.Load()) }

// Feed entrega o buffer de entrada do callback (intercalado, channels canais, frames quadros).
// Mede o pico e os clips do canal escolhido, antes do trim.
func (l *LiveInput) Feed(in []float32, channels, frames int) {
	l.in, l.ch, l.n, l.pos = in, channels, frames, 0
	l.rawPeak, l.clips = 0, 0
	if channels <= 0 {
		l.n = 0
		return
	}
	for i := 0; i < frames; i++ {
		a := float32(math.Abs(l.raw(i)))
		l.rawPeak = max(l.rawPeak, a)
		if a >= ClipLevel {
			l.clips++
		}
	}
}

// raw devolve a amostra i do canal escolhido (ou a média), antes do trim.
func (l *LiveInput) raw(i int) float64 {
	c := int(l.channel.Load())
	base := i * l.ch
	if c == ChannelMix {
		s := float32(0)
		for k := 0; k < l.ch; k++ {
			s += l.in[base+k]
		}
		return float64(s) / float64(l.ch)
	}
	if c < 0 || c >= l.ch {
		c = 0
	}
	return float64(l.in[base+c])
}

// RawAt devolve a amostra i do callback atual, antes do trim (usado pelo teste de latência).
func (l *LiveInput) RawAt(i int) float64 {
	if i < 0 || i >= l.n {
		return 0
	}
	return l.raw(i)
}

// RawPeak devolve o pico (antes do trim) e o número de clips do último Feed.
func (l *LiveInput) RawPeak() (float32, uint32) { return l.rawPeak, l.clips }

// Read implementa Source: entrega as próximas amostras do callback atual, com o trim aplicado.
func (l *LiveInput) Read(buf []float64) {
	l.gain.SetTarget(math.Pow(10, l.trim.Get()/20))
	for i := range buf {
		if l.pos < l.n {
			buf[i] = l.raw(l.pos) * l.gain.Next()
			l.pos++
		} else {
			buf[i] = 0
		}
	}
}
