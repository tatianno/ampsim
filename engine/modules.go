package engine

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/wav"
)

// Module é um bloco da cadeia (o amp ou um pedal). Mono, processamento in-place.
//
// Contrato (vale para o app e para o futuro plugin):
//   - Prepare pode alocar; é chamado com o áudio parado.
//   - Process e Reset não alocam, não travam e não fazem I/O.
//   - Process aceita qualquer tamanho de bloco até maxBlock.
type Module interface {
	ID() string
	Name() string
	Params() []*Param
	Param(id string) *Param
	Prepare(fs float64, maxBlock int)
	Reset()
	Latency() int
	Process(buf []float64)
}

// ExtraState é implementado por módulos com estado além dos knobs (ex.: arquivo de IR).
type ExtraState interface {
	ExtraState() map[string]string
	ApplyExtraState(map[string]string) error
}

type base struct {
	id, name string
	paramSet
}

func (b *base) ID() string   { return b.id }
func (b *base) Name() string { return b.name }

// pct converte um parâmetro em % para 0..1.
func (b *base) pct(id string) float64 { return b.get(id) / 100 }

// ---------------------------------------------------------------- Gate

// Gate é o pedal de noise gate.
type Gate struct {
	base
	g    *dsp.Gate
	open atomic.Bool // publicado para os medidores da GUI
}

// IsOpen informa se o gate está aberto (seguro para chamar de outra goroutine).
func (m *Gate) IsOpen() bool { return m.open.Load() }

// NewGate cria o módulo de gate.
func NewGate() *Gate {
	return &Gate{base: base{id: "gate", name: "Gate", paramSet: newParamSet(
		ParamInfo{ID: "threshold", Name: "Threshold", Unit: "dB", Min: -80, Max: -10, Default: -40},
		ParamInfo{ID: "release", Name: "Release", Unit: "ms", Min: 10, Max: 1000, Default: 100},
	)}}
}

func (m *Gate) Prepare(fs float64, _ int) {
	m.g = dsp.NewGate(fs)
	m.g.SetHysteresis(3)
}
func (m *Gate) Reset()       { m.g.Reset() }
func (m *Gate) Latency() int { return 0 }
func (m *Gate) Process(buf []float64) {
	m.g.SetThreshold(m.get("threshold"))
	m.g.SetTimes(1, m.get("release"))
	m.g.ProcessBlock(buf)
	m.open.Store(m.g.IsOpen())
}

// ---------------------------------------------------------------- Amp
//
// O amp é dividido em três módulos para permitir o loop de efeitos (send/return):
//
//	PRÉ-AMP (pre) → [loop: pedais] → POWER AMP (power) → GABINETE (cab)
//
// A cadeia garante a ordem pre → power → cab; os pedais podem ficar em qualquer posição.

// Pre é o pré-amp: HP 70 Hz → GANHO/saturação 4x → tone stack (grave/médio/agudo).
//
// O tone stack pode ser o "Moderno" (3 equalizadores independentes) ou o circuito passivo
// TMB modelado ("Americano": Fender Bassman 5F6-A; "Britânico": Marshall JTM45/1959), em que
// os controles interagem.
type Pre struct {
	base
	tight dsp.Biquad
	pre   *dsp.Preamp
	tone  *dsp.ToneStack
	tmb   [2]*dsp.TMBStack // Americano, Britânico

	kind, prev int     // tone stack atual e o anterior (durante a troca)
	xf, xstep  float64 // crossfade da troca de tone stack (0 → 1)
	scratch    []float64

	nam         namAmp
	mode, pmode int     // modo do amp atual e o anterior (durante a troca)
	mxf, mxstep float64 // crossfade da troca de modo
	mscratch    []float64
}

// Tipos de tone stack (valores do parâmetro "stack").
const (
	StackModern = iota
	StackAmerican
	StackBritish
)

// StackFadeMs é o crossfade ao trocar de tone stack (evita estalo).
const StackFadeMs = 20.0

