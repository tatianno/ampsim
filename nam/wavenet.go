package nam

// Execução do WaveNet em streaming, em blocos, em float32 (como a NAM core).
//
// Cada convolução com kernel > 1 guarda o passado da sua entrada num buffer linear
// ("ring") por canal: o bloco novo é escrito depois do passado e cada tap do kernel lê
// uma fatia contígua deslocada pela dilatação. Quando o buffer enche, as últimas
// `lookback` amostras são copiadas para o início. Process não aloca.

// mat é um sinal multicanal: rows linhas (canais) de stride amostras, contíguas.
type mat struct {
	rows, stride int
	d            []float32
}

func newMat(rows, stride int) mat { return mat{rows, stride, make([]float32, rows*stride)} }

func (m mat) row(i int) []float32 { return m.d[i*m.stride : (i+1)*m.stride] }

// ring guarda o passado da entrada de uma convolução causal.
type ring struct {
	ch, size, look, pos int
	data                []float32 // ch × size, fatia da arena da Net
}

func (r *ring) row(c int) []float32 { return r.data[c*r.size : (c+1)*r.size] }

// write acrescenta n amostras de x e devolve a posição onde o bloco começa.
func (r *ring) write(x mat, n int) int {
	if r.pos+n > r.size {
		for c := range r.ch {
			row := r.row(c)
			copy(row[:r.look], row[r.pos-r.look:r.pos])
		}
		r.pos = r.look
	}
	for c := range r.ch {
		copy(r.row(c)[r.pos:r.pos+n], x.row(c)[:n])
	}
	base := r.pos
	r.pos += n
	return base
}

func fill(dst []float32, v float32) {
	for i := range dst {
		dst[i] = v
	}
}

// startRow põe o bias (ou zero) na linha o de y.
func (c *conv) startRow(y []float32, o int) {
	if c.bias != nil {
		fill(y, c.bias[o])
	} else {
		clear(y)
	}
}

// run1x1 faz y = W·x (+ bias), ou y += W·x (+ bias) se acc.
func (c *conv) run1x1(x mat, n int, y mat, acc bool) {
	for o := range c.out {
		dst := y.row(o)[:n]
		if !acc {
			c.startRow(dst, o)
		} else if c.bias != nil {
			b := c.bias[o]
			for f := range dst {
				dst[f] += b
			}
		}
		convRows(dst, c.w[o*c.in:(o+1)*c.in], x.d, x.stride, c.in, 1, 0, 0)
	}
}

// runRing faz y = conv(x) a partir do passado guardado em r (o bloco atual começa em base).
func (c *conv) runRing(r *ring, base, n int, y mat) {
	total := c.k * c.in
	for o := range c.out {
		dst := y.row(o)[:n]
		c.startRow(dst, o)
		convRows(dst, c.w[o*total:(o+1)*total], r.data, r.size, c.in, c.k, c.dil, base)
	}
}

type layerState struct {
	spec *layerSpec
	ring ring
	z    mat // bottleneck (×2 com gating)
	tmp  mat // pré-ativação (blending) e saída do head1x1
}

type arrayState struct {
	spec     *arraySpec
	layers   []layerState
	a, b     mat // entrada/saída das camadas (alternam)
	out      mat // saída da última camada (aponta para a ou b)
	headAcc  mat
	headRing ring
	headOut  mat
}

type headState struct {
	spec  *headSpec
	work  mat
	rings []ring
	outs  []mat
}

// Net é uma instância executável de um WaveNet (com estado). Não é segura para uso
// concorrente; cada thread de áudio usa a sua.
type Net struct {
	spec     *Spec
	maxBlock int
	cond     mat
	arrays   []arrayState
	head     *headState

	rings []*ring
	arena []float32 // estado de todas as convoluções
	warm  []float32 // estado depois do prewarm (nil = ainda não aquecido)
	warmP []int
}

