package nam

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// gating diz como a camada usa os canais dobrados da convolução.
type gating int

const (
	gateNone    gating = iota
	gateGated          // ativação(a) · secundária(b)
	gateBlended        // α·ativação(a) + (1-α)·a, com α = secundária(b)
)

// conv guarda os pesos de uma convolução causal: w[out][k][in] (k = 0 é a amostra mais
// antiga, k = K-1 a atual) e o bias (nil se não houver). Uma 1×1 tem K = 1.
type conv struct {
	in, out, k, dil int
	w               []float32
	bias            []float32
}

func (c *conv) at(k, o, i int) float32 { return c.w[(o*c.k+k)*c.in+i] }

// lookback é quantas amostras de passado a convolução lê.
func (c *conv) lookback() int { return c.dil * (c.k - 1) }

// layerSpec é uma camada do WaveNet:
//
//	z = conv_dilatada(x) + mixin(condição) → ativação (com gating opcional) → a
//	cabeça += head1x1(a) (ou a);   próxima camada = x + layer1x1(a) (ou só x)
type layerSpec struct {
	conv     conv  // channels → bottleneck (×2 com gating), com bias
	mixin    conv  // condição → bottleneck (×2), sem bias
	layer1x1 *conv // bottleneck → channels, com bias (opcional)
	head1x1  *conv // bottleneck → head1x1.out_channels, com bias (opcional)
	act      activation
	act2     activation // secundária (gating/blending)
	gating   gating
}

// arraySpec é uma pilha de camadas com os mesmos canais.
type arraySpec struct {
	inputSize, condSize, channels, bottleneck int
	headIn                                    int  // canais que cada camada soma na cabeça
	rechannel                                 conv // input_size → channels, 1×1 sem bias
	layers                                    []layerSpec
	headRechannel                             conv // headIn → head_size, causal (kernel ≥ 1)
}

// headSpec é a cabeça opcional depois das pilhas: (ativação → conv) repetidos.
type headSpec struct {
	convs []conv
	act   activation
}

// Spec é um WaveNet já validado, com os pesos distribuídos. É imutável: várias instâncias
// de Net podem compartilhar o mesmo Spec.
type Spec struct {
	Channels  int // canais da primeira pilha (3 no "lite" A2, 16 no standard A1...)
	Layers    int // total de camadas
	Weights   int
	arrays    []arraySpec
	head      *headSpec
	headScale float32
}

// ReceptiveField é quantas amostras de entrada influenciam cada amostra de saída.
func (s *Spec) ReceptiveField() int {
	rf := 1
	for _, a := range s.arrays {
		for _, l := range a.layers {
			rf += l.conv.lookback()
		}
		rf += a.headRechannel.lookback()
	}
	if s.head != nil {
		for _, c := range s.head.convs {
			rf += c.lookback()
		}
	}
	return rf
}

// MACs é o número aproximado de multiplicações por amostra (para estimar a CPU).
func (s *Spec) MACs() int {
	n := 0
	cost := func(c *conv) {
		if c != nil {
			n += c.in * c.out * c.k
		}
	}
	for i := range s.arrays {
		a := &s.arrays[i]
		cost(&a.rechannel)
		for j := range a.layers {
			l := &a.layers[j]
			cost(&l.conv)
			cost(&l.mixin)
			cost(l.layer1x1)
			cost(l.head1x1)
		}
		cost(&a.headRechannel)
	}
	if s.head != nil {
		for i := range s.head.convs {
			cost(&s.head.convs[i])
		}
	}
	return n
}

// ---------------------------------------------------------------- JSON

type toggleJSON struct {
	Active      *bool `json:"active"`
	Groups      *int  `json:"groups"`
	OutChannels *int  `json:"out_channels"`
}