// NewPre cria o pré-amp. No modo NAM ("amp" = NAM), um modelo NAM substitui o
// pré-amp simulado (e o power amp passa a ser só o volume; veja Power).
func NewPre() *Pre {
	return &Pre{base: base{id: "pre", name: "Pré-amp", paramSet: newParamSet(
		ParamInfo{ID: "gain", Name: "Ganho", Min: 0, Max: 10, Default: 6.5},
		ParamInfo{ID: "bass", Name: "Grave", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "mid", Name: "Médio", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "treble", Name: "Agudo", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "stack", Name: "Tone stack", Min: 0, Max: 2, Default: StackModern, Step: 1,
			Choices: []string{"Moderno", "Americano", "Britânico"}},
		ParamInfo{ID: "amp", Name: "Amp", Min: 0, Max: 1, Default: AmpSimulated, Step: 1,
			Choices: []string{"Simulado", "NAM"}},
		ParamInfo{ID: "nam_input", Name: "Entrada", Unit: "dB", Min: -24, Max: 24, Default: 0},
		ParamInfo{ID: "nam_size", Name: "Tamanho", Min: 0, Max: 1, Default: NAMLite, Step: 1,
			Choices: []string{"Lite", "Completo"}},
	)}}
}

func (m *Pre) Prepare(fs float64, maxBlock int) {
	m.tight.SetCoefs(dsp.OnePoleHighpass(fs, 70))
	m.pre = dsp.NewPreamp(fs)
	m.tone = dsp.NewToneStack(fs)
	m.tmb = [2]*dsp.TMBStack{dsp.NewTMBStack(fs, dsp.TMBFender), dsp.NewTMBStack(fs, dsp.TMBMarshall)}
	m.kind = int(m.get("stack"))
	m.prev, m.xf = m.kind, 1
	m.xstep = 1 / (StackFadeMs / 1000 * fs)
	m.scratch = make([]float64, maxBlock)
	m.mode = m.Mode()
	m.pmode, m.mxf = m.mode, 1
	m.mxstep = 1 / (AmpFadeMs / 1000 * fs)
	m.mscratch = make([]float64, maxBlock)
	m.nam.prepare(fs, maxBlock)
}

func (m *Pre) Reset() {
	m.resetSim()
	m.nam.reset()
	m.mxf = 1
}

func (m *Pre) resetSim() {
	m.tight.Reset()
	m.pre.Reset()
	m.tone.Reset()
	for _, t := range m.tmb {
		t.Reset()
	}
}

func (m *Pre) Latency() int { return dsp.PreampLatency }

// stack aplica o tone stack k.
func (m *Pre) stack(k int, buf []float64) {
	switch k {
	case StackAmerican:
		m.tmb[0].ProcessBlock(buf)
	case StackBritish:
		m.tmb[1].ProcessBlock(buf)
	default:
		m.tone.ProcessBlock(buf)
	}
}

func (m *Pre) Process(buf []float64) {
	if mode := m.Mode(); mode != m.mode && m.mxf >= 1 {
		m.pmode, m.mode, m.mxf = m.mode, mode, 0
		if mode == AmpNAM {
			m.nam.reset()
		} else {
			m.resetSim()
		}
	}
	if m.mxf >= 1 {
		m.run(m.mode, buf)
		return
	}
	// troca de modo em andamento: os dois rodam e o resultado é misturado
	old := m.mscratch[:len(buf)]
	copy(old, buf)
	m.run(m.pmode, old)
	m.run(m.mode, buf)
	for i := range buf {
		m.mxf = math.Min(1, m.mxf+m.mxstep)
		buf[i] = m.mxf*buf[i] + (1-m.mxf)*old[i]
	}
}

func (m *Pre) run(mode int, buf []float64) {
	if mode == AmpNAM {
		m.nam.process(buf, math.Pow(10, m.get("nam_input")/20), int(m.get("nam_size")))
		return
	}
	m.processSim(buf)
}

func (m *Pre) processSim(buf []float64) {
	m.pre.SetDrive(m.get("gain") / 10)
	bass, mid, treble := m.get("bass"), m.get("mid"), m.get("treble")
	m.tone.Set(bass, mid, treble)
	for _, t := range m.tmb {
		t.Set(bass, mid, treble)
	}
	m.tight.ProcessBlock(buf)
	m.pre.ProcessBlock(buf)

	if k := int(m.get("stack")); k != m.kind && m.xf >= 1 {
		m.prev, m.kind, m.xf = m.kind, k, 0
		switch k { // o novo começa limpo
		case StackAmerican:
			m.tmb[0].Reset()
		case StackBritish:
			m.tmb[1].Reset()
		default:
			m.tone.Reset()
		}
	}
	if m.xf >= 1 {
		m.stack(m.kind, buf)
		return
	}
	// troca em andamento: os dois tone stacks rodam e o resultado é misturado
	old := m.scratch[:len(buf)]
	copy(old, buf)
	m.stack(m.prev, old)
	m.stack(m.kind, buf)
	for i := range buf {
		m.xf = math.Min(1, m.xf+m.xstep)
		buf[i] = m.xf*buf[i] + (1-m.xf)*old[i]
	}
}

