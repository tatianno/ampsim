package engine

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/nam"
)

// Modos do amp (valores do parâmetro "amp" do pré-amp).
const (
	AmpSimulated = iota
	AmpNAM
)

// Tamanhos do modelo NAM (parâmetro "nam_size"): o lite gasta ~1/6 da CPU do completo.
const (
	NAMLite = iota
	NAMFull
)

// NAMTargetLoudness é o loudness (dB) para o qual a saída de cada modelo é normalizada,
// usando o "loudness" dos metadados (como o "normalize" do plugin oficial). Acertado para
// as capturas incluídas, com o gabinete, ficarem em torno do volume do amp simulado no
// mesmo knob de volume: as limpas ~1,5 dB acima e as mais saturadas até ~5 dB abaixo
// (o loudness dos metadados é medido sem gabinete, e a IR corta os agudos da distorção).
const NAMTargetLoudness = -12.0

// AmpFadeMs é o crossfade ao trocar de modelo, de tamanho ou entre Simulado e NAM.
const AmpFadeMs = 20.0

// DefaultNAM é o modelo do amp até o usuário escolher outro.
var DefaultNAM = capturas.Prefix + "Dist Sat5 V4 B5 M5 T5 P5"

// DefaultDriveNAM é a captura do Drive no tipo NAM até o usuário escolher outra.
var DefaultDriveNAM = capturas.Prefix + "TS7 TS9 D5 T5 L5 (Medium Drive)"

// NAMInfo descreve o modelo carregado (para a GUI).
type NAMInfo struct {
	Ref      string // "embutida:<nome>" ou caminho do arquivo
	Name     string // nome para exibir
	Channels int    // canais da rede em uso
	Size     string // "lite" ou "completo" (vazio se o arquivo só tem uma rede)
	Warning  string // ex.: taxa da placa diferente da do modelo, sem reamostragem possível
	// InputLevelDBu é o input_level_dbu do modelo (nil se o arquivo não traz): o nível em dBu
	// que dava 0 dBFS na placa da captura (veja SetInputCalibration).
	InputLevelDBu *float64
	Resampled     string // ex.: "44,1↔48 kHz" (reamostrado; vazio se o modelo roda na taxa do áudio)
}

// namNet é uma rede pronta para a thread de áudio, na taxa do áudio: se o modelo foi
// treinado em outra taxa, roda dentro de uma dsp.RateBridge (reamostragem em streaming).
// A saída sai atrasada de exatamente lat amostras (a latência que o bloco compensa).
type namNet struct {
	net   *nam.Net
	rb    *dsp.RateBridge // nil: modelo na taxa do áudio
	delay *dsp.DelayLine
	pad   int // atraso que falta para completar lat (lat − latência da ponte)
}

func newNamNet(spec *nam.Spec, modelFS, fs float64, maxBlock, lat int) (*namNet, error) {
	nn := &namNet{pad: lat}
	var err error
	if modelFS > 0 && modelFS != fs {
		inner := dsp.BridgeMaxInner(fs, modelFS, maxBlock)
		nn.net = nam.NewNet(spec, inner)
		if nn.rb, err = dsp.NewRateBridge(fs, modelFS, maxBlock, lat, nn.net); err == nil {
			nn.pad = lat - nn.rb.Latency()
		}
	} else {
		nn.net = nam.NewNet(spec, maxBlock)
	}
	if nn.pad > 0 {
		nn.delay = dsp.NewDelayLine(nn.pad)
	}
	nn.net.Prewarm()
	return nn, err
}

func (nn *namNet) reset() {
	nn.net.Reset()
	if nn.rb != nil {
		nn.rb.Reset()
	}
	if nn.delay != nil {
		nn.delay.Reset()
	}
}

func (nn *namNet) process(in, out []float32) {
	if nn.rb != nil {
		nn.rb.Process(in, out)
	} else {
		nn.net.Process(in, out)
	}
	if nn.delay != nil {
		for i, v := range out {
			d := nn.delay.ReadInt(nn.pad)
			nn.delay.Write(float64(v))
			out[i] = float32(d)
		}
	}
}

// namInst é um modelo pronto para a thread de áudio: uma rede (aquecida) por submodelo.
type namInst struct {
	model *nam.Model
	ref   string
	nets  []*namNet
	infos []*NAMInfo // um por rede (montados fora da thread de áudio)
	gain  float32    // normalização pelo loudness
	level *float64   // input_level_dbu (nil se o arquivo não traz)
}

