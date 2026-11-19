package plugin

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/tatianno/ampsim/engine"
)

// Pedal é um módulo da cadeia (pedal ou modulação) oferecido sozinho como plugin, com os
// knobs dele.
type Pedal struct {
	ID     string // id do módulo (engine): "chorus", "delay"…
	Name   string
	Stereo bool               // a variante estéreo (com Largura e o modo estéreo do delay)
	Params []engine.ParamInfo // os parâmetros expostos, na ordem do módulo
}

// hidden são os parâmetros que o plugin de pedal não expõe, além dos só de estéreo na
// variante mono (engine.StereoOnly): o drive fica no modo modelado (o tipo NAM depende de
// uma captura, que não tem seletor de arquivo na UI genérica).
var hidden = map[string][]string{
	"drive": {"type", "nam_input", "nam_size"},
}

// Pedals devolve os pedais e modulações, na ordem padrão da cadeia (o amp e o gabinete
// ficam de fora: são oferecidos pelos presets), na variante mono ou estéreo.
func Pedals(stereo bool) []Pedal {
	c := engine.NewDefaultChain()
	var out []Pedal
	for _, id := range engine.DefaultOrder {
		if engine.Required(id) {
			continue
		}
		m := c.Module(id)
		p := Pedal{ID: id, Name: m.Name(), Stereo: stereo}
		for _, q := range m.Params() {
			info := q.Info()
			if (!stereo && engine.StereoOnly(info.ID)) || slices.Contains(hidden[id], info.ID) {
				continue
			}
			p.Params = append(p.Params, info)
		}
		out = append(out, p)
	}
	return out
}

// FindPedal devolve o pedal id, na variante mono ou estéreo.
func FindPedal(id string, stereo bool) (Pedal, bool) {
	for _, p := range Pedals(stereo) {
		if p.ID == id {
			return p, true
		}
	}
	return Pedal{}, false
}

// Label devolve o nome do parâmetro i para a UI genérica do host: com o número da banda
// quando o nome se repete no pedal (EQ: "Freq 2"), "Passo n" na sequência do synth, e a
// legenda das posições num seletor ("Modo (0 Envelope, 1 LFO, 2 Fixo)"; com duas posições,
// o nome da ligada: "Velocidade (Rápido)", ver Toggle).
func (p Pedal) Label(i int) string {
	info := p.Params[i]
	name := info.Name
	if isNumber(name) {
		name = "Passo " + name
	} else if p.repeated(name) {
		name += " " + strings.TrimLeftFunc(info.ID, func(r rune) bool { return !unicode.IsDigit(r) })
	}
	switch {
	case Toggle(info):
		return fmt.Sprintf("%s (%s)", name, info.Choices[1])
	case len(info.Choices) > 0:
		var leg []string
		for k, c := range info.Choices {
			leg = append(leg, fmt.Sprintf("%g %s", info.Min+float64(k)*info.Step, c))
		}
		return fmt.Sprintf("%s (%s)", name, strings.Join(leg, ", "))
	case info.Step > 1: // valores discretos com passo (estágios do phaser: 4, 6, 8)
		var vs []string
		for v := info.Min; v <= info.Max+1e-9; v += info.Step {
			vs = append(vs, fmt.Sprintf("%g", v))
		}
		return fmt.Sprintf("%s (%s)", name, strings.Join(vs, ", "))
	}
	return name
}

// Toggle diz se o parâmetro vira uma caixa de marcar: um seletor de duas posições (a
// desligada é a primeira).
func Toggle(info engine.ParamInfo) bool {
	return len(info.Choices) == 2 && info.Min == 0 && info.Max == 1
}

func (p Pedal) repeated(name string) bool {
	n := 0
	for _, q := range p.Params {
		if q.Name == name {
			n++
		}
	}
	return n > 1
}

func isNumber(s string) bool {
	return s != "" && strings.TrimFunc(s, unicode.IsDigit) == ""
}

// NewPedal cria uma instância do pedal id à taxa fs: a cadeia tem só esse módulo, e
// Settings.Params traz os valores dos knobs (na ordem de Pedal.Params) a cada bloco. Em
// estéreo, a entrada é misturada em mono e o módulo sai em dois canais (como na app).
func NewPedal(fs float64, id string, stereo bool) (*Instance, error) {
	p, ok := FindPedal(id, stereo)
	if !ok {
		return nil, fmt.Errorf("pedal %q não existe", id)
	}
	m := engine.NewDefaultChain().Module(id) // um módulo novo (a cadeia padrão é descartada)
	in := newInstance(fs, stereo)
	in.pedal = true
	c := engine.NewChain(m)
	c.SetStereo(stereo)
	c.Prepare(fs, MaxBlock)
	in.chain = c
	for _, info := range p.Params {
		in.params = append(in.params, m.Param(info.ID))
	}
	in.last = make([]float64, len(in.params))
	for i := range in.last {
		in.last[i] = -1e300 // força aplicar no primeiro bloco
	}
	return in, nil
}

// Activate prepara a instância para tocar do zero (o activate do LV2, antes do primeiro
// bloco): num pedal, aplica os knobs params (na ordem de Pedal.Params) e prepara a cadeia com
// eles, para o som sair certo desde a primeira amostra (sem a rampa a partir dos valores
// padrão); num preset, limpa as caudas. Aloca: não chamar na thread de áudio.
func (in *Instance) Activate(params []float64) {
	if !in.pedal {
		in.chain.Reset()
		return
	}
	for i, v := range params[:min(len(params), len(in.params))] {
		in.params[i].Set(v)
		in.last[i] = v
	}
	in.chain.Prepare(in.fs, MaxBlock)
}