// NewNet cria uma instância para blocos de até maxBlock amostras, com estado zerado
// (sem prewarm). Aloca.
func NewNet(s *Spec, maxBlock int) *Net {
	if maxBlock < 1 {
		maxBlock = 1
	}
	n := &Net{spec: s, maxBlock: maxBlock, cond: newMat(1, maxBlock)}
	slack := 4 * maxBlock
	var rings []*ring
	addRing := func(r *ring, ch, look int) {
		*r = ring{ch: ch, look: look, size: look + slack, pos: look}
		rings = append(rings, r)
	}
	n.arrays = make([]arrayState, len(s.arrays))
	for i := range s.arrays {
		as := &s.arrays[i]
		st := &n.arrays[i]
		st.spec = as
		st.a, st.b = newMat(as.channels, maxBlock), newMat(as.channels, maxBlock)
		st.headAcc = newMat(as.headIn, maxBlock)
		st.headOut = newMat(as.headRechannel.out, maxBlock)
		st.layers = make([]layerState, len(as.layers))
		for j := range as.layers {
			ls := &as.layers[j]
			l := &st.layers[j]
			l.spec = ls
			l.z = newMat(ls.conv.out, maxBlock)
			t := as.bottleneck
			if ls.head1x1 != nil {
				t = max(t, ls.head1x1.out)
			}
			l.tmp = newMat(t, maxBlock)
		}
	}
	if s.head != nil {
		h := &headState{spec: s.head, work: newMat(s.head.convs[0].in, maxBlock)}
		h.rings = make([]ring, len(s.head.convs))
		for _, c := range s.head.convs {
			h.outs = append(h.outs, newMat(c.out, maxBlock))
		}
		n.head = h
	}
	// Os rings vêm depois para os ponteiros ficarem estáveis.
	for i := range n.arrays {
		st := &n.arrays[i]
		for j := range st.layers {
			addRing(&st.layers[j].ring, st.spec.channels, st.spec.layers[j].conv.lookback())
		}
		addRing(&st.headRing, st.spec.headIn, st.spec.headRechannel.lookback())
	}
	if n.head != nil {
		for i, c := range s.head.convs {
			addRing(&n.head.rings[i], c.in, c.lookback())
		}
	}
	total := 0
	for _, r := range rings {
		total += r.ch * r.size
	}
	n.arena = make([]float32, total)
	off := 0
	for _, r := range rings {
		r.data = n.arena[off : off+r.ch*r.size]
		off += r.ch * r.size
	}
	n.rings = rings
	return n
}

// Spec devolve a rede que esta instância executa.
func (n *Net) Spec() *Spec { return n.spec }

// MaxBlock devolve o maior bloco aceito por chamada interna (Process divide blocos maiores).
func (n *Net) MaxBlock() int { return n.maxBlock }

// Prewarm leva a rede ao estado de repouso (entrada em silêncio por todo o campo receptivo),
// como a NAM core faz ao carregar, e guarda esse estado para o Reset. Sem isso, o começo
// do som tem um transiente: os bias das camadas "enchem" os buffers. Aloca na primeira vez.
func (n *Net) Prewarm() {
	n.zero()
	zeros := make([]float32, n.maxBlock)
	for done := 0; done < n.spec.ReceptiveField(); done += n.maxBlock {
		n.Process(zeros, zeros)
		clear(zeros)
	}
	if n.warm == nil {
		n.warm = make([]float32, len(n.arena))
		n.warmP = make([]int, len(n.rings))
	}
	copy(n.warm, n.arena)
	for i, r := range n.rings {
		n.warmP[i] = r.pos
	}
}

// Reset volta ao estado depois do prewarm (ou zera, se Prewarm nunca foi chamado). Não aloca.
func (n *Net) Reset() {
	if n.warm == nil {
		n.zero()
		return
	}
	copy(n.arena, n.warm)
	for i, r := range n.rings {
		r.pos = n.warmP[i]
	}
}

func (n *Net) zero() {
	clear(n.arena)
	for _, r := range n.rings {
		r.pos = r.look
	}
}

// Process processa in e grava em out (podem ser o mesmo slice; len(out) >= len(in)).
// Aceita qualquer tamanho; blocos maiores que MaxBlock são divididos. Não aloca.
func (n *Net) Process(in, out []float32) {
	for len(in) > 0 {
		k := min(len(in), n.maxBlock)
		n.block(in[:k], out[:k])
		in, out = in[k:], out[k:]
	}
}

