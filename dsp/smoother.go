package dsp

import "math"

// Smoother suaviza um parâmetro com um filtro de 1 polo, evitando "zipper noise"
// quando um knob é girado. Na primeira vez (ou após Snap) o valor vai direto ao alvo.
type Smoother struct {
	y, target, coef float64
	primed          bool
}

// NewSmoother cria um suavizador com constante de tempo timeMs.
func NewSmoother(fs, timeMs float64) Smoother {
	var s Smoother
	s.SetTime(fs, timeMs)
	return s
}

// SetTime ajusta a constante de tempo.
func (s *Smoother) SetTime(fs, timeMs float64) {
	if timeMs <= 0 {
		s.coef = 1
		return
	}
	s.coef = 1 - math.Exp(-1/(timeMs*0.001*fs))
}

// SetTarget define o novo alvo. Se ainda não houver valor, salta direto para ele.
func (s *Smoother) SetTarget(v float64) {
	s.target = v
	if !s.primed {
		s.y = v
		s.primed = true
	}
}

// Snap salta direto para o valor.
func (s *Smoother) Snap(v float64) { s.y, s.target, s.primed = v, v, true }

// Next avança uma amostra e devolve o valor suavizado.
func (s *Smoother) Next() float64 {
	if s.y != s.target {
		s.y += s.coef * (s.target - s.y)
		if math.Abs(s.target-s.y) < 1e-9 {
			s.y = s.target
		}
	}
	return s.y
}

// Value devolve o valor atual sem avançar.
func (s *Smoother) Value() float64 { return s.y }

// Settled indica se já chegou ao alvo.
func (s *Smoother) Settled() bool { return s.y == s.target }

// Unprime faz o próximo SetTarget saltar direto (usado em Reset).
func (s *Smoother) Unprime() { s.primed = false }