// Power é o power amp: saturação suave (tanh) → presença → volume. Com o pré-amp no
// modo NAM (a captura já inclui o power amp), sobra só o volume.
type Power struct {
	base
	presence *dsp.Presence
	volume   dsp.Smoother

	ampMode   *Param  // parâmetro "amp" do pré-amp (nil = sempre simulado)
	sim, step float64 // 1 = power simulado, 0 = só volume (crossfade na troca)
	dry       []float64
}

// FollowAmpMode liga o power amp ao seletor de modo do pré-amp.
func (m *Power) FollowAmpMode(pre *Pre) { m.ampMode = pre.Param("amp") }

func (m *Power) simTarget() float64 {
	if m.ampMode != nil && int(m.ampMode.Get()) == AmpNAM {
		return 0
	}
	return 1
}

// NewPower cria o power amp.
func NewPower() *Power {
	return &Power{base: base{id: "power", name: "Power amp", paramSet: newParamSet(
		ParamInfo{ID: "presence", Name: "Presença", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "volume", Name: "Volume", Unit: "dB", Min: -40, Max: 12, Default: -12},
	)}}
}

func (m *Power) Prepare(fs float64, maxBlock int) {
	m.dry = make([]float64, maxBlock)
	m.presence = dsp.NewPresence(fs)
	m.volume = dsp.NewSmoother(fs, 20)
	m.sim = m.simTarget()
	m.step = 1 / (AmpFadeMs / 1000 * fs)
}

func (m *Power) Reset()       { m.presence.Reset(); m.sim = m.simTarget() }
func (m *Power) Latency() int { return 0 }

func (m *Power) Process(buf []float64) {
	m.volume.SetTarget(math.Pow(10, m.get("volume")/20))
	target := m.simTarget()
	switch {
	case target == 0 && m.sim == 0: // só volume
	case target == 1 && m.sim == 1:
		m.presence.Set(m.get("presence"))
		for i, x := range buf {
			buf[i] = math.Tanh(x)
		}
		m.presence.ProcessBlock(buf)
	default: // troca de modo: mistura o power simulado com o sinal direto
		if m.sim == 0 {
			m.presence.Reset()
		}
		m.presence.Set(m.get("presence"))
		dry := m.dry[:len(buf)]
		copy(dry, buf)
		for i, x := range buf {
			buf[i] = math.Tanh(x)
		}
		m.presence.ProcessBlock(buf)
		for i, x := range dry {
			if m.sim < target {
				m.sim = math.Min(target, m.sim+m.step)
			} else {
				m.sim = math.Max(target, m.sim-m.step)
			}
			buf[i] = m.sim*buf[i] + (1-m.sim)*x
		}
	}
	for i := range buf {
		buf[i] *= m.volume.Next()
	}
}

// Cab é o gabinete: IR (convolução) → low cut / high cut (o "microfone").
type Cab struct {
	base
	fs              float64
	hp, lp          dsp.Cascade
	lowCut, highCut float64 // valores aplicados nos filtros

	conv    *dsp.Convolver                // usado só pela thread de áudio
	pending atomic.Pointer[dsp.Convolver] // nova IR preparada fora da thread de áudio

	mu     sync.Mutex // protege os campos abaixo (fora da thread de áudio)
	irRaw  []float64
	irFS   int
	irPath string
	prepFS float64
}

// IRMaxMs é o comprimento máximo da IR usada.
const IRMaxMs = 150.0

// IRPartition é o tamanho de partição da convolução.
const IRPartition = 64