type layerArrayJSON struct {
	InputSize           int             `json:"input_size"`
	ConditionSize       int             `json:"condition_size"`
	Channels            int             `json:"channels"`
	Bottleneck          *int            `json:"bottleneck"`
	KernelSize          *int            `json:"kernel_size"`
	KernelSizes         []int           `json:"kernel_sizes"`
	Dilations           []int           `json:"dilations"`
	Activation          json.RawMessage `json:"activation"`
	Gated               *bool           `json:"gated"`
	GatingMode          json.RawMessage `json:"gating_mode"`
	SecondaryActivation json.RawMessage `json:"secondary_activation"`
	Head                *struct {
		OutChannels  int  `json:"out_channels"`
		KernelSize   int  `json:"kernel_size"`
		Bias         bool `json:"bias"`
		HeadDilation *int `json:"head_dilation"`
	} `json:"head"`
	HeadSize         *int            `json:"head_size"`
	HeadBias         *bool           `json:"head_bias"`
	Layer1x1         *toggleJSON     `json:"layer1x1"`
	Head1x1          *toggleJSON     `json:"head1x1"`
	GroupsInput      *int            `json:"groups_input"`
	GroupsInputMixin *int            `json:"groups_input_mixin"`
	Slimmable        json.RawMessage `json:"slimmable"`

	// FiLM: ainda não suportado; só aceitamos se estiver desligado.
	ConvPreFilm        json.RawMessage `json:"conv_pre_film"`
	ConvPostFilm       json.RawMessage `json:"conv_post_film"`
	InputMixinPreFilm  json.RawMessage `json:"input_mixin_pre_film"`
	InputMixinPostFilm json.RawMessage `json:"input_mixin_post_film"`
	ActivationPreFilm  json.RawMessage `json:"activation_pre_film"`
	ActivationPostFilm json.RawMessage `json:"activation_post_film"`
	Layer1x1PostFilm   json.RawMessage `json:"layer1x1_post_film"`
	Head1x1PostFilm    json.RawMessage `json:"head1x1_post_film"`
}

type waveNetJSON struct {
	Layers       []layerArrayJSON `json:"layers"`
	Head         json.RawMessage  `json:"head"`
	HeadScale    float32          `json:"head_scale"`
	InChannels   *int             `json:"in_channels"`
	ConditionDSP json.RawMessage  `json:"condition_dsp"`
}

func isNull(r json.RawMessage) bool {
	r = bytes.TrimSpace(r)
	return len(r) == 0 || bytes.Equal(r, []byte("null"))
}

