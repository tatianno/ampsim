package engine

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
)

// BypassFadeMs é a duração do crossfade ao ligar/desligar um módulo (evita estalo).
const BypassFadeMs = 10.0 // float: 10/1000 entre constantes inteiras daria 0

// ReorderFadeMs é a duração de cada metade da rampa ao reordenar a cadeia
// (desce até o silêncio, troca a ordem, sobe de novo).
const ReorderFadeMs = 5.0

// IDs fixos das três partes do amp. A ordem entre elas é obrigatória: pre → power → cab.
const (
	PreID   = "pre"
	PowerID = "power"
	CabID   = "cab"
)

type slot struct {
	m       Module
	enabled atomic.Bool

	lat     int
	gain    float64 // 0 = bypass total, 1 = efeito total
	step    float64
	dry     []float64
	dryLine *dsp.DelayLine // atrasa o sinal seco pela latência do módulo (crossfade alinhado)
}

// Chain é a cadeia de módulos em série, com ordem configurável ao vivo. Implementa a
// interface de processamento que o app (malgo) e o plugin vão chamar.
type Chain struct {
	all  []*slot // todos os módulos, na ordem de criação
	byID map[string]*slot

	want atomic.Pointer[[]*slot] // última ordem pedida (escrita pela GUI)

	// estado da thread de áudio
	active *[]*slot // ordem em uso
	rgain  float64  // ganho da rampa de reordenação
	rstep  float64

	fs       float64
	maxBlock int
	mono     []float64
	prepared bool
}

// NewChain monta uma cadeia com os módulos na ordem dada (todos ligados).
func NewChain(mods ...Module) *Chain {
	c := &Chain{byID: make(map[string]*slot), rgain: 1}
	for _, m := range mods {
		s := &slot{m: m}
		s.enabled.Store(true)
		c.all = append(c.all, s)
		c.byID[m.ID()] = s
	}
	ord := slices.Clone(c.all)
	c.want.Store(&ord)
	c.active = &ord
	return c
}

// DefaultOrder é a ordem padrão: gate e compressor na frente do amp; EQ e os outros pedais no loop de efeitos.
var DefaultOrder = []string{"gate", "comp", PreID, "eq", "chorus", "flanger", "phaser", "delay", "reverb", PowerID, CabID}

// NewDefaultChain monta a cadeia padrão (DefaultOrder). Gate e amp começam ligados;
// os outros pedais, desligados.
func NewDefaultChain() *Chain {
	pre, pow := NewPre(), NewPower()
	pow.FollowAmpMode(pre)
	c := NewChain(NewGate(), NewCompressor(), pre, NewEQ(), NewChorus(), NewFlanger(), NewPhaser(), NewDelay(), NewReverb(), pow, NewCab())
	for _, id := range []string{"comp", "eq", "chorus", "flanger", "phaser", "delay", "reverb"} {
		c.SetEnabled(id, false)
	}
	return c
}

