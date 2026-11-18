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

// DefaultNAM é o modelo usado até o usuário escolher outro.
var DefaultNAM = capturas.Prefix + "Dist Sat5 V4 B5 M5 T5 P5"

// NAMInfo descreve o modelo carregado (para a GUI).
type NAMInfo struct {
	Ref      string // "embutida:<nome>" ou caminho do arquivo
	Name     string // nome para exibir
	Channels int    // canais da rede em uso
	Size     string // "lite" ou "completo" (vazio se o arquivo só tem uma rede)
	Warning  string // ex.: taxa da placa diferente da do modelo
}

// namInst é um modelo pronto para a thread de áudio: uma Net (aquecida) por submodelo.
type namInst struct {
	model *nam.Model
	ref   string
	nets  []*nam.Net
	infos []*NAMInfo // um por rede (montados fora da thread de áudio)
	gain  float32    // normalização pelo loudness
}

// namAmp roda modelos NAM; é o "motor" do pré-amp no modo NAM. A latência é compensada
// para ficar igual à do amp simulado (a cadeia tem latência constante).
type namAmp struct {
	fs       float64
	maxBlock int

	// thread de áudio
	inst      *namInst
	cur, old  *nam.Net
	curIdx    int
	xf, xstep float64
	in, a, b  []float32
	delay     *dsp.DelayLine
	pending   atomic.Pointer[namInst]

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
func (n *namAmp) Load(ref string) error {
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
func (n *namAmp) Ref() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ref
}

func (n *namAmp) build(m *nam.Model, ref string) *namInst {
	in := &namInst{model: m, ref: ref, gain: 1}
	for i, sm := range m.Submodels {
		net := nam.NewNet(sm.Spec, n.maxBlock)
		net.Prewarm()
		in.nets = append(in.nets, net)
		info := &NAMInfo{Ref: ref, Name: displayName(m, ref), Channels: sm.Spec.Channels}
		if len(m.Submodels) > 1 {
			info.Size = "completo"
			if i < len(m.Submodels)-1 {
				info.Size = "lite"
			}
		}
		if sr := m.SampleRate; sr > 0 && sr != n.fs {
			info.Warning = fmt.Sprintf("modelo treinado a %g kHz, áudio a %g kHz: o timbre muda um pouco", sr/1000, n.fs/1000)
		}
		in.infos = append(in.infos, info)
	}
	if l := m.Metadata.Loudness; l != nil {
		in.gain = float32(math.Pow(10, (NAMTargetLoudness-*l)/20))
	}
	return in
}

func (n *namAmp) prepare(fs float64, maxBlock int) {
	n.fs, n.maxBlock = fs, maxBlock
	n.in = make([]float32, maxBlock)
	n.a = make([]float32, maxBlock)
	n.b = make([]float32, maxBlock)
	n.xstep = 1 / (AmpFadeMs / 1000 * fs)
	n.delay = dsp.NewDelayLine(dsp.PreampLatency)

	n.mu.Lock()
	if n.model == nil {
		if m, err := loadModel(DefaultNAM); err == nil {
			n.model, n.ref = m, DefaultNAM
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

func (n *namAmp) reset() {
	if n.cur != nil {
		n.cur.Reset()
	}
	n.old, n.xf = nil, 1
	n.delay.Reset()
}

// netFor devolve o índice da rede para o tamanho pedido.
func (in *namInst) netFor(size int) int {
	if size == NAMLite {
		return in.model.SubmodelFor(0)
	}
	return in.model.SubmodelFor(1)
}

// process roda o modelo sobre buf (in-place), com ganho de entrada inGain e tamanho size.
func (n *namAmp) process(buf []float64, inGain float64, size int) {
	// troca de modelo ou de tamanho: só começa quando o crossfade anterior terminou
	if n.xf >= 1 {
		if p := n.pending.Swap(nil); p != nil {
			n.inst = p
			n.curIdx = -1
		}
		if n.inst != nil {
			if idx := n.inst.netFor(size); idx != n.curIdx {
				next := n.inst.nets[idx]
				next.Reset()
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
	g := float32(inGain)
	x := n.in[:k]
	for i, v := range buf {
		x[i] = float32(v) * g
	}
	a := n.a[:k]
	n.cur.Process(x, a)
	gain := n.inst.gain
	if n.xf < 1 {
		b := n.b[:k]
		n.old.Process(x, b)
		for i := range a {
			n.xf = math.Min(1, n.xf+n.xstep)
			a[i] = float32(n.xf)*a[i] + float32(1-n.xf)*b[i]
		}
		if n.xf >= 1 {
			n.old = nil
		}
	}
	for i, v := range a {
		buf[i] = n.delay.ReadInt(dsp.PreampLatency)
		n.delay.Write(float64(v * gain))
	}
}

// Info devolve o modelo em uso (nil antes do primeiro bloco no modo NAM).
func (n *namAmp) Info() *NAMInfo { return n.info.Load() }

// ---------------------------------------------------------------- API no Pre

// LoadNAM carrega um modelo NAM no pré-amp: "embutida:<nome>" ou o caminho de um .nam.
func (m *Pre) LoadNAM(ref string) error {
	if ref == "" {
		return errors.New("modelo NAM vazio")
	}
	if !strings.HasPrefix(ref, capturas.Prefix) {
		if _, err := os.Stat(ref); err != nil {
			return fmt.Errorf("carregando modelo NAM: %w", err)
		}
	}
	if err := m.nam.Load(ref); err != nil {
		return fmt.Errorf("carregando modelo NAM: %w", err)
	}
	return nil
}

// NAMRef devolve o modelo NAM escolhido ("embutida:<nome>" ou caminho).
func (m *Pre) NAMRef() string {
	if r := m.nam.Ref(); r != "" {
		return r
	}
	return DefaultNAM
}

// NAMInfo descreve o modelo NAM em uso (nil se o modo NAM ainda não rodou).
func (m *Pre) NAMInfo() *NAMInfo { return m.nam.Info() }

// Mode devolve o modo do amp (AmpSimulated ou AmpNAM).
func (m *Pre) Mode() int { return int(m.get("amp")) }

func (m *Pre) ExtraState() map[string]string {
	return map[string]string{"nam": m.NAMRef()}
}

func (m *Pre) ApplyExtraState(s map[string]string) error {
	if r := s["nam"]; r != "" && r != m.nam.Ref() {
		return m.LoadNAM(r)
	}
	return nil
}