func or(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

// filmActive diz se um bloco FiLM está ligado (ausente, null ou false = desligado).
func filmActive(r json.RawMessage) bool {
	if isNull(r) || bytes.Equal(bytes.TrimSpace(r), []byte("false")) {
		return false
	}
	var f struct {
		Active *bool `json:"active"`
	}
	if json.Unmarshal(r, &f) != nil {
		return true
	}
	return f.Active == nil || *f.Active
}

// weightReader consome o vetor de pesos na ordem do arquivo.
type weightReader struct {
	w []float32
	i int
}

func (r *weightReader) take(n int) ([]float32, error) {
	if r.i+n > len(r.w) {
		return nil, fmt.Errorf("pesos insuficientes: o modelo pede mais que os %d do arquivo", len(r.w))
	}
	s := r.w[r.i : r.i+n]
	r.i += n
	return s, nil
}

// readConv lê uma convolução agrupada. A ordem no arquivo é a do PyTorch achatado:
// grupo, saída, entrada, tap do kernel; depois o bias. Guarda a matriz cheia (zeros fora
// dos blocos de cada grupo).
func (r *weightReader) readConv(in, out, k, dil, groups int, bias bool) (conv, error) {
	if groups < 1 || in%groups != 0 || out%groups != 0 {
		return conv{}, fmt.Errorf("grupos (%d) não dividem os canais (%d → %d)", groups, in, out)
	}
	if in < 1 || out < 1 || k < 1 || dil < 1 {
		return conv{}, fmt.Errorf("convolução inválida (%d → %d, kernel %d, dilatação %d)", in, out, k, dil)
	}
	c := conv{in: in, out: out, k: k, dil: dil, w: make([]float32, k*out*in)}
	og, ig := out/groups, in/groups
	raw, err := r.take(groups * og * ig * k)
	if err != nil {
		return conv{}, err
	}
	p := 0
	for g := range groups {
		for o := range og {
			for i := range ig {
				for t := range k {
					c.w[((g*og+o)*k+t)*in+g*ig+i] = raw[p]
					p++
				}
			}
		}
	}
	if bias {
		b, err := r.take(out)
		if err != nil {
			return conv{}, err
		}
		c.bias = b
	}
	return c, nil
}

// parseWaveNet valida a config de um WaveNet e distribui os pesos.
func parseWaveNet(raw json.RawMessage, weights []float32) (*Spec, error) {
	s, err := buildWaveNet(raw, &weightReader{w: weights})
	if err != nil {
		return nil, err
	}
	if s.Weights != len(weights) {
		return nil, fmt.Errorf("sobram pesos: o modelo usa %d, o arquivo tem %d", s.Weights, len(weights))
	}
	return s, nil
}

// buildWaveNet monta o Spec consumindo os pesos de r; Spec.Weights = quantos consumiu.
func buildWaveNet(raw json.RawMessage, r *weightReader) (*Spec, error) {
	var cfg waveNetJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config do WaveNet: %w", err)
	}
	if !isNull(cfg.ConditionDSP) {
		return nil, errors.New("condition_dsp ainda não suportado")
	}
	if len(cfg.Layers) == 0 {
		return nil, errors.New("WaveNet sem pilhas de camadas")
	}
	inCh := or(cfg.InChannels, 1)
	if inCh != 1 {
		return nil, fmt.Errorf("só modelos mono (in_channels = %d)", inCh)
	}

	s := &Spec{}
	for i := range cfg.Layers {
		a, err := parseArray(&cfg.Layers[i], r)
		if err != nil {
			return nil, fmt.Errorf("pilha %d: %w", i, err)
		}
		if a.condSize != inCh {
			return nil, fmt.Errorf("pilha %d: condition_size %d (sem condition_dsp precisa ser %d)", i, a.condSize, inCh)
		}
		if i == 0 {
			if a.inputSize != inCh {
				return nil, fmt.Errorf("pilha 0: input_size %d, a entrada tem %d canal", a.inputSize, inCh)
			}
		} else {
			prev := &s.arrays[i-1]
			if a.inputSize != prev.channels {
				return nil, fmt.Errorf("pilha %d: input_size %d, a anterior sai com %d canais", i, a.inputSize, prev.channels)
			}
			if a.headIn != prev.headRechannel.out {
				return nil, fmt.Errorf("pilha %d: a cabeça soma %d canais, a anterior entrega %d", i, a.headIn, prev.headRechannel.out)
			}
		}
		s.arrays = append(s.arrays, a)
		s.Layers += len(a.layers)
	}
	s.Channels = s.arrays[0].channels
	lastOut := s.arrays[len(s.arrays)-1].headRechannel.out

	if !isNull(cfg.Head) {
		h, out, err := parseHead(cfg.Head, lastOut, r)
		if err != nil {
			return nil, fmt.Errorf("cabeça: %w", err)
		}
		s.head, lastOut = h, out
	}
	if lastOut != 1 {
		return nil, fmt.Errorf("só modelos mono (a saída tem %d canais)", lastOut)
	}

	// O último peso é o head_scale (prevalece sobre o da config, como na NAM core).
	hs, err := r.take(1)
	if err != nil {
		return nil, err
	}
	s.headScale = hs[0]
	s.Weights = r.i
	return s, nil
}