// namBlock roda modelos NAM; é o "motor" do pré-amp no modo NAM e do Drive no tipo NAM. A
// latência é compensada para ficar igual à do bloco simulado (a cadeia tem latência constante).
type namBlock struct {
	// configuração (fixa, dada na criação)
	latency   int    // atraso da saída, em amostras (a latência do bloco simulado)
	normalize bool   // normaliza a saída pelo loudness dos metadados (amp); pedal: não
	def       string // modelo usado até escolherem outro

	fs       float64
	maxBlock int
	cal      *inputCal // calibração da cadeia (nil fora de uma cadeia)

	// thread de áudio
	inst      *namInst
	cur, old  *namNet
	curIdx    int
	xf, xstep float64
	in, a, b  []float32
	pending   atomic.Pointer[namInst]
	calG      dsp.Smoother // ganho de calibração (linear), com rampa

	info atomic.Pointer[NAMInfo]

	mu       sync.Mutex // protege os campos abaixo (fora da thread de áudio)
	model    *nam.Model
	ref      string
	prepared bool
}

// loadModel lê um modelo embutido ("embutida:<nome>") ou um arquivo .nam.
func loadModel(ref string) (*nam.Model, error) {
	if name, ok := strings.CutPrefix(ref, capturas.Prefix); ok {
		return capturas.Load(name)
	}
	return nam.Load(ref)
}

// displayName é o nome do modelo para a GUI.
func displayName(m *nam.Model, ref string) string {
	if name, ok := strings.CutPrefix(ref, capturas.Prefix); ok {
		return name
	}
	if m.Name() != "" {
		return m.Name()
	}
	return strings.TrimSuffix(filepath.Base(ref), filepath.Ext(ref))
}

// Load carrega um modelo. Seguro com o áudio rodando: as redes são montadas e aquecidas
// aqui e entram com crossfade no próximo bloco.
func (n *namBlock) Load(ref string) error {
	m, err := loadModel(ref)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.model, n.ref = m, ref
	if n.prepared {
		n.pending.Store(n.build(m, ref))
	}
	return nil
}

// Ref devolve a referência do modelo atual ("" se nenhum).
func (n *namBlock) Ref() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ref
}

func (n *namBlock) build(m *nam.Model, ref string) *namInst {
	in := &namInst{model: m, ref: ref, gain: 1, level: m.Metadata.InputLevelDBu}
	for i, sm := range m.Submodels {
		net, err := newNamNet(sm.Spec, m.SampleRate, n.fs, n.maxBlock, n.latency)
		in.nets = append(in.nets, net)
		info := &NAMInfo{Ref: ref, Name: displayName(m, ref), Channels: sm.Spec.Channels, InputLevelDBu: in.level}
		if len(m.Submodels) > 1 {
			info.Size = "completo"
			if i < len(m.Submodels)-1 {
				info.Size = "lite"
			}
		}
		if sr := m.SampleRate; err != nil {
			// sem reamostragem possível: o modelo roda na taxa do áudio
			info.Warning = fmt.Sprintf("modelo treinado a %g kHz, áudio a %g kHz: o timbre muda um pouco", sr/1000, n.fs/1000)
		} else if net.rb != nil {
			info.Resampled = strings.ReplaceAll(fmt.Sprintf("%g↔%g kHz", n.fs/1000, sr/1000), ".", ",")
		}
		in.infos = append(in.infos, info)
	}
	// Pedal: o ganho que a rede aprendeu (o quanto o pedal empurra o amp) faz parte do som;
	// além disso, o loudness dos metadados muda entre versões do mesmo arquivo.
	if l := m.Metadata.Loudness; l != nil && n.normalize {
		in.gain = float32(math.Pow(10, (NAMTargetLoudness-*l)/20))
	}
	return in
}

func (n *namBlock) prepare(fs float64, maxBlock int) {
	n.fs, n.maxBlock = fs, maxBlock
	n.in = make([]float32, maxBlock)
	n.a = make([]float32, maxBlock)
	n.b = make([]float32, maxBlock)
	n.xstep = 1 / (AmpFadeMs / 1000 * fs)
	n.calG = dsp.NewSmoother(fs, CalibrationFadeMs)

	n.mu.Lock()
	if n.model == nil && n.def != "" {
		if m, err := loadModel(n.def); err == nil {
			n.model, n.ref = m, n.def
		}
	}
	n.prepared = true
	n.inst, n.cur, n.old, n.curIdx, n.xf = nil, nil, nil, -1, 1
	if n.model != nil {
		n.inst = n.build(n.model, n.ref)
	}
	n.pending.Store(nil)
	n.mu.Unlock()
}