// NewCab cria o gabinete (sem IR: só os filtros, até LoadIR).
func NewCab() *Cab {
	return &Cab{base: base{id: "cab", name: "Gabinete", paramSet: newParamSet(
		ParamInfo{ID: "lowcut", Name: "Low cut", Unit: "Hz", Min: 20, Max: 300, Default: 85},
		ParamInfo{ID: "highcut", Name: "High cut", Unit: "Hz", Min: 2000, Max: 12000, Default: 6200},
	)}}
}

func (m *Cab) Prepare(fs float64, _ int) {
	m.fs = fs
	m.hp = dsp.NewCascade(dsp.ButterworthHighpass(cabHPOrder, fs, m.get("lowcut")))
	m.lp = dsp.NewCascade(dsp.ButterworthLowpass(cabLPOrder, fs, m.get("highcut")))
	m.lowCut, m.highCut = m.get("lowcut"), m.get("highcut")

	m.mu.Lock()
	m.prepFS = fs
	if m.irRaw != nil {
		m.conv = dsp.NewConvolver(dsp.PrepareIR(m.irRaw, m.irFS, int(fs), IRMaxMs), IRPartition)
	} else {
		// sem IR escolhida: o gabinete interno (sem gabinete o amp soa áspero e chiado)
		m.conv = dsp.NewConvolver(builtinIR(fs), IRPartition)
	}
	m.pending.Store(nil)
	m.mu.Unlock()
}

func (m *Cab) Reset() {
	m.hp.Reset()
	m.lp.Reset()
	if m.conv != nil {
		m.conv.Reset()
	}
}

func (m *Cab) Latency() int { return 0 }

func (m *Cab) Process(buf []float64) {
	if c := m.pending.Swap(nil); c != nil {
		m.conv = c
	}
	// Filtros recalculados só quando o knob muda (sem alocar: os estágios já existem).
	if f := m.get("lowcut"); f != m.lowCut {
		setCascade(m.hp, dsp.ButterworthHighpassInto, cabHPOrder, f, m.fs)
		m.lowCut = f
	}
	if f := m.get("highcut"); f != m.highCut {
		setCascade(m.lp, dsp.ButterworthLowpassInto, cabLPOrder, f, m.fs)
		m.highCut = f
	}
	if m.conv != nil {
		m.conv.ProcessBlock(buf)
	}
	m.hp.ProcessBlock(buf)
	m.lp.ProcessBlock(buf)
}

// Ordens dos filtros do gabinete (as mesmas do main.py).
const (
	cabHPOrder = 2
	cabLPOrder = 3
)

func setCascade(c dsp.Cascade, design func(dst []dsp.Coefs, order int, fs, f float64), order int, f, fs float64) {
	var tmp [4]dsp.Coefs
	cs := tmp[:len(c)]
	design(cs, order, fs, f)
	for i := range c {
		c[i].SetCoefs(cs[i])
	}
}

// builtinIR é a IR do gabinete interno (capturas.DefaultIR) preparada para fs; se ela
// não puder ser lida, cai na IR genérica sintética.
func builtinIR(fs float64) []float64 {
	if irFS, raw, err := capturas.DefaultIR(); err == nil {
		return dsp.PrepareIR(raw, irFS, int(fs), IRMaxMs)
	}
	return dsp.GenericCabIR(fs)
}

// LoadIR carrega uma IR (amostras brutas e sua taxa). Pode ser chamado com o áudio
// rodando: a IR é preparada aqui e trocada de forma atômica no próximo bloco.
func (m *Cab) LoadIR(raw []float64, fsIR int, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.irRaw, m.irFS, m.irPath = raw, fsIR, path
	if m.prepFS > 0 {
		m.pending.Store(dsp.NewConvolver(dsp.PrepareIR(raw, fsIR, int(m.prepFS), IRMaxMs), IRPartition))
	}
}

// LoadIRFile carrega a IR de um arquivo WAV.
func (m *Cab) LoadIRFile(path string) error {
	fs, raw, err := wav.ReadMonoFile(path)
	if err != nil {
		return fmt.Errorf("carregando IR: %w", err)
	}
	m.LoadIR(raw, fs, path)
	return nil
}

// ResolveIR interpreta o -ir da linha de comando: um arquivo existente, ou o nome de uma
// IR incluída (ex.: "4x12 SM7").
func ResolveIR(s string) string {
	if _, err := os.Stat(s); err == nil || strings.HasPrefix(s, capturas.Prefix) {
		return s
	}
	for _, n := range capturas.IRNames() {
		if strings.EqualFold(n, s) {
			return capturas.Prefix + n
		}
	}
	return s // deixa o erro de arquivo inexistente aparecer
}

