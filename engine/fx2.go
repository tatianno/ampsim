package engine

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/tatianno/ampsim/dsp"
)

// ---------------------------------------------------------------- Drive

// Drive é o pedal de drive: Boost, Overdrive, Distorção ou Fuzz. A troca de modo faz um
// crossfade de 20 ms (os dois modos rodam juntos durante a troca).
type Drive struct {
	base
	cur, old  *dsp.Drive
	mode      int
	xf, xstep float64
	scratch   []float64
}

// NewDrive cria o módulo de drive.
func NewDrive() *Drive {
	return &Drive{base: base{id: "drive", name: "Drive", paramSet: newParamSet(
		ParamInfo{ID: "drive", Name: "Drive", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "tone", Name: "Tone", Min: 0, Max: 10, Default: 5},
		ParamInfo{ID: "level", Name: "Level", Unit: "dB", Min: -24, Max: 12, Default: 0},
		ParamInfo{ID: "mode", Name: "Modo", Min: 0, Max: 3, Default: dsp.DriveOverdrive, Step: 1,
			Choices: []string{"Boost", "Overdrive", "Distorção", "Fuzz"}},
	)}}
}

func (m *Drive) Prepare(fs float64, maxBlock int) {
	m.cur, m.old = dsp.NewDrive(fs), dsp.NewDrive(fs)
	m.mode = int(m.get("mode"))
	m.cur.SetMode(m.mode)
	m.xf, m.xstep = 1, 1/(AmpFadeMs/1000*fs)
	m.scratch = make([]float64, maxBlock)
}

func (m *Drive) Reset()       { m.cur.Reset(); m.xf = 1 }
func (m *Drive) Latency() int { return dsp.DriveLatency }

func (m *Drive) Process(buf []float64) {
	if mode := int(m.get("mode")); mode != m.mode && m.xf >= 1 {
		m.old, m.cur = m.cur, m.old
		m.cur.SetMode(mode)
		m.cur.Reset()
		m.mode, m.xf = mode, 0
	}
	drive, tone, lvl := m.get("drive"), m.get("tone"), m.get("level")
	m.cur.Set(drive, tone, lvl)
	if m.xf >= 1 {
		m.cur.ProcessBlock(buf)
		return
	}
	old := m.scratch[:len(buf)]
	copy(old, buf)
	m.old.Set(drive, tone, lvl)
	m.old.ProcessBlock(old)
	m.cur.ProcessBlock(buf)
	for i := range buf {
		m.xf = math.Min(1, m.xf+m.xstep)
		buf[i] = m.xf*buf[i] + (1-m.xf)*old[i]
	}
}

// ---------------------------------------------------------------- Wah

// Wah é o wah automático: segue a palhetada (Envelope), varre sozinho (LFO) ou fica parado
// numa posição (Fixo).
type Wah struct {
	base
	w *dsp.Wah
}

// NewWah cria o módulo de wah.
func NewWah() *Wah {
	return &Wah{base: base{id: "wah", name: "Wah", paramSet: newParamSet(
		ParamInfo{ID: "sens", Name: "Sensib.", Unit: "%", Min: 0, Max: 100, Default: 60},
		ParamInfo{ID: "range", Name: "Faixa", Unit: "%", Min: 0, Max: 100, Default: 50},
		ParamInfo{ID: "q", Name: "Q", Min: 1, Max: 12, Default: 4, Log: true},
		ParamInfo{ID: "response", Name: "Resposta", Unit: "ms", Min: 40, Max: 600, Default: 150, Log: true},
		ParamInfo{ID: "rate", Name: "Rate", Unit: "Hz", Min: 0.2, Max: 8, Default: 1.5, Log: true},
		ParamInfo{ID: "position", Name: "Posição", Unit: "%", Min: 0, Max: 100, Default: 50},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 100},
		ParamInfo{ID: "mode", Name: "Modo", Min: 0, Max: 2, Default: dsp.WahEnvelope, Step: 1,
			Choices: []string{"Envelope", "LFO", "Fixo"}},
	)}}
}