func (n *namBlock) reset() {
	if n.cur != nil {
		n.cur.reset()
	}
	n.old, n.xf = nil, 1
	n.calG = dsp.NewSmoother(n.fs, CalibrationFadeMs) // volta direto ao alvo no próximo bloco
}

// calOffset devolve o ajuste de calibração do modelo atual (dB) e se ele vale.
func (n *namBlock) calOffset() (float64, bool) {
	n.mu.Lock()
	m := n.model
	n.mu.Unlock()
	if m == nil || m.Metadata.InputLevelDBu == nil || n.cal == nil || !n.cal.on.Load() {
		return 0, false
	}
	return n.cal.offsetDB(m.Metadata.InputLevelDBu), true
}

// netFor devolve o índice da rede para o tamanho pedido.
func (in *namInst) netFor(size int) int {
	if size == NAMLite {
		return in.model.SubmodelFor(0)
	}
	return in.model.SubmodelFor(1)
}

// process roda o modelo sobre buf (in-place), com ganho de entrada inGain e tamanho size.
func (n *namBlock) process(buf []float64, inGain float64, size int) {
	// troca de modelo ou de tamanho: só começa quando o crossfade anterior terminou
	if n.xf >= 1 {
		if p := n.pending.Swap(nil); p != nil {
			n.inst = p
			n.curIdx = -1
		}
		if n.inst != nil {
			if idx := n.inst.netFor(size); idx != n.curIdx {
				next := n.inst.nets[idx]
				next.reset()
				n.old, n.cur, n.curIdx = n.cur, next, idx
				if n.old != nil {
					n.xf = 0
				}
				n.info.Store(n.inst.infos[idx])
			}
		}
	}
	k := len(buf)
	if n.cur == nil {
		clear(buf)
		return
	}
	n.calG.SetTarget(math.Pow(10, n.cal.offsetDB(n.inst.level)/20))
	x := n.in[:k]
	for i, v := range buf {
		x[i] = float32(v * inGain * n.calG.Next())
	}
	a := n.a[:k]
	n.cur.process(x, a)
	gain := n.inst.gain
	if n.xf < 1 {
		b := n.b[:k]
		n.old.process(x, b)
		for i := range a {
			n.xf = math.Min(1, n.xf+n.xstep)
			a[i] = float32(n.xf)*a[i] + float32(1-n.xf)*b[i]
		}
		if n.xf >= 1 {
			n.old = nil
		}
	}
	for i, v := range a {
		buf[i] = float64(v * gain)
	}
}

// Info devolve o modelo em uso (nil antes do primeiro bloco no modo NAM).
func (n *namBlock) Info() *NAMInfo { return n.info.Load() }

// load valida a referência e carrega o modelo: "embutida:<nome>" ou o caminho de um .nam.
func (n *namBlock) load(ref string) error {
	if ref == "" {
		return errors.New("modelo NAM vazio")
	}
	if !strings.HasPrefix(ref, capturas.Prefix) {
		if _, err := os.Stat(ref); err != nil {
			return fmt.Errorf("carregando modelo NAM: %w", err)
		}
	}
	if err := n.Load(ref); err != nil {
		return fmt.Errorf("carregando modelo NAM: %w", err)
	}
	return nil
}

// current devolve o modelo escolhido, ou o padrão se nenhum foi carregado.
func (n *namBlock) current() string {
	if r := n.Ref(); r != "" {
		return r
	}
	return n.def
}

// applyExtra carrega o modelo de um preset (extra.nam), se for outro.
func (n *namBlock) applyExtra(s map[string]string) error {
	if r := s["nam"]; r != "" && r != n.Ref() {
		return n.load(r)
	}
	return nil
}

// ---------------------------------------------------------------- API no Pre

// LoadNAM carrega um modelo NAM no pré-amp: "embutida:<nome>" ou o caminho de um .nam.
func (m *Pre) LoadNAM(ref string) error { return m.nam.load(ref) }

// NAMRef devolve o modelo NAM escolhido ("embutida:<nome>" ou caminho).
func (m *Pre) NAMRef() string { return m.nam.current() }

// NAMInfo descreve o modelo NAM em uso (nil se o modo NAM ainda não rodou).
func (m *Pre) NAMInfo() *NAMInfo { return m.nam.Info() }

// Mode devolve o modo do amp (AmpSimulated ou AmpNAM).
func (m *Pre) Mode() int { return int(m.get("amp")) }

func (m *Pre) ExtraState() map[string]string {
	return map[string]string{"nam": m.NAMRef()}
}

func (m *Pre) ApplyExtraState(s map[string]string) error { return m.nam.applyExtra(s) }