// LoadIRRef carrega uma IR incluída ("embutida:<nome>", veja capturas.IRNames) ou um arquivo WAV.
func (m *Cab) LoadIRRef(ref string) error {
	name, ok := strings.CutPrefix(ref, capturas.Prefix)
	if !ok {
		return m.LoadIRFile(ref)
	}
	fs, raw, err := capturas.LoadIR(name)
	if err != nil {
		return fmt.Errorf("carregando IR: %w", err)
	}
	m.LoadIR(raw, fs, ref)
	return nil
}

// IRPath devolve a IR escolhida: o caminho do arquivo, "embutida:<nome>" ou "" (a padrão,
// capturas.DefaultIRName).
func (m *Cab) IRPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.irPath
}

// IRName devolve o nome da IR em uso para exibir (o nome da incluída ou o do arquivo).
func (m *Cab) IRName() string {
	p := m.IRPath()
	if p == "" {
		return capturas.DefaultIRName
	}
	if n, ok := strings.CutPrefix(p, capturas.Prefix); ok {
		return n
	}
	return filepath.Base(p)
}

func (m *Cab) ExtraState() map[string]string {
	if p := m.IRPath(); p != "" {
		return map[string]string{"ir": p}
	}
	return nil
}

func (m *Cab) ApplyExtraState(s map[string]string) error {
	if p := s["ir"]; p != "" && p != m.IRPath() {
		return m.LoadIRRef(p)
	}
	return nil
}

// ---------------------------------------------------------------- Chorus

// Chorus é o pedal de chorus.
type Chorus struct {
	base
	c *dsp.Chorus
}

// NewChorus cria o módulo de chorus.
func NewChorus() *Chorus {
	return &Chorus{base: base{id: "chorus", name: "Chorus", paramSet: newParamSet(
		ParamInfo{ID: "rate", Name: "Rate", Unit: "Hz", Min: 0.1, Max: 5, Default: 1.2},
		ParamInfo{ID: "depth", Name: "Depth", Unit: "ms", Min: 0, Max: 5, Default: 2},
		ParamInfo{ID: "delay", Name: "Delay", Unit: "ms", Min: 7, Max: 40, Default: 22},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 50},
	)}}
}

func (m *Chorus) Prepare(fs float64, _ int) { m.c = dsp.NewChorus(fs) }
func (m *Chorus) Reset()                    { m.c.Reset() }
func (m *Chorus) Latency() int              { return 0 }
func (m *Chorus) Process(buf []float64) {
	m.c.Set(m.get("rate"), m.get("depth"), m.get("delay"), m.pct("mix"))
	m.c.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Flanger

// Flanger é o pedal de flanger. Manual = atraso central; Depth = quanto varia em torno dele.
type Flanger struct {
	base
	f *dsp.Flanger
}

// NewFlanger cria o módulo de flanger.
func NewFlanger() *Flanger {
	return &Flanger{base: base{id: "flanger", name: "Flanger", paramSet: newParamSet(
		ParamInfo{ID: "rate", Name: "Rate", Unit: "Hz", Min: 0.05, Max: 2, Default: 0.3},
		ParamInfo{ID: "manual", Name: "Manual", Unit: "ms", Min: 0.5, Max: 5, Default: 2.25},
		ParamInfo{ID: "depth", Name: "Depth", Unit: "ms", Min: 0, Max: 2.5, Default: 1.75},
		ParamInfo{ID: "feedback", Name: "Feedback", Unit: "%", Min: -90, Max: 90, Default: 60},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 50},
	)}}
}

