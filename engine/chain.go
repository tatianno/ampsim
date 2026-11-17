package engine

import (
	"fmt"
	"math"
	"sync/atomic"

	"ampsim/dsp"
)

// BypassFadeMs é a duração do crossfade ao ligar/desligar um módulo (evita estalo).
const BypassFadeMs = 10.0 // float: 10/1000 entre constantes inteiras daria 0

type slot struct {
	m       Module
	enabled atomic.Bool

	lat     int
	gain    float64 // 0 = bypass total, 1 = efeito total
	step    float64
	dry     []float64
	dryLine *dsp.DelayLine // atrasa o sinal seco pela latência do módulo (crossfade alinhado)
}

// Chain é a cadeia de módulos em série. Implementa a interface de processamento que
// o app (malgo) e o plugin vão chamar.
type Chain struct {
	slots    []*slot
	byID     map[string]*slot
	fs       float64
	maxBlock int
	mono     []float64
	prepared bool
}

// NewChain monta uma cadeia com os módulos na ordem dada (todos ligados).
func NewChain(mods ...Module) *Chain {
	c := &Chain{byID: make(map[string]*slot)}
	for _, m := range mods {
		s := &slot{m: m}
		s.enabled.Store(true)
		c.slots = append(c.slots, s)
		c.byID[m.ID()] = s
	}
	return c
}

// NewDefaultChain monta a cadeia padrão: Gate → Amp → Chorus → Flanger → Phaser → Delay → Reverb.
// Gate e Amp começam ligados; os pedais de modulação, desligados.
func NewDefaultChain() *Chain {
	c := NewChain(NewGate(), NewAmp(), NewChorus(), NewFlanger(), NewPhaser(), NewDelay(), NewReverb())
	for _, id := range []string{"chorus", "flanger", "phaser", "delay", "reverb"} {
		c.SetEnabled(id, false)
	}
	return c
}

// Modules devolve os módulos na ordem da cadeia.
func (c *Chain) Modules() []Module {
	out := make([]Module, len(c.slots))
	for i, s := range c.slots {
		out[i] = s.m
	}
	return out
}

// Module busca um módulo pelo ID (nil se não existir).
func (c *Chain) Module(id string) Module {
	if s := c.byID[id]; s != nil {
		return s.m
	}
	return nil
}

// Amp devolve o módulo de amp, se houver.
func (c *Chain) Amp() *Amp {
	a, _ := c.Module("amp").(*Amp)
	return a
}

// SetEnabled liga ou desliga um módulo (seguro com o áudio rodando).
func (c *Chain) SetEnabled(id string, on bool) error {
	s := c.byID[id]
	if s == nil {
		return fmt.Errorf("módulo desconhecido: %q", id)
	}
	s.enabled.Store(on)
	return nil
}

// Enabled informa se o módulo está ligado.
func (c *Chain) Enabled(id string) bool {
	s := c.byID[id]
	return s != nil && s.enabled.Load()
}

// Prepare prepara a cadeia para a taxa fs e blocos de até maxBlock amostras. Aloca.
func (c *Chain) Prepare(fs float64, maxBlock int) {
	c.fs, c.maxBlock = fs, maxBlock
	c.mono = make([]float64, maxBlock)
	for _, s := range c.slots {
		s.m.Prepare(fs, maxBlock)
		s.lat = s.m.Latency()
		s.step = 1 / (BypassFadeMs / 1000 * fs)
		s.dry = make([]float64, maxBlock)
		s.dryLine = nil
		if s.lat > 0 {
			s.dryLine = dsp.NewDelayLine(s.lat)
		}
		if s.enabled.Load() {
			s.gain = 1
		} else {
			s.gain = 0
		}
	}
	c.prepared = true
}

// SampleRate devolve a taxa preparada.
func (c *Chain) SampleRate() float64 { return c.fs }

// MaxBlock devolve o maior bloco aceito por chamada de ProcessMono.
func (c *Chain) MaxBlock() int { return c.maxBlock }

// Reset limpa o estado de todos os módulos (caudas de delay/reverb etc.).
func (c *Chain) Reset() {
	for _, s := range c.slots {
		s.m.Reset()
		if s.dryLine != nil {
			s.dryLine.Reset()
		}
	}
}

// Latency devolve a latência total em amostras. É constante: módulos em bypass
// continuam atrasando o sinal seco, então ligar/desligar pedais não desalinha nada.
func (c *Chain) Latency() int {
	total := 0
	for _, s := range c.slots {
		total += s.m.Latency()
	}
	return total
}

// ProcessMono processa um bloco mono in-place, de qualquer tamanho.
func (c *Chain) ProcessMono(buf []float64) {
	for len(buf) > 0 {
		n := min(len(buf), c.maxBlock)
		for _, s := range c.slots {
			s.process(buf[:n])
		}
		buf = buf[n:]
	}
}

// Process é a interface multicanal (float32) usada pelo app e pelo plugin:
// mistura as entradas em mono, processa e copia o resultado para todas as saídas.
func (c *Chain) Process(in, out [][]float32, n int) {
	for off := 0; off < n; off += c.maxBlock {
		k := min(c.maxBlock, n-off)
		m := c.mono[:k]
		clear(m)
		if len(in) > 0 {
			inv := 1 / float64(len(in))
			for _, ch := range in {
				for i := range m {
					m[i] += float64(ch[off+i]) * inv
				}
			}
		}
		c.ProcessMono(m)
		for _, ch := range out {
			for i, v := range m {
				ch[off+i] = float32(v)
			}
		}
	}
}

func (s *slot) process(buf []float64) {
	target := 0.0
	if s.enabled.Load() {
		target = 1
	}

	// Bypass total: só mantém o atraso do sinal seco.
	if s.gain == 0 && target == 0 {
		if s.dryLine != nil {
			for i, x := range buf {
				buf[i] = s.dryLine.ReadInt(s.lat)
				s.dryLine.Write(x)
			}
		}
		return
	}
	if s.gain == 0 {
		s.m.Reset() // pedal sendo ligado: começa sem resto de estado antigo
	}

	dry := s.dry[:len(buf)]
	copy(dry, buf)
	s.m.Process(buf)

	if s.gain == 1 && target == 1 {
		if s.dryLine != nil {
			for _, x := range dry {
				s.dryLine.Write(x)
			}
		}
		return
	}
	// Crossfade entre efeito e seco (seco atrasado pela latência do módulo).
	for i, x := range dry {
		d := x
		if s.dryLine != nil {
			d = s.dryLine.ReadInt(s.lat)
			s.dryLine.Write(x)
		}
		if s.gain < target {
			s.gain = math.Min(target, s.gain+s.step)
		} else if s.gain > target {
			s.gain = math.Max(target, s.gain-s.step)
		}
		buf[i] = s.gain*buf[i] + (1-s.gain)*d
	}
}