func (n *Net) block(in, out []float32) {
	k := len(in)
	copy(n.cond.row(0)[:k], in)
	var prev *arrayState
	for i := range n.arrays {
		st := &n.arrays[i]
		if prev == nil {
			st.process(n.cond, n.cond, k, nil)
		} else {
			st.process(prev.out, n.cond, k, &prev.headOut)
		}
		prev = st
	}
	final := prev.headOut.row(0)[:k]
	hs := n.spec.headScale
	if n.head == nil {
		for i, v := range final {
			out[i] = hs * v
		}
		return
	}
	h := n.head
	for c := range prev.headOut.rows {
		src, dst := prev.headOut.row(c)[:k], h.work.row(c)[:k]
		for i, v := range src {
			dst[i] = hs * v
		}
	}
	x := h.work
	for i := range h.spec.convs {
		cv := &h.spec.convs[i]
		for c := range cv.in {
			h.spec.act.apply(x.row(c)[:k], c)
		}
		base := h.rings[i].write(x, k)
		cv.runRing(&h.rings[i], base, k, h.outs[i])
		x = h.outs[i]
	}
	copy(out, x.row(0)[:k])
}

// process roda a pilha: x é a entrada (input_size canais), cond a condição, headIn a cabeça
// acumulada da pilha anterior (nil na primeira).
func (st *arrayState) process(x, cond mat, n int, headIn *mat) {
	s := st.spec
	s.rechannel.run1x1(x, n, st.a, false)
	if headIn == nil {
		for c := range st.headAcc.rows {
			clear(st.headAcc.row(c)[:n])
		}
	} else {
		for c := range st.headAcc.rows {
			copy(st.headAcc.row(c)[:n], headIn.row(c)[:n])
		}
	}
	cur, nxt := st.a, st.b
	for i := range st.layers {
		st.layers[i].process(cur, cond, n, nxt, st.headAcc, s.bottleneck)
		cur, nxt = nxt, cur
	}
	st.out = cur
	base := st.headRing.write(st.headAcc, n)
	s.headRechannel.runRing(&st.headRing, base, n, st.headOut)
}

func (l *layerState) process(x, cond mat, n int, out, headAcc mat, bn int) {
	s := l.spec
	base := l.ring.write(x, n)
	s.conv.runRing(&l.ring, base, n, l.z)
	s.mixin.run1x1(cond, n, l.z, true)

	z := l.z
	switch s.gating {
	case gateNone:
		for c := range bn {
			s.act.apply(z.row(c)[:n], c)
		}
	case gateGated:
		for c := range bn {
			a, g := z.row(c)[:n], z.row(bn + c)[:n]
			s.act.apply(a, c)
			s.act2.apply(g, c)
			for f := range a {
				a[f] *= g[f]
			}
		}
	case gateBlended:
		for c := range bn {
			a, g, pre := z.row(c)[:n], z.row(bn + c)[:n], l.tmp.row(c)[:n]
			copy(pre, a)
			s.act.apply(a, c)
			s.act2.apply(g, c)
			for f := range a {
				a[f] = g[f]*a[f] + (1-g[f])*pre[f]
			}
		}
	}
	// a = z (só as bn primeiras linhas)

	if s.head1x1 != nil {
		s.head1x1.run1x1(z, n, l.tmp, false)
		for c := range s.head1x1.out {
			axpy(headAcc.row(c)[:n], 1, l.tmp.row(c)[:n])
		}
	} else {
		for c := range bn {
			axpy(headAcc.row(c)[:n], 1, z.row(c)[:n])
		}
	}

	if s.layer1x1 != nil {
		s.layer1x1.run1x1(z, n, out, false)
		for c := range out.rows {
			axpy(out.row(c)[:n], 1, x.row(c)[:n])
		}
	} else {
		for c := range out.rows {
			copy(out.row(c)[:n], x.row(c)[:n])
		}
	}
}
