package engine

import (
	"fmt"
	"math"

	"github.com/tatianno/ampsim/dsp"
)

// EQBands é o número de bandas do equalizador.
const EQBands = 5

// eqShelfQ é o Q fixo dos shelves (Butterworth: sem bossa na transição).
const eqShelfQ = 1 / math.Sqrt2

// eqBand descreve uma banda: tipo, faixa e padrão da frequência.
type eqBand struct {
	kind          int // 0 = shelf de graves, 1 = pico, 2 = shelf de agudos
	fMin, fMax, f float64
}

const (
	eqLowShelf = iota
	eqPeak
	eqHighShelf
)

var eqLayout = [EQBands]eqBand{
	{eqLowShelf, 40, 500, 100},
	{eqPeak, 80, 2000, 250},
	{eqPeak, 200, 5000, 800},
	{eqPeak, 500, 10000, 2500},
	{eqHighShelf, 1500, 12000, 6000},
}

// EQ é o equalizador paramétrico de 5 bandas: shelf de graves, 3 picos com Q ajustável e
// shelf de agudos, mais o nível de saída. Os parâmetros são f1..f5 (Hz), g1..g5 (dB),
// q2..q4 e level (dB).
type EQ struct {
	base
	fs      float64
	filters [EQBands]dsp.Biquad
	applied [EQBands][3]float64 // f, g, q em uso (recalcula os coeficientes só quando mudam)
	level   dsp.Smoother
	pf, pg  [EQBands]*Param
	pq      [EQBands]*Param // nil nos shelves
}

// NewEQ cria o equalizador.
func NewEQ() *EQ {
	var infos []ParamInfo
	for i, b := range eqLayout {
		n := i + 1
		infos = append(infos,
			ParamInfo{ID: fmt.Sprintf("f%d", n), Name: "Freq", Unit: "Hz", Min: b.fMin, Max: b.fMax, Default: b.f, Log: true},
			ParamInfo{ID: fmt.Sprintf("g%d", n), Name: "Ganho", Unit: "dB", Min: -15, Max: 15, Default: 0})
		if b.kind == eqPeak {
			infos = append(infos, ParamInfo{ID: fmt.Sprintf("q%d", n), Name: "Q", Min: 0.3, Max: 10, Default: 1, Log: true})
		}
	}
	infos = append(infos, ParamInfo{ID: "level", Name: "Level", Unit: "dB", Min: -12, Max: 12, Default: 0})
	m := &EQ{base: base{id: "eq", name: "EQ", paramSet: newParamSet(infos...)}}
	for i := range eqLayout {
		n := i + 1
		m.pf[i], m.pg[i] = m.Param(fmt.Sprintf("f%d", n)), m.Param(fmt.Sprintf("g%d", n))
		m.pq[i] = m.Param(fmt.Sprintf("q%d", n))
	}
	return m
}

// band devolve f, g e q da banda i (0..4) a partir dos parâmetros.
func (m *EQ) band(i int) (f, g, q float64) {
	f, g, q = m.pf[i].Get(), m.pg[i].Get(), eqShelfQ
	if m.pq[i] != nil {
		q = m.pq[i].Get()
	}
	return
}

// eqCoefs projeta a banda i.
func eqCoefs(i int, fs, f, g, q float64) dsp.Coefs {
	f = math.Min(f, 0.45*fs) // seguro em taxas baixas
	switch eqLayout[i].kind {
	case eqLowShelf:
		return dsp.LowShelf(fs, f, q, g)
	case eqHighShelf:
		return dsp.HighShelf(fs, f, q, g)
	}
	return dsp.Peaking(fs, f, q, g)
}

func (m *EQ) Prepare(fs float64, _ int) {
	m.fs = fs
	m.level = dsp.NewSmoother(fs, 20)
	m.level.SetTarget(math.Pow(10, m.get("level")/20))
	for i := range m.filters {
		m.applied[i] = [3]float64{-1, 0, 0} // força o cálculo no primeiro bloco
	}
	m.update()
}

func (m *EQ) Reset() {
	for i := range m.filters {
		m.filters[i].Reset()
	}
}

func (m *EQ) Latency() int { return 0 }

// update recalcula os coeficientes das bandas que mudaram (sem alocar).
func (m *EQ) update() {
	for i := range m.filters {
		f, g, q := m.band(i)
		if p := [3]float64{f, g, q}; p != m.applied[i] {
			m.filters[i].SetCoefs(eqCoefs(i, m.fs, f, g, q))
			m.applied[i] = p
		}
	}
}

func (m *EQ) Process(buf []float64) {
	m.update()
	for i := range m.filters {
		m.filters[i].ProcessBlock(buf)
	}
	m.level.SetTarget(math.Pow(10, m.get("level")/20))
	for i := range buf {
		buf[i] *= m.level.Next()
	}
}

// ResponseDB calcula a resposta do EQ (dB, incluindo o level) nas frequências freqs, com os
// parâmetros atuais, e grava em out. Para desenhar a curva; seguro de outra goroutine.
func (m *EQ) ResponseDB(freqs, out []float64) {
	fs := m.fs
	if fs == 0 {
		fs = 48000
	}
	var cs [EQBands]dsp.Coefs
	for i := range cs {
		f, g, q := m.band(i)
		cs[i] = eqCoefs(i, fs, f, g, q)
	}
	lvl := m.get("level")
	for k, f := range freqs {
		s := lvl
		if f < fs/2 {
			for _, c := range cs {
				s += c.MagnitudeDB(f, fs)
			}
		}
		out[k] = s
	}
}