// Modules devolve os módulos na ordem da cadeia (a última pedida).
func (c *Chain) Modules() []Module {
	ord := *c.want.Load()
	out := make([]Module, len(ord))
	for i, s := range ord {
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

// Pre devolve o pré-amp, se houver.
func (c *Chain) Pre() *Pre { m, _ := c.Module(PreID).(*Pre); return m }

// Power devolve o power amp, se houver.
func (c *Chain) Power() *Power { m, _ := c.Module(PowerID).(*Power); return m }

// Cab devolve o gabinete, se houver.
func (c *Chain) Cab() *Cab { m, _ := c.Module(CabID).(*Cab); return m }

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

// ---------------------------------------------------------------- ordem

// Order devolve os IDs na ordem da cadeia.
func (c *Chain) Order() []string {
	ord := *c.want.Load()
	ids := make([]string, len(ord))
	for i, s := range ord {
		ids[i] = s.m.ID()
	}
	return ids
}

// SetOrder define a ordem completa da cadeia. Precisa conter todos os módulos uma vez,
// com pre antes de power e power antes de cab. Seguro com o áudio rodando: a troca
// acontece com uma rampa curta, sem estalo.
func (c *Chain) SetOrder(ids []string) error {
	ord, err := c.validate(ids)
	if err != nil {
		return err
	}
	c.want.Store(&ord)
	if !c.prepared {
		c.active = &ord
	}
	return nil
}

// Move leva o módulo id para a posição index (0 = início da cadeia).
func (c *Chain) Move(id string, index int) error {
	ids := c.Order()
	from := slices.Index(ids, id)
	if from < 0 {
		return fmt.Errorf("módulo desconhecido: %q", id)
	}
	if index < 0 || index >= len(ids) {
		return fmt.Errorf("posição %d fora da cadeia (0..%d)", index, len(ids)-1)
	}
	ids = slices.Delete(ids, from, from+1)
	ids = slices.Insert(ids, index, id)
	return c.SetOrder(ids)
}

// MoveBefore coloca id imediatamente antes de ref (ex.: MoveBefore("delay", "power") = último do loop).
func (c *Chain) MoveBefore(id, ref string) error { return c.moveRel(id, ref, 0) }

// MoveAfter coloca id imediatamente depois de ref.
func (c *Chain) MoveAfter(id, ref string) error { return c.moveRel(id, ref, 1) }

func (c *Chain) moveRel(id, ref string, off int) error {
	if id == ref {
		return fmt.Errorf("não dá para mover %q em relação a si mesmo", id)
	}
	ids := c.Order()
	from := slices.Index(ids, id)
	if from < 0 {
		return fmt.Errorf("módulo desconhecido: %q", id)
	}
	ids = slices.Delete(ids, from, from+1)
	to := slices.Index(ids, ref)
	if to < 0 {
		return fmt.Errorf("módulo desconhecido: %q", ref)
	}
	ids = slices.Insert(ids, to+off, id)
	return c.SetOrder(ids)
}

func (c *Chain) validate(ids []string) ([]*slot, error) {
	if len(ids) != len(c.all) {
		return nil, fmt.Errorf("a ordem precisa ter os %d módulos (%s), veio %d",
			len(c.all), strings.Join(c.allIDs(), ", "), len(ids))
	}
	seen := make(map[string]bool, len(ids))
	ord := make([]*slot, len(ids))
	for i, id := range ids {
		s := c.byID[id]
		if s == nil {
			return nil, fmt.Errorf("módulo desconhecido: %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("módulo repetido: %q", id)
		}
		seen[id] = true
		ord[i] = s
	}
	pos := func(id string) int { return slices.Index(ids, id) }
	if p, w := pos(PreID), pos(PowerID); p >= 0 && w >= 0 && p > w {
		return nil, fmt.Errorf("o pré-amp precisa vir antes do power amp")
	}
	if w, k := pos(PowerID), pos(CabID); w >= 0 && k >= 0 && w > k {
		return nil, fmt.Errorf("o power amp precisa vir antes do gabinete")
	}
	return ord, nil
}

func (c *Chain) allIDs() []string {
	ids := make([]string, len(c.all))
	for i, s := range c.all {
		ids[i] = s.m.ID()
	}
	return ids
}

// Section diz em que trecho da cadeia um módulo está, em relação ao amp.
type Section string

const (
	SectionInput     Section = "entrada"   // antes do pré-amp (na frente do amp)
	SectionLoop      Section = "loop"      // entre pré-amp e power amp (send/return)
	SectionPostPower Section = "pós-power" // entre power amp e gabinete
	SectionPost      Section = "pós"       // depois do gabinete (estilo estúdio)
	SectionAmp       Section = "amp"       // o próprio pre, power ou cab
)

// SectionOf devolve o trecho da cadeia em que o módulo id está.
func (c *Chain) SectionOf(id string) Section {
	if id == PreID || id == PowerID || id == CabID {
		return SectionAmp
	}
	ids := c.Order()
	i := slices.Index(ids, id)
	pre, pow, cab := slices.Index(ids, PreID), slices.Index(ids, PowerID), slices.Index(ids, CabID)
	switch {
	case pre >= 0 && i < pre:
		return SectionInput
	case pow >= 0 && i < pow:
		return SectionLoop
	case cab >= 0 && i < cab:
		return SectionPostPower
	default:
		return SectionPost
	}
}

// ---------------------------------------------------------------- processamento

// Prepare prepara a cadeia para a taxa fs e blocos de até maxBlock amostras. Aloca.
func (c *Chain) Prepare(fs float64, maxBlock int) {
	c.fs, c.maxBlock = fs, maxBlock
	c.mono = make([]float64, maxBlock)
	for _, s := range c.all {
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
	c.active = c.want.Load()
	c.rgain = 1
	c.rstep = 1 / (ReorderFadeMs / 1000 * fs)
	c.prepared = true
}

// SampleRate devolve a taxa preparada.
func (c *Chain) SampleRate() float64 { return c.fs }

// MaxBlock devolve o maior bloco aceito por chamada de ProcessMono.
func (c *Chain) MaxBlock() int { return c.maxBlock }

// Reset limpa o estado de todos os módulos (caudas de delay/reverb etc.).
func (c *Chain) Reset() {
	for _, s := range c.all {
		s.m.Reset()
		if s.dryLine != nil {
			s.dryLine.Reset()
		}
	}
}

// Latency devolve a latência total em amostras. É constante: módulos em bypass
// continuam atrasando o sinal seco, e a ordem não muda a soma.
func (c *Chain) Latency() int {
	total := 0
	for _, s := range c.all {
		total += s.m.Latency()
	}
	return total
}

// ProcessMono processa um bloco mono in-place, de qualquer tamanho.
func (c *Chain) ProcessMono(buf []float64) {
	for len(buf) > 0 {
		n := min(len(buf), c.maxBlock)
		b := buf[:n]
		for _, s := range *c.active {
			s.process(b)
		}
		if want := c.want.Load(); want != c.active || c.rgain < 1 {
			c.reorderRamp(b, want)
		}
		buf = buf[n:]
	}
}

// reorderRamp desce o volume até zero com a ordem antiga, troca a ordem e sobe de novo.
func (c *Chain) reorderRamp(b []float64, want *[]*slot) {
	for i := range b {
		if want != c.active {
			c.rgain -= c.rstep
			if c.rgain <= 0 {
				c.rgain = 0
				clear(b[i:])
				c.active = want // a nova ordem vale a partir do próximo bloco
				return
			}
		} else if c.rgain < 1 {
			c.rgain = math.Min(1, c.rgain+c.rstep)
		}
		b[i] *= c.rgain
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

// String descreve a cadeia na ordem atual, marcando o loop de efeitos e os módulos desligados:
//
//	Gate → Pré-amp → [loop: Delay → Reverb (off)] → Power amp → Gabinete
func (c *Chain) String() string {
	name := func(s *slot) string {
		n := s.m.Name()
		if p, ok := s.m.(*Pre); ok && p.Mode() == AmpNAM {
			n = "NAM (" + strings.TrimPrefix(p.NAMRef(), capturas.Prefix) + ")"
		}
		if s.enabled.Load() {
			return n
		}
		return n + " (off)"
	}
	var parts, loop []string
	inLoop := false
	for _, s := range *c.want.Load() {
		switch id := s.m.ID(); {
		case id == PreID:
			parts = append(parts, name(s))
			inLoop = true
		case id == PowerID:
			if len(loop) > 0 {
				parts = append(parts, "[loop: "+strings.Join(loop, " → ")+"]")
			}
			loop, inLoop = nil, false
			parts = append(parts, name(s))
		case inLoop:
			loop = append(loop, name(s))
		default:
			parts = append(parts, name(s))
		}
	}
	parts = append(parts, loop...) // só sobra algo se não houver power amp
	return strings.Join(parts, " → ")
}
