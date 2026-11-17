// Pacote engine monta a cadeia de efeitos sobre o pacote dsp, com parâmetros
// descritos em tabela (que geram os knobs da GUI e, no futuro, os parâmetros do plugin)
// e estado serializável em JSON (presets).
//
// O engine não sabe de onde vem o áudio (guitarra, arquivo ou DAW).
package engine

import (
	"math"
	"sync/atomic"
)

// ParamInfo descreve um parâmetro (um knob ou chave).
type ParamInfo struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Unit    string  `json:"unit,omitempty"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"default"`
	Step    float64 `json:"step,omitempty"` // 0 = contínuo; > 0 = valores discretos (ex.: estágios do phaser)
}

// Param é um parâmetro com valor atômico: a GUI escreve, a thread de áudio lê, sem trava.
type Param struct {
	info ParamInfo
	bits atomic.Uint64
}

// NewParam cria um parâmetro com o valor padrão.
func NewParam(info ParamInfo) *Param {
	p := &Param{info: info}
	p.Set(info.Default)
	return p
}

// Info devolve a descrição do parâmetro.
func (p *Param) Info() ParamInfo { return p.info }

// Get lê o valor atual.
func (p *Param) Get() float64 { return math.Float64frombits(p.bits.Load()) }

// Set ajusta o valor, limitado à faixa e arredondado ao passo.
func (p *Param) Set(v float64) {
	if math.IsNaN(v) {
		return
	}
	if p.info.Step > 0 {
		v = p.info.Min + math.Round((v-p.info.Min)/p.info.Step)*p.info.Step
	}
	v = math.Max(p.info.Min, math.Min(p.info.Max, v))
	p.bits.Store(math.Float64bits(v))
}

// Reset volta ao valor padrão.
func (p *Param) Reset() { p.Set(p.info.Default) }

// Normalized devolve o valor em 0..1 (útil para desenhar knobs).
func (p *Param) Normalized() float64 {
	return (p.Get() - p.info.Min) / (p.info.Max - p.info.Min)
}

// SetNormalized ajusta a partir de 0..1.
func (p *Param) SetNormalized(n float64) {
	p.Set(p.info.Min + n*(p.info.Max-p.info.Min))
}

// paramSet agrupa os parâmetros de um módulo.
type paramSet struct {
	list []*Param
	byID map[string]*Param
}

func newParamSet(infos ...ParamInfo) paramSet {
	ps := paramSet{byID: make(map[string]*Param, len(infos))}
	for _, in := range infos {
		p := NewParam(in)
		ps.list = append(ps.list, p)
		ps.byID[in.ID] = p
	}
	return ps
}

// Params devolve os parâmetros na ordem de exibição.
func (ps *paramSet) Params() []*Param { return ps.list }

// Param busca um parâmetro pelo ID (nil se não existir).
func (ps *paramSet) Param(id string) *Param { return ps.byID[id] }

func (ps *paramSet) get(id string) float64 { return ps.byID[id].Get() }