func parseArray(c *layerArrayJSON, r *weightReader) (arraySpec, error) {
	n := len(c.Dilations)
	if n == 0 {
		return arraySpec{}, errors.New("sem camadas (dilations vazio)")
	}
	if !isNull(c.Slimmable) {
		return arraySpec{}, errors.New("WaveNet slimmable por canais ainda não suportado")
	}
	for name, f := range map[string]json.RawMessage{
		"conv_pre_film": c.ConvPreFilm, "conv_post_film": c.ConvPostFilm,
		"input_mixin_pre_film": c.InputMixinPreFilm, "input_mixin_post_film": c.InputMixinPostFilm,
		"activation_pre_film": c.ActivationPreFilm, "activation_post_film": c.ActivationPostFilm,
		"layer1x1_post_film": c.Layer1x1PostFilm, "head1x1_post_film": c.Head1x1PostFilm,
	} {
		if filmActive(f) {
			return arraySpec{}, fmt.Errorf("FiLM (%s) ainda não suportado", name)
		}
	}

	a := arraySpec{inputSize: c.InputSize, condSize: c.ConditionSize, channels: c.Channels}
	a.bottleneck = or(c.Bottleneck, c.Channels)
	if a.channels < 1 || a.bottleneck < 1 || a.inputSize < 1 || a.condSize < 1 {
		return a, errors.New("canais inválidos")
	}

	// kernel: um para todas (formato antigo) ou um por camada
	var kernels []int
	switch {
	case c.KernelSize != nil && c.KernelSizes != nil:
		return a, errors.New("use kernel_size ou kernel_sizes, não os dois")
	case c.KernelSizes != nil:
		kernels = c.KernelSizes
	case c.KernelSize != nil:
		for range n {
			kernels = append(kernels, *c.KernelSize)
		}
	default:
		return a, errors.New("falta kernel_size/kernel_sizes")
	}
	if len(kernels) != n {
		return a, fmt.Errorf("%d kernels para %d camadas", len(kernels), n)
	}

	acts, err := perLayerActs(c.Activation, n, false)
	if err != nil {
		return a, fmt.Errorf("activation: %w", err)
	}
	gates, err := perLayerGating(c, n)
	if err != nil {
		return a, err
	}
	acts2, err := perLayerActs(c.SecondaryActivation, n, true)
	if err != nil {
		return a, fmt.Errorf("secondary_activation: %w", err)
	}

	l1 := c.Layer1x1 == nil || c.Layer1x1.Active == nil || *c.Layer1x1.Active // padrão: ligado
	l1Groups := 1
	if c.Layer1x1 != nil {
		l1Groups = or(c.Layer1x1.Groups, 1)
	}
	if !l1 && a.bottleneck != a.channels {
		return a, fmt.Errorf("sem layer1x1, bottleneck (%d) precisa ser igual a channels (%d)", a.bottleneck, a.channels)
	}
	h1 := c.Head1x1 != nil && c.Head1x1.Active != nil && *c.Head1x1.Active
	h1Out, h1Groups := a.channels, 1
	if c.Head1x1 != nil {
		h1Out, h1Groups = or(c.Head1x1.OutChannels, a.channels), or(c.Head1x1.Groups, 1)
	}
	a.headIn = a.bottleneck
	if h1 {
		a.headIn = h1Out
	}

	// cabeça da pilha (rechannel para head_size)
	headSize, headK, headDil, headBias := 0, 1, 1, false
	switch {
	case c.Head != nil:
		headSize, headK, headBias = c.Head.OutChannels, c.Head.KernelSize, c.Head.Bias
		headDil = or(c.Head.HeadDilation, 1)
	case c.HeadSize != nil:
		headSize = *c.HeadSize
		if c.HeadBias == nil {
			return a, errors.New("head_size sem head_bias")
		}
		headBias = *c.HeadBias
	default:
		return a, errors.New("falta head (ou head_size/head_bias)")
	}

	// Pesos, na ordem da NAM core: rechannel, camadas, head rechannel.
	if a.rechannel, err = r.readConv(a.inputSize, a.channels, 1, 1, 1, false); err != nil {
		return a, fmt.Errorf("rechannel: %w", err)
	}
	gIn, gMix := or(c.GroupsInput, 1), or(c.GroupsInputMixin, 1)
	for i := range n {
		var l layerSpec
		l.act, l.act2, l.gating = acts[i], acts2[i], gates[i]
		zc := a.bottleneck
		if l.gating != gateNone {
			zc *= 2
		}
		if err := l.act.checkChannels(a.bottleneck); err != nil {
			return a, fmt.Errorf("camada %d: %w", i, err)
		}
		if err := l.act2.checkChannels(a.bottleneck); err != nil {
			return a, fmt.Errorf("camada %d: %w", i, err)
		}
		if l.conv, err = r.readConv(a.channels, zc, kernels[i], c.Dilations[i], gIn, true); err != nil {
			return a, fmt.Errorf("camada %d, conv: %w", i, err)
		}
		if l.mixin, err = r.readConv(a.condSize, zc, 1, 1, gMix, false); err != nil {
			return a, fmt.Errorf("camada %d, mixin: %w", i, err)
		}
		if l1 {
			cv, err := r.readConv(a.bottleneck, a.channels, 1, 1, l1Groups, true)
			if err != nil {
				return a, fmt.Errorf("camada %d, layer1x1: %w", i, err)
			}
			l.layer1x1 = &cv
		}
		if h1 {
			cv, err := r.readConv(a.bottleneck, h1Out, 1, 1, h1Groups, true)
			if err != nil {
				return a, fmt.Errorf("camada %d, head1x1: %w", i, err)
			}
			l.head1x1 = &cv
		}
		a.layers = append(a.layers, l)
	}
	if a.headRechannel, err = r.readConv(a.headIn, headSize, headK, headDil, 1, headBias); err != nil {
		return a, fmt.Errorf("head rechannel: %w", err)
	}
	return a, nil
}

