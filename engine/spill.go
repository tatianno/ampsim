package engine

import "math"

// Spillover: na troca de preset, as repetições do delay e a cauda do reverb do som anterior
// continuam soando enquanto o som novo já toca, cada uma com os ajustes do preset de onde veio.
//
// Cada módulo com cauda (Spiller) tem instâncias reservas, criadas no Prepare. Na troca, a
// thread de áudio passa a instância que estava tocando para uma cauda (que recebe só silêncio
// e mantém os parâmetros antigos) e o módulo segue com uma reserva limpa, já com os
// parâmetros novos. As caudas vizinhas (ex.: delay → reverb no loop) formam um grupo e
// continuam ligadas entre si, como na cadeia antiga; o grupo soma na entrada da próxima parte
// do amp que vinha depois dele (o power amp, no loop) e, assim, ainda passa pelo amp e pelo
// gabinete. Quando a cauda se extingue, as instâncias voltam a ser reservas.
//
// Com o spillover desligado o mecanismo é o mesmo, mas a cauda some num fade curto: a troca
// fica limpa, sem o som anterior embolando no novo.

// Tail é uma instância de efeito tocando a cauda do som anterior.
type Tail interface {
	ProcessBlock(buf []float64)
	ProcessStereo(l, r []float64)
	Reset()
	Silent() bool
}

// Spiller é implementado pelos módulos com cauda (delay e reverb).
type Spiller interface {
	// NewTail cria uma instância reserva (chamado no Prepare, depois do Prepare do módulo; aloca).
	NewTail() Tail
	// SwapTail passa o módulo a usar fresh, zerada (o próximo ajuste dos parâmetros vale na
	// hora), e devolve a instância que estava tocando. Thread de áudio; não aloca.
	SwapTail(fresh Tail) Tail
}

const (
	// SpillSpares é quantas instâncias reservas cada módulo com cauda tem: a cauda da troca
	// atual e a da anterior, que ainda some num fade.
	SpillSpares = 2
	// SpillCutMs é o fade da cauda com o spillover desligado.
	SpillCutMs = 30.0
	// SpillOverlapMs é o fade das caudas mais antigas quando há uma nova troca de preset.
	SpillOverlapMs = 150.0
	// SpillMaxMs é quanto uma cauda dura no máximo (um delay com feedback alto quase não
	// acaba); passado esse tempo ela some em SpillEndMs.
	SpillMaxMs = 10000.0
	SpillEndMs = 1000.0
)

const maxTailGroups = 8

type tailPart struct {
	owner *slot
	t     Tail
}

// tailGroup é uma sequência de caudas da cadeia antiga, processadas em série.
type tailGroup struct {
	on     bool
	gen    uint64 // troca de preset que criou o grupo
	anchor *slot  // soma na entrada deste módulo (uma parte do amp); nil = fim da cadeia
	parts  []tailPart
	gain   float64
	step   float64 // > 0: fade em andamento (quanto o ganho desce por amostra)
	age    int
}

func (g *tailGroup) fadeOut(step float64) {
	if g.step == 0 || step > g.step {
		g.step = step
	}
}

// SetSpillover liga ou desliga o spillover (padrão: ligado). Seguro com o áudio rodando.
func (c *Chain) SetSpillover(on bool) { c.noSpill.Store(!on) }

// Spillover informa se o spillover está ligado.
func (c *Chain) Spillover() bool { return !c.noSpill.Load() }

// Tails devolve quantos grupos de cauda estão soando (para testes e medidores).
func (c *Chain) Tails() int { return c.nTails }

// beginTransition avisa a thread de áudio de que um novo som vai ser aplicado: no próximo
// bloco, as caudas atuais viram spillover. Chamado antes de mudar os parâmetros.
func (c *Chain) beginTransition() { c.transReq.Add(1) }

// prepareTails aloca as reservas e os grupos (no Prepare).
func (c *Chain) prepareTails() {
	n := 0
	for _, s := range c.all {
		s.sp, s.free = nil, nil
		if sp, ok := s.m.(Spiller); ok {
			s.sp = sp
			s.free = make([]Tail, 0, SpillSpares)
			for range SpillSpares {
				s.free = append(s.free, sp.NewTail())
			}
			n++
		}
	}
	c.groups = make([]tailGroup, maxTailGroups)
	for i := range c.groups {
		c.groups[i].parts = make([]tailPart, 0, n)
	}
	c.nTails = 0
	c.tbuf = make([]float64, c.maxBlock)
	c.tbufR = make([]float64, c.maxBlock)
	ms := func(v float64) float64 { return 1 / (v / 1000 * c.fs) }
	c.cutStep, c.overlapStep, c.endStep = ms(SpillCutMs), ms(SpillOverlapMs), ms(SpillEndMs)
	c.maxTail = int(SpillMaxMs / 1000 * c.fs)
	c.transSeen = c.transReq.Load()
}

