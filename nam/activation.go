package nam

import (
	"encoding/json"
	"fmt"
	"math"
)

type actKind int

const (
	actTanh actKind = iota
	actHardtanh
	actFasttanh
	actReLU
	actLeakyReLU
	actPReLU
	actSigmoid
	actSiLU
	actHardswish
	actLeakyHardtanh
	actSoftsign
)

var actNames = map[string]actKind{
	"Tanh": actTanh, "Hardtanh": actHardtanh, "Fasttanh": actFasttanh, "ReLU": actReLU,
	"LeakyReLU": actLeakyReLU, "PReLU": actPReLU, "Sigmoid": actSigmoid, "SiLU": actSiLU,
	"Hardswish": actHardswish, "LeakyHardtanh": actLeakyHardtanh, "LeakyHardTanh": actLeakyHardtanh,
	"Softsign": actSoftsign,
}

// activation é uma função de ativação com seus parâmetros (os padrões são os da NAM core).
type activation struct {
	kind                   actKind
	name                   string
	slope                  float32   // LeakyReLU, PReLU com um só valor
	slopes                 []float32 // PReLU por canal
	minV, maxV, minS, maxS float32   // LeakyHardtanh
}

// parseActivation aceita "Tanh" ou {"type": "LeakyReLU", "negative_slope": 0.01}.
func parseActivation(raw json.RawMessage) (activation, error) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		k, ok := actNames[name]
		if !ok {
			return activation{}, fmt.Errorf("ativação desconhecida: %q", name)
		}
		return newActivation(k, name), nil
	}
	var obj struct {
		Type           string    `json:"type"`
		NegativeSlope  *float32  `json:"negative_slope"`
		NegativeSlopes []float32 `json:"negative_slopes"`
		MinVal         *float32  `json:"min_val"`
		MaxVal         *float32  `json:"max_val"`
		MinSlope       *float32  `json:"min_slope"`
		MaxSlope       *float32  `json:"max_slope"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return activation{}, fmt.Errorf("ativação inválida: %s", raw)
	}
	k, ok := actNames[obj.Type]
	if !ok {
		return activation{}, fmt.Errorf("ativação desconhecida: %q", obj.Type)
	}
	a := newActivation(k, obj.Type)
	set := func(dst *float32, p *float32) {
		if p != nil {
			*dst = *p
		}
	}
	switch k {
	case actLeakyReLU:
		set(&a.slope, obj.NegativeSlope)
	case actPReLU:
		if obj.NegativeSlope != nil {
			a.slope = *obj.NegativeSlope
		} else if len(obj.NegativeSlopes) > 0 {
			a.slopes = obj.NegativeSlopes
		}
	case actLeakyHardtanh:
		set(&a.minV, obj.MinVal)
		set(&a.maxV, obj.MaxVal)
		set(&a.minS, obj.MinSlope)
		set(&a.maxS, obj.MaxSlope)
	}
	return a, nil
}

func newActivation(k actKind, name string) activation {
	return activation{kind: k, name: name, slope: 0.01, minV: -1, maxV: 1, minS: 0.01, maxS: 0.01}
}

// checkChannels confere se um PReLU por canal tem um valor por canal.
func (a activation) checkChannels(n int) error {
	if a.kind == actPReLU && a.slopes != nil && len(a.slopes) != n {
		return fmt.Errorf("PReLU com %d inclinações aplicado a %d canais", len(a.slopes), n)
	}
	return nil
}

// apply aplica a ativação, in-place, a uma linha (um canal) do sinal.
func (a *activation) apply(x []float32, channel int) {
	switch a.kind {
	case actTanh:
		for i, v := range x {
			x[i] = float32(math.Tanh(float64(v)))
		}
	case actFasttanh:
		for i, v := range x {
			x[i] = fastTanh(v)
		}
	case actHardtanh:
		for i, v := range x {
			x[i] = min(max(v, -1), 1)
		}
	case actReLU:
		for i, v := range x {
			x[i] = max(v, 0)
		}
	case actLeakyReLU, actPReLU:
		s := a.slope
		if a.slopes != nil {
			s = a.slopes[channel]
		}
		for i, v := range x {
			if !(v > 0) {
				x[i] = s * v
			}
		}
	case actSigmoid:
		for i, v := range x {
			x[i] = sigmoid(v)
		}
	case actSiLU:
		for i, v := range x {
			x[i] = v * sigmoid(v)
		}
	case actHardswish:
		for i, v := range x {
			t := min(max(v+3, 0), 6)
			x[i] = v * t * (1.0 / 6)
		}
	case actLeakyHardtanh:
		for i, v := range x {
			switch {
			case v < a.minV:
				x[i] = (v-a.minV)*a.minS + a.minV
			case v > a.maxV:
				x[i] = (v-a.maxV)*a.maxS + a.maxV
			}
		}
	case actSoftsign:
		for i, v := range x {
			x[i] = v / (1 + float32(math.Abs(float64(v))))
		}
	}
}

func sigmoid(v float32) float32 { return float32(1 / (1 + math.Exp(-float64(v)))) }

// fastTanh é a aproximação racional de tanh da NAM core ("Fasttanh").
func fastTanh(x float32) float32 {
	ax := float32(math.Abs(float64(x)))
	x2 := x * x
	return x * (2.45550750702956 + 2.45550750702956*ax + (0.893229853513558+0.821226666969744*ax)*x2) /
		(2.44506634652299 + (2.44506634652299+x2)*float32(math.Abs(float64(x+0.814642734961073*x*ax))))
}