// perLayerActs aceita uma ativação para todas as camadas ou uma lista (com null nas
// camadas que não usam, no caso da secundária). Secundária ausente = Sigmoid.
func perLayerActs(raw json.RawMessage, n int, secondary bool) ([]activation, error) {
	out := make([]activation, n)
	if isNull(raw) {
		if !secondary {
			return nil, errors.New("ausente")
		}
		for i := range out {
			out[i] = newActivation(actSigmoid, "Sigmoid")
		}
		return out, nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		a, err := parseActivation(raw)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i] = a
		}
		return out, nil
	}
	if len(list) != n {
		return nil, fmt.Errorf("%d itens para %d camadas", len(list), n)
	}
	for i, item := range list {
		if isNull(item) {
			out[i] = newActivation(actSigmoid, "Sigmoid")
			continue
		}
		a, err := parseActivation(item)
		if err != nil {
			return nil, fmt.Errorf("camada %d: %w", i, err)
		}
		out[i] = a
	}
	return out, nil
}

func perLayerGating(c *layerArrayJSON, n int) ([]gating, error) {
	parse := func(s string) (gating, error) {
		switch s {
		case "none":
			return gateNone, nil
		case "gated":
			return gateGated, nil
		case "blended":
			return gateBlended, nil
		}
		return 0, fmt.Errorf("gating_mode desconhecido: %q", s)
	}
	out := make([]gating, n)
	switch {
	case !isNull(c.GatingMode):
		var list []string
		if json.Unmarshal(c.GatingMode, &list) == nil {
			if len(list) != n {
				return nil, fmt.Errorf("gating_mode: %d itens para %d camadas", len(list), n)
			}
			for i, s := range list {
				g, err := parse(s)
				if err != nil {
					return nil, err
				}
				out[i] = g
			}
			return out, nil
		}
		var one string
		if err := json.Unmarshal(c.GatingMode, &one); err != nil {
			return nil, fmt.Errorf("gating_mode inválido: %s", c.GatingMode)
		}
		g, err := parse(one)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i] = g
		}
	case c.Gated != nil && *c.Gated:
		for i := range out {
			out[i] = gateGated
		}
	}
	return out, nil
}

func parseHead(raw json.RawMessage, in int, r *weightReader) (*headSpec, int, error) {
	var hj struct {
		InChannels  *int            `json:"in_channels"`
		Channels    int             `json:"channels"`
		OutChannels int             `json:"out_channels"`
		KernelSizes []int           `json:"kernel_sizes"`
		Activation  json.RawMessage `json:"activation"`
	}
	if err := json.Unmarshal(raw, &hj); err != nil {
		return nil, 0, err
	}
	if hj.InChannels != nil && *hj.InChannels != in {
		return nil, 0, fmt.Errorf("in_channels %d, a última pilha entrega %d", *hj.InChannels, in)
	}
	if len(hj.KernelSizes) == 0 {
		return nil, 0, errors.New("kernel_sizes vazio")
	}
	act, err := parseActivation(hj.Activation)
	if err != nil {
		return nil, 0, err
	}
	h := &headSpec{act: act}
	cin := in
	for i, k := range hj.KernelSizes {
		cout := hj.Channels
		if i == len(hj.KernelSizes)-1 {
			cout = hj.OutChannels
		}
		if err := act.checkChannels(cin); err != nil {
			return nil, 0, err
		}
		cv, err := r.readConv(cin, cout, k, 1, 1, true)
		if err != nil {
			return nil, 0, err
		}
		h.convs = append(h.convs, cv)
		cin = cout
	}
	return h, cin, nil
}