// releaseGroup devolve as instâncias do grupo às reservas.
func (c *Chain) releaseGroup(g *tailGroup) {
	for _, p := range g.parts {
		p.owner.free = append(p.owner.free, p.t)
	}
	clear(g.parts)
	g.parts = g.parts[:0]
	g.on = false
	c.nTails--
}

// releaseTails desliga todas as caudas (Reset da cadeia).
func (c *Chain) releaseTails() {
	for i := range c.groups {
		if c.groups[i].on {
			c.releaseGroup(&c.groups[i])
		}
	}
}

// takeFree pega uma reserva de s. Sem reserva (trocas muito seguidas), toma a instância da
// cauda mais baixa de s, que já estava sumindo.
func (c *Chain) takeFree(s *slot) Tail {
	if n := len(s.free); n > 0 {
		t := s.free[n-1]
		s.free[n-1] = nil
		s.free = s.free[:n-1]
		return t
	}
	var best *tailGroup
	bi := -1
	for i := range c.groups {
		g := &c.groups[i]
		if !g.on || g.gen == c.transSeen {
			continue
		}
		for k, p := range g.parts {
			if p.owner == s && (best == nil || g.gain < best.gain) {
				best, bi = g, k
			}
		}
	}
	if best == nil {
		return nil
	}
	t := best.parts[bi].t
	copy(best.parts[bi:], best.parts[bi+1:])
	best.parts[len(best.parts)-1] = tailPart{}
	best.parts = best.parts[:len(best.parts)-1]
	if len(best.parts) == 0 {
		best.on = false
		c.nTails--
	}
	return t
}

// newGroup acha um grupo livre (ou recupera o mais baixo de uma troca anterior).
func (c *Chain) newGroup(anchor *slot) *tailGroup {
	var g *tailGroup
	for i := range c.groups {
		if !c.groups[i].on {
			g = &c.groups[i]
			break
		}
	}
	if g == nil {
		for i := range c.groups {
			h := &c.groups[i]
			if h.gen != c.transSeen && (g == nil || h.gain < g.gain) {
				g = h
			}
		}
		if g == nil {
			return nil
		}
		c.releaseGroup(g)
	}
	*g = tailGroup{on: true, gen: c.transSeen, anchor: anchor, parts: g.parts[:0], gain: 1}
	if c.noSpill.Load() {
		g.step = c.cutStep
	}
	c.nTails++
	return g
}

// spill transforma as caudas da cadeia atual em spillover (thread de áudio, início de bloco).
func (c *Chain) spill() {
	for i := range c.groups { // as caudas de trocas anteriores dão lugar às novas
		if c.groups[i].on {
			c.groups[i].fadeOut(c.overlapStep)
		}
	}
	act := *c.active
	var g *tailGroup
	for i, s := range act {
		if s.sp == nil || s.gain == 0 {
			continue
		}
		anchor := (*slot)(nil)
		for _, n := range act[i+1:] {
			if Required(n.m.ID()) {
				anchor = n
				break
			}
		}
		fresh := c.takeFree(s)
		if fresh == nil {
			continue
		}
		if g == nil || g.anchor != anchor {
			if g = c.newGroup(anchor); g == nil {
				s.free = append(s.free, fresh) // sem grupo: a cauda segue no próprio módulo
				continue
			}
		}
		g.parts = append(g.parts, tailPart{owner: s, t: s.sp.SwapTail(fresh)})
	}
}

// mixTails soma em b (e br, no trecho estéreo) as caudas que entram antes de anchor (nil =
// fim da cadeia).
func (c *Chain) mixTails(b, br []float64, anchor *slot) {
	for i := range c.groups {
		g := &c.groups[i]
		if !g.on || g.anchor != anchor {
			continue
		}
		tb := c.tbuf[:len(b)]
		clear(tb)
		var tr []float64
		if br != nil {
			tr = c.tbufR[:len(b)]
			clear(tr)
		}
		silent := true
		for _, p := range g.parts {
			if tr != nil {
				p.t.ProcessStereo(tb, tr)
			} else {
				p.t.ProcessBlock(tb)
			}
			silent = silent && p.t.Silent()
		}
		for k, v := range tb {
			if g.step > 0 {
				g.gain = math.Max(0, g.gain-g.step)
			}
			b[k] += g.gain * v
			if tr != nil {
				br[k] += g.gain * tr[k]
			}
		}
		g.age += len(b)
		switch {
		case silent || g.gain == 0:
			c.releaseGroup(g)
		case g.age >= c.maxTail:
			g.fadeOut(c.endStep)
		}
	}
}
