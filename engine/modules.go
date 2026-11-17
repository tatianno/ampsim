package engine

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"ampsim/dsp"
	"ampsim/wav"
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

// Amp é o cabeçote + gabinete:
//
//	HP 70 Hz → GANHO/saturação 4x → tone stack → power amp (tanh) → presença
//	→ IR do gabinete → HP 85 Hz / LP 6,2 kHz → volume
type Amp struct {
	base
	fs float64

	tight    dsp.Biquad
	pre      *dsp.Preamp
	tone     *dsp.ToneStack
	presence *dsp.Presence
	cabHP    dsp.Cascade
	cabLP    dsp.Cascade
	volume   dsp.Smoother

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

// NewAmp cria o amp.
func NewAmp() *Amp {
	return &Amp{base: base{id: "amp", name: "Amp", paramSet: newParamSet(
		ParamInfo{ID: "gain", Name: "Ganho", Min: 0, Max: 10, Default: 6.5},
		ParamInfo{ID: "bass", Name: "Grave", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "mid", Name: "Médio", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "treble", Name: "Agudo", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "presence", Name: "Presença", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "volume", Name: "Volume", Unit: "dB", Min: -40, Max: 12, Default: -12},
	)}}
}

func (m *Amp) Prepare(fs float64, _ int) {
	m.fs = fs
	m.tight.SetCoefs(dsp.OnePoleHighpass(fs, 70))
	m.pre = dsp.NewPreamp(fs)
	m.tone = dsp.NewToneStack(fs)
	m.presence = dsp.NewPresence(fs)
	m.cabHP = dsp.NewCascade(dsp.ButterworthHighpass(2, fs, 85))
	m.cabLP = dsp.NewCascade(dsp.ButterworthLowpass(3, fs, 6200))
	m.volume = dsp.NewSmoother(fs, 20)

	m.mu.Lock()
	m.prepFS = fs
	m.conv = nil
	if m.irRaw != nil {
		m.conv = dsp.NewConvolver(dsp.PrepareIR(m.irRaw, m.irFS, int(fs), IRMaxMs), IRPartition)
	}
	m.pending.Store(nil)
	m.mu.Unlock()
}

func (m *Amp) Reset() {
	m.tight.Reset()
	m.pre.Reset()
	m.tone.Reset()
	m.presence.Reset()
	m.cabHP.Reset()
	m.cabLP.Reset()
	if m.conv != nil {
		m.conv.Reset()
	}
}

func (m *Amp) Latency() int { return dsp.PreampLatency }

func (m *Amp) Process(buf []float64) {
	if c := m.pending.Swap(nil); c != nil {
		m.conv = c
	}
	m.pre.SetDrive(m.get("gain") / 10)
	m.tone.Set(m.get("bass"), m.get("mid"), m.get("treble"))
	m.presence.Set(m.get("presence"))
	m.volume.SetTarget(math.Pow(10, m.get("volume")/20))

	m.tight.ProcessBlock(buf)
	m.pre.ProcessBlock(buf)
	m.tone.ProcessBlock(buf)
	for i, x := range buf {
		buf[i] = math.Tanh(x) // power amp
	}
	m.presence.ProcessBlock(buf)
	if m.conv != nil {
		m.conv.ProcessBlock(buf)
	}
	m.cabHP.ProcessBlock(buf)
	m.cabLP.ProcessBlock(buf)
	for i := range buf {
		buf[i] *= m.volume.Next()
	}
}

// LoadIR carrega uma IR (amostras brutas e sua taxa). Pode ser chamado com o áudio
// rodando: a IR é preparada aqui e trocada de forma atômica no próximo bloco.
func (m *Amp) LoadIR(raw []float64, fsIR int, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.irRaw, m.irFS, m.irPath = raw, fsIR, path
	if m.prepFS > 0 {
		m.pending.Store(dsp.NewConvolver(dsp.PrepareIR(raw, fsIR, int(m.prepFS), IRMaxMs), IRPartition))
	}
}

// LoadIRFile carrega a IR de um arquivo WAV.
func (m *Amp) LoadIRFile(path string) error {
	fs, raw, err := wav.ReadMonoFile(path)
	if err != nil {
		return fmt.Errorf("carregando IR: %w", err)
	}
	m.LoadIR(raw, fs, path)
	return nil
}

// IRPath devolve o caminho da IR carregada ("" se nenhuma).
func (m *Amp) IRPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.irPath
}

func (m *Amp) ExtraState() map[string]string {
	if p := m.IRPath(); p != "" {
		return map[string]string{"ir": p}
	}
	return nil
}

func (m *Amp) ApplyExtraState(s map[string]string) error {
	if p := s["ir"]; p != "" && p != m.IRPath() {
		return m.LoadIRFile(p)
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