func (m *Flanger) Prepare(fs float64, _ int) { m.f = dsp.NewFlanger(fs) }
func (m *Flanger) Reset()                    { m.f.Reset() }
func (m *Flanger) Latency() int              { return 0 }
func (m *Flanger) Process(buf []float64) {
	man, dep := m.get("manual"), m.get("depth")
	m.f.Set(m.get("rate"), math.Max(0.05, man-dep), man+dep, m.pct("feedback"), m.pct("mix"))
	m.f.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Phaser

// PhaserMinHz é a frequência mais baixa da varredura do phaser.
const PhaserMinHz = 200

// Phaser é o pedal de phaser. Range = largura da varredura em oitavas acima de 200 Hz.
type Phaser struct {
	base
	p *dsp.Phaser
}

// NewPhaser cria o módulo de phaser.
func NewPhaser() *Phaser {
	return &Phaser{base: base{id: "phaser", name: "Phaser", paramSet: newParamSet(
		ParamInfo{ID: "rate", Name: "Rate", Unit: "Hz", Min: 0.05, Max: 5, Default: 0.5},
		ParamInfo{ID: "range", Name: "Range", Unit: "oct", Min: 0.5, Max: 4, Default: math.Log2(9)},
		ParamInfo{ID: "feedback", Name: "Feedback", Unit: "%", Min: -90, Max: 90, Default: 50},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 50},
		ParamInfo{ID: "stages", Name: "Estágios", Min: 4, Max: 8, Default: 4, Step: 2},
	)}}
}

func (m *Phaser) Prepare(fs float64, _ int) { m.p = dsp.NewPhaser(fs) }
func (m *Phaser) Reset()                    { m.p.Reset() }
func (m *Phaser) Latency() int              { return 0 }
func (m *Phaser) Process(buf []float64) {
	hi := PhaserMinHz * math.Pow(2, m.get("range"))
	m.p.Set(m.get("rate"), PhaserMinHz, hi, int(m.get("stages")), m.pct("feedback"), m.pct("mix"))
	m.p.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Delay

// Delay é o pedal de delay analógico. Tone 100% = repetições limpas; menos = mais escuras.
type Delay struct {
	base
	d *dsp.Delay
}

// NewDelay cria o módulo de delay.
func NewDelay() *Delay {
	return &Delay{base: base{id: "delay", name: "Delay", paramSet: newParamSet(
		ParamInfo{ID: "time", Name: "Time", Unit: "ms", Min: 20, Max: dsp.DelayMaxMs, Default: 380},
		ParamInfo{ID: "feedback", Name: "Feedback", Unit: "%", Min: 0, Max: 90, Default: 45},
		ParamInfo{ID: "tone", Name: "Tone", Unit: "%", Min: 10, Max: 100, Default: 65},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 35},
	)}}
}

func (m *Delay) Prepare(fs float64, _ int) { m.d = dsp.NewDelay(fs) }
func (m *Delay) Reset()                    { m.d.Reset() }
func (m *Delay) Latency() int              { return 0 }
func (m *Delay) Process(buf []float64) {
	m.d.Set(m.get("time"), m.pct("feedback"), 1-m.pct("tone"), m.pct("mix"))
	m.d.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Reverb

// Reverb é o pedal de reverb.
type Reverb struct {
	base
	r *dsp.Reverb
}

// NewReverb cria o módulo de reverb.
func NewReverb() *Reverb {
	return &Reverb{base: base{id: "reverb", name: "Reverb", paramSet: newParamSet(
		ParamInfo{ID: "size", Name: "Size", Unit: "%", Min: 0, Max: 98, Default: 80},
		ParamInfo{ID: "damping", Name: "Damping", Unit: "%", Min: 0, Max: 100, Default: 40},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 35},
	)}}
}

func (m *Reverb) Prepare(fs float64, _ int) { m.r = dsp.NewReverb(fs) }
func (m *Reverb) Reset()                    { m.r.Reset() }
func (m *Reverb) Latency() int              { return 0 }
func (m *Reverb) Process(buf []float64) {
	m.r.Set(m.pct("size"), m.pct("damping"), m.pct("mix"))
	m.r.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Compressor

// ParamShow é um controle visível agora, com o nome a mostrar.
type ParamShow struct{ ID, Name string }

// ParamViewer é implementado pelos módulos cujos controles mudam com o modo (ex.: o drive):
// devolve os parâmetros contínuos que valem no modo atual, na ordem, com o nome de cada um.
// Os que ficam de fora não afetam o som nesse modo. Os seletores (o próprio modo) sempre aparecem.
type ParamViewer interface {
	ParamView() []ParamShow
}

// GainReducer é implementado por módulos que reduzem ganho (para o medidor de GR na GUI).
type GainReducer interface {
	GainReductionDB() float64
}

// Compressor é o pedal de compressor.
type Compressor struct {
	base
	c  *dsp.Compressor
	gr atomic.Uint64 // float64 bits: maior redução de ganho do último bloco (dB)
}

// NewCompressor cria o módulo de compressor.
func NewCompressor() *Compressor {
	return &Compressor{base: base{id: "comp", name: "Compressor", paramSet: newParamSet(
		ParamInfo{ID: "threshold", Name: "Threshold", Unit: "dB", Min: -50, Max: 0, Default: -20},
		ParamInfo{ID: "ratio", Name: "Ratio", Unit: ":1", Min: 1, Max: 20, Default: 4},
		ParamInfo{ID: "attack", Name: "Attack", Unit: "ms", Min: 0.1, Max: 100, Default: 10},
		ParamInfo{ID: "release", Name: "Release", Unit: "ms", Min: 10, Max: 1000, Default: 150},
		ParamInfo{ID: "level", Name: "Level", Unit: "dB", Min: 0, Max: 24, Default: 6},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 100},
	)}}
}

