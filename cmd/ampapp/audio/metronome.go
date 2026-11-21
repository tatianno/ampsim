package audio

import (
	"math"
	"sync/atomic"

	"github.com/tatianno/ampsim/engine"
)

// Faixas do metrônomo.
const (
	MetroMinBPM     = 30.0
	MetroMaxBPM     = 300.0
	MetroDefaultBPM = 120.0
	metroClickMs    = 30.0   // duração de cada clique
	metroAccentHz   = 1600.0 // primeiro tempo do compasso
	metroBeatHz     = 1000.0 // demais tempos
)

// Metronome é o clique de andamento. Entra só na saída para a placa, depois do gravador e
// do analisador: ouve-se enquanto grava, mas o clique não vai para o arquivo.
type Metronome struct {
	r *Renderer

	BPM    *engine.Param // andamento
	Beats  *engine.Param // tempos por compasso (1 = sem acento)
	Volume *engine.Param // nível do clique (dBFS de pico)

	on      atomic.Bool
	restart atomic.Bool   // pedido de recomeçar do 1º tempo (thread de áudio consome)
	count   atomic.Uint64 // quantos cliques já tocaram (a GUI pisca o LED quando muda)
	beat    atomic.Int32  // tempo do compasso do último clique (0 = acento)

	// thread de áudio
	accent, normal []float64
	phase          float64 // amostras desde o último clique
	next           int     // tempo do compasso do próximo clique
	click          []float64
	pos            int
}

// NewMetronome cria o metrônomo (desligado) e o liga ao render.
func NewMetronome(r *Renderer) *Metronome {
	m := &Metronome{
		r: r,
		BPM: engine.NewParam(engine.ParamInfo{ID: "bpm", Name: "Andamento", Unit: "BPM",
			Min: MetroMinBPM, Max: MetroMaxBPM, Default: MetroDefaultBPM, Step: 1}),
		Beats: engine.NewParam(engine.ParamInfo{ID: "compasso", Name: "Compasso", Min: 1, Max: 7, Default: 4, Step: 1,
			Choices: []string{"sem", "2", "3", "4", "5", "6", "7"}}),
		Volume: engine.NewParam(engine.ParamInfo{ID: "volume", Name: "Volume", Unit: "dB", Min: -40, Max: 0, Default: -12}),
	}
	m.accent = clickSound(r.SampleRate(), metroAccentHz)
	m.normal = clickSound(r.SampleRate(), metroBeatHz)
	r.metro.Store(m)
	return m
}

// clickSound é um clique curto: senoide com ataque de 0,5 ms, queda exponencial e os
// últimos 5 ms em fade (o corte no fim não estala).
func clickSound(fs, hz float64) []float64 {
	n := int(metroClickMs / 1000 * fs)
	out := make([]float64, n)
	attack := 0.0005 * fs
	tau := 0.006 * fs
	fade := int(0.005 * fs)
	for i := range out {
		env := math.Exp(-float64(i) / tau)
		if a := float64(i) / attack; a < 1 {
			env *= a
		}
		if k := n - 1 - i; k < fade {
			env *= 0.5 - 0.5*math.Cos(math.Pi*float64(k)/float64(fade))
		}
		out[i] = env * math.Sin(2*math.Pi*hz*float64(i)/fs)
	}
	return out
}

// Start liga o metrônomo, começando pelo 1º tempo do compasso.
func (m *Metronome) Start() {
	m.restart.Store(true)
	m.on.Store(true)
}

// Stop desliga o metrônomo.
func (m *Metronome) Stop() { m.on.Store(false) }

// Running informa se o metrônomo está ligado.
func (m *Metronome) Running() bool { return m.on.Load() }

// Count devolve quantos cliques já tocaram e o tempo do compasso do último (0 = acento).
func (m *Metronome) Count() (n uint64, beat int) { return m.count.Load(), int(m.beat.Load()) }

// mix soma o clique em L e R (thread de áudio). Com silent (saída muda), o andamento
// continua correndo, mas nada é somado.
func (m *Metronome) mix(left, right []float64, silent bool) {
	if !m.on.Load() {
		m.click = nil
		return
	}
	period := m.r.SampleRate() * 60 / m.BPM.Get()
	if m.restart.Swap(false) {
		m.phase, m.next, m.click = period, 0, nil // o 1º tempo toca já
	}
	gain := math.Pow(10, m.Volume.Get()/20)
	beats := int(m.Beats.Get())
	for i := range left {
		if m.phase >= period {
			m.phase -= period
			if m.phase >= period { // o andamento subiu muito: não acumula cliques atrasados
				m.phase = 0
			}
			if m.next >= beats {
				m.next = 0
			}
			m.click, m.pos = m.normal, 0
			if m.next == 0 && beats > 1 {
				m.click = m.accent
			}
			m.beat.Store(int32(m.next))
			m.count.Add(1)
			m.next++
		}
		m.phase++
		if m.click == nil {
			continue
		}
		if !silent {
			v := gain * m.click[m.pos]
			left[i] += v
			right[i] += v
		}
		if m.pos++; m.pos >= len(m.click) {
			m.click = nil
		}
	}
}
