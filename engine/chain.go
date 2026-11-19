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
	mark    uint64 // usado na troca de ordem para achar os módulos que entram na cadeia

	lat     int
	gain    float64 // 0 = bypass total, 1 = efeito total
	step    float64
	dry     []float64
	dryLine *dsp.DelayLine // atrasa o sinal seco pela latência do módulo (crossfade alinhado)

	sp   Spiller // módulo com cauda (spillover), ou nil
	free []Tail  // instâncias reservas (thread de áudio)

	// estéreo (stereo.go)
	twin     Module          // instância do canal R (Twinner), ou nil
	sync     TwinSyncer      // acerta a gêmea a cada bloco, ou nil
	sp2      StereoProcessor // processa os dois canais, ou nil
	stereo   bool            // processado no trecho estéreo no último bloco
	dryR     []float64
	dryLineR *dsp.DelayLine
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
	gen    uint64

	fs       float64
	maxBlock int
	mono     []float64
	right    []float64
	prepared bool

	stereoWant atomic.Bool // saída estéreo pedida (GUI)
	stereo     bool        // modo em uso (thread de áudio)

	limWant atomic.Bool        // limitador de saída pedido (SetLimiter)
	cal     inputCal           // calibração de entrada dos modelos NAM (SetInputCalibration)
	lim     *dsp.SafetyLimiter // no fim da cadeia (criado em Prepare)

	// spillover (spill.go)
	noSpill                       atomic.Bool   // spillover desligado
	transReq                      atomic.Uint64 // trocas de preset pedidas (GUI)
	transSeen                     uint64        // última atendida (thread de áudio)
	groups                        []tailGroup
	nTails                        int
	tbuf, tbufR                   []float64
	cutStep, overlapStep, endStep float64
	maxTail                       int
}