func (m *Compressor) Prepare(fs float64, _ int) { m.c = dsp.NewCompressor(fs) }
func (m *Compressor) Reset()                    { m.c.Reset(); m.gr.Store(0) }
func (m *Compressor) Latency() int              { return 0 }
func (m *Compressor) Process(buf []float64) {
	m.c.Set(m.get("threshold"), m.get("ratio"), m.get("attack"), m.get("release"), m.get("level"), m.pct("mix"))
	m.c.ProcessBlock(buf)
	m.gr.Store(math.Float64bits(m.c.GainReductionDB()))
}

// GainReductionDB devolve a redução de ganho atual (seguro de outra goroutine).
func (m *Compressor) GainReductionDB() float64 { return math.Float64frombits(m.gr.Load()) }

// ---------------------------------------------------------------- Tremolo

// Tremolo é o pedal de tremolo (volume modulado).
type Tremolo struct {
	base
	t *dsp.Tremolo
}

// NewTremolo cria o módulo de tremolo.
func NewTremolo() *Tremolo {
	return &Tremolo{base: base{id: "tremolo", name: "Tremolo", paramSet: newParamSet(
		ParamInfo{ID: "rate", Name: "Rate", Unit: "Hz", Min: 0.5, Max: 15, Default: 5, Log: true},
		ParamInfo{ID: "depth", Name: "Depth", Unit: "%", Min: 0, Max: 100, Default: 50},
		ParamInfo{ID: "shape", Name: "Forma", Min: 0, Max: 2, Default: dsp.WaveSine, Step: 1,
			Choices: []string{"Seno", "Triângulo", "Quadrada"}},
	)}}
}

func (m *Tremolo) Prepare(fs float64, _ int) { m.t = dsp.NewTremolo(fs) }
func (m *Tremolo) Reset()                    { m.t.Reset() }
func (m *Tremolo) Latency() int              { return 0 }
func (m *Tremolo) Process(buf []float64) {
	m.t.Set(m.get("rate"), m.pct("depth"), int(m.get("shape")))
	m.t.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Vibrato

// Vibrato é o pedal de vibrato (tom modulado, sem o sinal seco). Latência 0, como o chorus:
// o atraso médio (de 2 amostras a ~2,5 ms) faz parte do efeito.
type Vibrato struct {
	base
	v *dsp.Vibrato
}

// NewVibrato cria o módulo de vibrato.
func NewVibrato() *Vibrato {
	return &Vibrato{base: base{id: "vibrato", name: "Vibrato", paramSet: newParamSet(
		ParamInfo{ID: "rate", Name: "Rate", Unit: "Hz", Min: 0.5, Max: 12, Default: 5, Log: true},
		ParamInfo{ID: "depth", Name: "Depth", Unit: "ms", Min: 0, Max: dsp.VibratoMaxMs, Default: 0.6},
	)}}
}

func (m *Vibrato) Prepare(fs float64, _ int) { m.v = dsp.NewVibrato(fs) }
func (m *Vibrato) Reset()                    { m.v.Reset() }
func (m *Vibrato) Latency() int              { return 0 }
func (m *Vibrato) Process(buf []float64) {
	m.v.Set(m.get("rate"), m.get("depth"))
	m.v.ProcessBlock(buf)
}