func (m *Wah) Prepare(fs float64, _ int) { m.w = dsp.NewWah(fs) }
func (m *Wah) Reset()                    { m.w.Reset() }
func (m *Wah) Latency() int              { return 0 }
func (m *Wah) Process(buf []float64) {
	m.w.SetResponse(m.get("response"))
	m.w.Set(int(m.get("mode")), m.pct("sens"), m.pct("range"), m.get("q"), m.get("rate"), m.pct("position"), m.pct("mix"))
	m.w.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Octave

// Octave é o oitavador (uma e duas oitavas abaixo, uma acima), monofônico como o OC-2.
type Octave struct {
	base
	o *dsp.Octaver
}

// NewOctave cria o módulo de oitavador.
func NewOctave() *Octave {
	return &Octave{base: base{id: "octave", name: "Octave", paramSet: newParamSet(
		ParamInfo{ID: "sub1", Name: "−1 oit.", Unit: "%", Min: 0, Max: 100, Default: 70},
		ParamInfo{ID: "sub2", Name: "−2 oit.", Unit: "%", Min: 0, Max: 100, Default: 0},
		ParamInfo{ID: "up", Name: "+1 oit.", Unit: "%", Min: 0, Max: 100, Default: 0},
		ParamInfo{ID: "dry", Name: "Seco", Unit: "%", Min: 0, Max: 100, Default: 100},
	)}}
}

func (m *Octave) Prepare(fs float64, _ int) { m.o = dsp.NewOctaver(fs) }
func (m *Octave) Reset()                    { m.o.Reset() }
func (m *Octave) Latency() int              { return 0 }
func (m *Octave) Process(buf []float64) {
	m.o.Set(m.pct("sub1"), m.pct("sub2"), m.pct("up"), m.pct("dry"))
	m.o.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Rotary

// Rotary é a caixa rotativa (Leslie): Lento/Rápido com aceleração.
type Rotary struct {
	base
	r *dsp.Rotary
}

// NewRotary cria o módulo da caixa rotativa.
func NewRotary() *Rotary {
	return &Rotary{base: base{id: "rotary", name: "Rotary", paramSet: newParamSet(
		ParamInfo{ID: "depth", Name: "Depth", Unit: "%", Min: 0, Max: 100, Default: 80},
		ParamInfo{ID: "balance", Name: "Corneta", Unit: "%", Min: 0, Max: 100, Default: 60},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 100},
		ParamInfo{ID: "speed", Name: "Velocidade", Min: 0, Max: 1, Default: 0, Step: 1,
			Choices: []string{"Lento", "Rápido"}},
	)}}
}

func (m *Rotary) Prepare(fs float64, _ int) { m.r = dsp.NewRotary(fs) }
func (m *Rotary) Reset()                    { m.r.Reset() }
func (m *Rotary) Latency() int              { return 0 }
func (m *Rotary) Process(buf []float64) {
	m.r.Set(m.get("speed") >= 1, m.pct("depth"), m.pct("balance"), m.pct("mix"))
	m.r.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Vibe

// Vibe é o modulador de fase com lâmpada (estilo Uni-Vibe), nos modos Chorus e Vibrato.
type Vibe struct {
	base
	v *dsp.Vibe
}

// NewVibe cria o módulo de vibe.
func NewVibe() *Vibe {
	return &Vibe{base: base{id: "vibe", name: "Vibe", paramSet: newParamSet(
		ParamInfo{ID: "rate", Name: "Speed", Unit: "Hz", Min: 0.5, Max: 10, Default: 2, Log: true},
		ParamInfo{ID: "intensity", Name: "Intensity", Unit: "%", Min: 0, Max: 100, Default: 70},
		ParamInfo{ID: "mode", Name: "Modo", Min: 0, Max: 1, Default: 0, Step: 1,
			Choices: []string{"Chorus", "Vibrato"}},
	)}}
}

func (m *Vibe) Prepare(fs float64, _ int) { m.v = dsp.NewVibe(fs) }
func (m *Vibe) Reset()                    { m.v.Reset() }
func (m *Vibe) Latency() int              { return 0 }
func (m *Vibe) Process(buf []float64) {
	m.v.Set(m.get("rate"), m.pct("intensity"), m.get("mode") >= 1)
	m.v.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Pitch

// Pitch é o pitch shifter / harmonizador: duas vozes em intervalos fixos (±1 oitava),
// com detune opcional.
type Pitch struct {
	base
	p *dsp.PitchShifter
}

// NewPitch cria o módulo de pitch shifter.
func NewPitch() *Pitch {
	return &Pitch{base: base{id: "pitch", name: "Pitch", paramSet: newParamSet(
		ParamInfo{ID: "voice1", Name: "Voz 1", Unit: "st", Min: -12, Max: 12, Default: 12, Step: 1},
		ParamInfo{ID: "voice2", Name: "Voz 2", Unit: "st", Min: -12, Max: 12, Default: -12, Step: 1},
		ParamInfo{ID: "level2", Name: "Nível V2", Unit: "%", Min: 0, Max: 100, Default: 0},
		ParamInfo{ID: "detune", Name: "Detune", Unit: "cents", Min: 0, Max: 30, Default: 0},
		ParamInfo{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 50},
	)}}
}

func (m *Pitch) Prepare(fs float64, _ int) { m.p = dsp.NewPitchShifter(fs) }
func (m *Pitch) Reset()                    { m.p.Reset() }
func (m *Pitch) Latency() int              { return 0 }
func (m *Pitch) Process(buf []float64) {
	det := m.get("detune")
	m.p.Set(dsp.Semitones(m.get("voice1"), det), dsp.Semitones(m.get("voice2"), -det), m.pct("level2"), m.pct("mix"))
	m.p.ProcessBlock(buf)
}

// ---------------------------------------------------------------- Synth

// Synth é o filtro de sintetizador: ladder de 4 polos (LP/BP/HP) com ressonância, o corte
// modulado por envelope, LFO ou uma sequência de 8 passos (s1..s8), e FM pelo próprio sinal.
type Synth struct {
	base
	f     *dsp.SynthFilter
	steps [dsp.SynthSteps]*Param
	step  atomic.Int32
}

// SynthStepDefaults é a sequência inicial (em %).
var SynthStepDefaults = [dsp.SynthSteps]float64{100, 25, 60, 10, 85, 40, 70, 15}

// NewSynth cria o módulo do filtro de sintetizador.
func NewSynth() *Synth {
	infos := []ParamInfo{
		{ID: "cutoff", Name: "Cutoff", Unit: "Hz", Min: 60, Max: 8000, Default: 300, Log: true},
		{ID: "res", Name: "Resson.", Unit: "%", Min: 0, Max: 100, Default: 60},
		{ID: "depth", Name: "Profund.", Unit: "oct", Min: 0, Max: 5, Default: 3},
		{ID: "fm", Name: "FM", Unit: "%", Min: 0, Max: 100, Default: 0},
		{ID: "sens", Name: "Sensib.", Unit: "%", Min: 0, Max: 100, Default: 60},
		{ID: "rate", Name: "Rate", Unit: "Hz", Min: 0.1, Max: 20, Default: 8, Log: true},
		{ID: "glide", Name: "Glide", Unit: "ms", Min: 0, Max: 200, Default: 20},
		{ID: "drive", Name: "Drive", Unit: "dB", Min: 0, Max: 24, Default: 0},
		{ID: "mix", Name: "Mix", Unit: "%", Min: 0, Max: 100, Default: 100},
		{ID: "level", Name: "Level", Unit: "dB", Min: -12, Max: 12, Default: 0},
		{ID: "mode", Name: "Modo", Min: 0, Max: 2, Default: dsp.SynthEnvelope, Step: 1,
			Choices: []string{"Envelope", "LFO", "Sequência"}},
		{ID: "type", Name: "Filtro", Min: 0, Max: 2, Default: dsp.SynthLP, Step: 1,
			Choices: []string{"LP", "BP", "HP"}},
	}
	for i, v := range SynthStepDefaults {
		infos = append(infos, ParamInfo{ID: fmt.Sprintf("s%d", i+1), Name: fmt.Sprintf("%d", i+1), Unit: "%", Min: 0, Max: 100, Default: v})
	}
	m := &Synth{base: base{id: "synth", name: "Synth", paramSet: newParamSet(infos...)}}
	for i := range m.steps {
		m.steps[i] = m.Param(fmt.Sprintf("s%d", i+1))
	}
	return m
}

func (m *Synth) Prepare(fs float64, _ int) { m.f = dsp.NewSynthFilter(fs) }
func (m *Synth) Reset()                    { m.f.Reset() }
func (m *Synth) Latency() int              { return dsp.SynthLatency }

// CurrentStep devolve o passo da sequência que está tocando (seguro de outra goroutine).
func (m *Synth) CurrentStep() int { return int(m.step.Load()) }

// Steps devolve os parâmetros dos passos (s1..s8).
func (m *Synth) Steps() [dsp.SynthSteps]*Param { return m.steps }

func (m *Synth) Process(buf []float64) {
	var st [dsp.SynthSteps]float64
	for i, p := range m.steps {
		st[i] = p.Get() / 100
	}
	m.f.SetSteps(st, m.get("glide"))
	m.f.Set(int(m.get("mode")), int(m.get("type")), m.get("cutoff"), m.pct("res"), m.get("depth"), m.pct("sens"),
		m.get("rate"), m.pct("fm"), m.get("drive"), m.pct("mix"), m.get("level"))
	m.f.ProcessBlock(buf)
	m.step.Store(int32(m.f.Step()))
}