// NewChain monta uma cadeia com os módulos na ordem dada (todos ligados).
func NewChain(mods ...Module) *Chain {
	c := &Chain{byID: make(map[string]*slot), rgain: 1}
	c.cal.set(false, DefaultInputLevelDBu)
	for _, m := range mods {
		if u, ok := m.(calUser); ok {
			u.useCal(&c.cal)
		}
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

// DefaultOrder é a ordem padrão: dinâmica, wah, oitavador e drive na frente do amp; EQ,
// modulações, pitch e ambiências no loop de efeitos. Um pedal adicionado entra na posição
// dele nesta ordem.
var DefaultOrder = []string{"gate", "comp", "wah", "octave", "synth", "drive", PreID, "eq", "chorus", "flanger", "phaser", "vibe", "vibrato", "tremolo", "rotary", "pitch", "delay", "reverb", PowerID, CabID}

// StartOrder é a cadeia com que o app abre: o gate e o amp (os outros pedais são
// adicionados pelo "+").
var StartOrder = []string{"gate", PreID, PowerID, CabID}

// NewDefaultChain monta a cadeia padrão (DefaultOrder). Gate e amp começam ligados;
// os outros pedais, desligados.
func NewDefaultChain() *Chain {
	pre, pow := NewPre(), NewPower()
	pow.FollowAmpMode(pre)
	c := NewChain(NewGate(), NewCompressor(), NewWah(), NewOctave(), NewSynth(), NewDrive(), pre, NewEQ(), NewChorus(), NewFlanger(),
		NewPhaser(), NewVibe(), NewVibrato(), NewTremolo(), NewRotary(), NewPitch(), NewDelay(), NewReverb(), pow, NewCab())
	for _, m := range c.AllModules() { // pedais começam desligados (o gate e o amp, ligados)
		id := m.ID()
		if id == "gate" || Required(id) {
			continue
		}
		c.SetEnabled(id, false)
	}
	return c
}

// Modules devolve os módulos que estão na cadeia, na ordem (a última pedida).
func (c *Chain) Modules() []Module {
	ord := *c.want.Load()
	out := make([]Module, len(ord))
	for i, s := range ord {
		out[i] = s.m
	}
	return out
}

// AllModules devolve todos os módulos, inclusive os que estão fora da cadeia (na ordem de criação).
func (c *Chain) AllModules() []Module {
	out := make([]Module, len(c.all))
	for i, s := range c.all {
		out[i] = s.m
	}
	return out
}

// Required informa se o módulo é obrigatório na cadeia (as três partes do amp).
func Required(id string) bool { return id == PreID || id == PowerID || id == CabID }

// InChain informa se o módulo está na cadeia.
func (c *Chain) InChain(id string) bool { return slices.Contains(c.Order(), id) }

// Available devolve os módulos fora da cadeia (que podem ser adicionados), na ordem de criação.
func (c *Chain) Available() []Module {
	ord := *c.want.Load()
	var out []Module
	for _, s := range c.all {
		if !slices.Contains(ord, s) {
			out = append(out, s.m)
		}
	}
	return out
}

// Add coloca um módulo na cadeia, ligado, na posição padrão: logo depois do módulo que o
// precede na ordem de criação (DefaultOrder) e está na cadeia. Seguro com o áudio rodando.
func (c *Chain) Add(id string) error {
	s := c.byID[id]
	if s == nil {
		return fmt.Errorf("módulo desconhecido: %q", id)
	}
	ids := c.Order()
	if slices.Contains(ids, id) {
		return c.SetEnabled(id, true)
	}
	s.enabled.Store(true)
	return c.SetOrder(insertDefault(ids, id, c.allIDs()))
}

// PadLatency põe na cadeia, desligados, os módulos com latência que estão fora dela (o drive,
// o synth): desligados, eles só atrasam o sinal seco, então o som não muda, e a latência da
// cadeia fica a máxima, a mesma com qualquer preset. Para os hosts em que a latência só pode
// mudar com o plugin parado (CLAP). Seguro com o áudio rodando (a troca de ordem tem a rampa
// de sempre); antes do Prepare, a cadeia já começa com eles.
func (c *Chain) PadLatency() error {
	ids := c.Order()
	n := len(ids)
	for _, s := range c.all {
		if s.m.Latency() == 0 || slices.Contains(ids, s.m.ID()) {
			continue
		}
		s.enabled.Store(false)
		ids = insertDefault(ids, s.m.ID(), c.allIDs())
	}
	if len(ids) == n {
		return nil
	}
	return c.SetOrder(ids)
}

// insertDefault insere id em ids logo depois do módulo que o precede em ref e está em ids.
func insertDefault(ids []string, id string, ref []string) []string {
	pos := 0
	if i := slices.Index(ref, id); i > 0 {
		for j := i - 1; j >= 0; j-- {
			if k := slices.Index(ids, ref[j]); k >= 0 {
				pos = k + 1
				break
			}
		}
	}
	return slices.Insert(slices.Clone(ids), pos, id)
}

// Remove tira um módulo da cadeia (as partes do amp não saem). Seguro com o áudio rodando.
func (c *Chain) Remove(id string) error {
	if c.byID[id] == nil {
		return fmt.Errorf("módulo desconhecido: %q", id)
	}
	if Required(id) {
		return fmt.Errorf("%q faz parte do amp e não sai da cadeia", id)
	}
	ids := c.Order()
	i := slices.Index(ids, id)
	if i < 0 {
		return nil
	}
	return c.SetOrder(slices.Delete(ids, i, i+1))
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

// Drive devolve o pedal de drive, se houver (mesmo fora da cadeia).
func (c *Chain) Drive() *Drive { m, _ := c.Module("drive").(*Drive); return m }

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

// SetOrder define a cadeia: os módulos presentes, na ordem. Cada um aparece uma vez; pre,
// power e cab são obrigatórios, nessa ordem. Os módulos de fora não são processados. Seguro com o áudio rodando: a troca
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
	for _, id := range []string{PreID, PowerID, CabID} {
		if c.byID[id] != nil && !seen[id] {
			return nil, fmt.Errorf("a cadeia precisa ter %q (parte do amp)", id)
		}
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
	c.right = make([]float64, maxBlock)
	for _, s := range c.all {
		s.m.Prepare(fs, maxBlock)
		s.lat = s.m.Latency()
		s.step = 1 / (BypassFadeMs / 1000 * fs)
		s.dry = make([]float64, maxBlock)
		s.dryR = make([]float64, maxBlock)
		s.dryLine, s.dryLineR = nil, nil
		if s.lat > 0 {
			s.dryLine = dsp.NewDelayLine(s.lat)
			s.dryLineR = dsp.NewDelayLine(s.lat)
		}
		s.twin, s.sync, s.sp2, s.stereo = nil, nil, nil, false
		if tw, ok := s.m.(Twinner); ok {
			s.twin = tw.Twin()
			s.twin.Prepare(fs, maxBlock)
			s.sync, _ = s.m.(TwinSyncer)
		}
		s.sp2, _ = s.m.(StereoProcessor)
		if s.enabled.Load() {
			s.gain = 1
		} else {
			s.gain = 0
		}
	}
	c.active = c.want.Load()
	c.rgain = 1
	c.rstep = 1 / (ReorderFadeMs / 1000 * fs)
	c.lim = dsp.NewSafetyLimiter(fs)
	c.lim.SetOn(c.limWant.Load())
	c.stereo = c.stereoWant.Load()
	c.prepareTails()
	c.prepared = true
}

// SampleRate devolve a taxa preparada.
func (c *Chain) SampleRate() float64 { return c.fs }

// MaxBlock devolve o maior bloco aceito por chamada de ProcessMono.
func (c *Chain) MaxBlock() int { return c.maxBlock }

// Reset limpa o estado de todos os módulos (caudas de delay/reverb etc.).
func (c *Chain) Reset() {
	c.releaseTails()
	for _, s := range c.all {
		s.reset()
	}
}

// Latency devolve a latência total em amostras dos módulos na cadeia. Ligar e desligar
// não muda (módulos em bypass continuam atrasando o sinal seco), nem a ordem; só
// adicionar ou remover um módulo com latência (ex.: o drive, com oversampling).
func (c *Chain) Latency() int {
	total := 0
	for _, s := range *c.want.Load() {
		total += s.m.Latency()
	}
	return total
}

// SetLimiter liga ou desliga o limitador de segurança no fim da cadeia (dsp.SafetyLimiter:
// teto de −0,3 dBFS, sem latência, L e R juntos). Começa desligado: o app liga (ajuste do
// usuário); as medições offline e o ampcli medem o som sem ele. Seguro com o áudio rodando.
func (c *Chain) SetLimiter(on bool) { c.limWant.Store(on) }

// Limiter informa se o limitador de saída está ligado.
func (c *Chain) Limiter() bool { return c.limWant.Load() }

// LimiterReduction devolve a maior redução do limitador (dB, ≥ 0) desde a chamada anterior.
// Só na thread de áudio (entre os blocos).
func (c *Chain) LimiterReduction() float64 {
	if c.lim == nil {
		return 0
	}
	return c.lim.Reduction()
}

// ProcessMono processa um bloco mono in-place, de qualquer tamanho. A cadeia toda roda em
// mono, mesmo com o estéreo ligado (para a saída estéreo, ProcessStereo).
func (c *Chain) ProcessMono(buf []float64) { c.run(buf, nil) }

// ProcessStereo processa um bloco: l é a entrada (mono) e vira o canal esquerdo; r recebe o
// direito (o conteúdo de entrada é ignorado). Com o estéreo desligado, r = l.
func (c *Chain) ProcessStereo(l, r []float64) { c.run(l, r[:len(l)]) }

// splitIndex é onde a cadeia passa a ser estéreo: logo depois do pré-amp (a entrada de um
// amp é mono), ou desde o início, numa cadeia sem pré-amp.
func splitIndex(act []*slot) int {
	for i, s := range act {
		if s.m.ID() == PreID {
			return i + 1
		}
	}
	return 0
}

// run processa l (e r, se não for nil: saída estéreo).
func (c *Chain) run(l, r []float64) {
	for len(l) > 0 {
		n := min(len(l), c.maxBlock)
		b := l[:n]
		var br []float64
		if r != nil {
			br = r[:n]
		}
		if req := c.transReq.Load(); req != c.transSeen {
			c.transSeen = req
			c.spill()
		}
		st := c.stereo && br != nil
		act := *c.active
		split := len(act)
		if st {
			split = splitIndex(act)
		}
		for i, s := range act {
			if i == split {
				copy(br, b) // daqui em diante, dois canais
			}
			var sr []float64
			if i >= split {
				sr = br
			}
			if c.nTails > 0 {
				c.mixTails(b, sr, s)
			}
			s.process(b, sr)
		}
		var er []float64
		if st {
			if split == len(act) {
				copy(br, b)
			}
			er = br
		}
		if c.nTails > 0 {
			c.mixTails(b, er, nil)
		}
		if br != nil && !st {
			copy(br, b)
		}
		if want := c.want.Load(); want != c.active || c.rgain < 1 || c.stereoWant.Load() != c.stereo {
			c.reorderRamp(b, br, want)
		}
		if c.lim != nil {
			c.lim.SetOn(c.limWant.Load())
			c.lim.Process(b, br)
		}
		l = l[n:]
		if r != nil {
			r = r[n:]
		}
	}
}

// reorderRamp desce o volume até zero com a ordem antiga, troca a ordem (e o modo
// mono/estéreo) e sobe de novo. br pode ser nil.
func (c *Chain) reorderRamp(b, br []float64, want *[]*slot) {
	stw := c.stereoWant.Load()
	for i := range b {
		if want != c.active || stw != c.stereo {
			c.rgain -= c.rstep
			if c.rgain <= 0 {
				c.rgain = 0
				clear(b[i:])
				if br != nil {
					clear(br[i:])
				}
				c.enterOrder(want) // a nova ordem vale a partir do próximo bloco
				if stw != c.stereo {
					c.stereo = stw
					c.releaseTails() // as caudas eram do outro modo
				}
				return
			}
		} else if c.rgain < 1 {
			c.rgain = math.Min(1, c.rgain+c.rstep)
		}
		b[i] *= c.rgain
		if br != nil {
			br[i] *= c.rgain
		}
	}
}

// enterOrder troca para a nova ordem; os módulos que entram na cadeia começam limpos
// (sem o resto de quando saíram). Não aloca.
func (c *Chain) enterOrder(want *[]*slot) {
	c.gen++
	for _, s := range *c.active {
		s.mark = c.gen
	}
	for _, s := range *want {
		if s.mark != c.gen {
			s.reset()
		}
	}
	c.active = want
}

// Process é a interface multicanal (float32) usada pelo plugin: mistura as entradas em mono e
// processa; com duas saídas ou mais, a 1ª recebe o L e a 2ª o R (as demais, o L).
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
		r := m
		if len(out) >= 2 {
			r = c.right[:k]
			c.ProcessStereo(m, r)
		} else {
			c.ProcessMono(m)
		}
		for j, ch := range out {
			src := m
			if j == 1 {
				src = r
			}
			for i, v := range src {
				ch[off+i] = float32(v)
			}
		}
	}
}

// reset limpa o módulo (e a instância do canal R) e o atraso do seco.
func (s *slot) reset() {
	s.m.Reset()
	if s.twin != nil {
		s.twin.Reset()
	}
	if s.dryLine != nil {
		s.dryLine.Reset()
		s.dryLineR.Reset()
	}
}

// run processa o módulo em mono (r == nil) ou em estéreo: em dois canais de verdade
// (StereoProcessor), com a instância gêmea no R (Twinner) ou, nos módulos só mono, somando
// os canais.
func (s *slot) run(l, r []float64) {
	switch {
	case r == nil:
		s.m.Process(l)
	case s.sp2 != nil:
		s.sp2.ProcessStereo(l, r)
	case s.twin != nil:
		if s.sync != nil {
			s.sync.SyncTwin(s.twin, len(l))
		}
		s.m.Process(l)
		s.twin.Process(r)
	default:
		for i := range l {
			l[i] = 0.5 * (l[i] + r[i])
		}
		s.m.Process(l)
		copy(r, l)
	}
}

// process processa o slot (r == nil: mono), com o crossfade de bypass.
func (s *slot) process(l, r []float64) {
	if st := r != nil; st != s.stereo { // mudou de trecho (mono ↔ estéreo): começa limpo
		s.stereo = st
		s.reset()
	}
	target := 0.0
	if s.enabled.Load() {
		target = 1
	}

	// Bypass total: só mantém o atraso do sinal seco.
	if s.gain == 0 && target == 0 {
		if s.dryLine != nil {
			delayDry(l, s.dryLine, s.lat)
			if r != nil {
				delayDry(r, s.dryLineR, s.lat)
			}
		}
		return
	}
	if s.gain == 0 {
		s.reset() // pedal sendo ligado: começa sem resto de estado antigo
	}

	dry := s.dry[:len(l)]
	copy(dry, l)
	var dryR []float64
	if r != nil {
		dryR = s.dryR[:len(r)]
		copy(dryR, r)
	}
	s.run(l, r)

	if s.gain == 1 && target == 1 {
		if s.dryLine != nil {
			for _, x := range dry {
				s.dryLine.Write(x)
			}
			for _, x := range dryR {
				s.dryLineR.Write(x)
			}
		}
		return
	}
	// Crossfade entre efeito e seco (seco atrasado pela latência do módulo).
	g := s.gain
	s.gain = fadeDry(l, dry, s.dryLine, s.lat, g, target, s.step)
	if r != nil {
		fadeDry(r, dryR, s.dryLineR, s.lat, g, target, s.step)
	}
}

func delayDry(buf []float64, line *dsp.DelayLine, lat int) {
	for i, x := range buf {
		buf[i] = line.ReadInt(lat)
		line.Write(x)
	}
}

// fadeDry mistura buf (efeito) com dry (atrasado por line, se houver) com o ganho indo de g
// até target; devolve o ganho final.
func fadeDry(buf, dry []float64, line *dsp.DelayLine, lat int, g, target, step float64) float64 {
	for i, x := range dry {
		d := x
		if line != nil {
			d = line.ReadInt(lat)
			line.Write(x)
		}
		if g < target {
			g = math.Min(target, g+step)
		} else if g > target {
			g = math.Max(target, g-step)
		}
		buf[i] = g*buf[i] + (1-g)*d
	}
	return g
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
