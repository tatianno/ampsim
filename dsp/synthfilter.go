package dsp

import "math"

// Modos de modulação do filtro de sintetizador.
const (
	SynthEnvelope = iota // o corte abre com a palhetada
	SynthLFO             // o corte varre sozinho
	SynthSequence        // o corte segue uma sequência de passos, no andamento
)

// Tipos de filtro.
const (
	SynthLP = iota // passa-baixa de 4 polos (24 dB/oitava)
	SynthBP        // passa-banda
	SynthHP        // passa-alta
)

// SynthSteps é o número de passos da sequência.
const SynthSteps = 8

// fastTanh é uma aproximação racional de tanh (erro < 2% e saturação suave), barata o
// bastante para os 5 tanh por amostra da taxa alta do ladder.
func fastTanh(x float64) float64 {
	if x > 3 {
		return 1
	}
	if x < -3 {
		return -1
	}
	x2 := x * x
	return x * (27 + x2) / (27 + 9*x2)
}

// SynthFilter é o filtro de sintetizador: um ladder de 4 polos no estilo Moog (modelo não
// linear de Huovilainen: um tanh em cada estágio e na realimentação), a 4x, com o corte
// modulado por envelope, LFO ou sequência de passos, e por FM na velocidade do áudio (o
// próprio sinal, normalizado pelo envelope, desloca o corte).
type SynthFilter struct {
	fs  float64
	os  *Oversampler4x
	up  [OversampleFactor]float64
	y   [4]float64 // estágios do ladder
	env float64

	lfo         LFO
	seqPhase    float64 // posição na sequência, em passos
	seqVal      float64 // valor da sequência suavizado (glide)
	steps       [SynthSteps]float64
	step        int
	attK, relK  float64
	glideK      float64
	fc          float64 // último corte (Hz) para a GUI e os testes
	mode, ftype int
	dry         *DelayLine // o seco, alinhado com a latência do oversampling (para o mix)

	cutoff, res, depth, sens, rate, fm, drive, mix, level Smoother
}

// NewSynthFilter cria o filtro (aloca).
func NewSynthFilter(fs float64) *SynthFilter {
	s := &SynthFilter{fs: fs, os: NewOversampler4x(), dry: NewDelayLine(osLatency + 1),
		attK: 1 - math.Exp(-1/(0.003*fs)), relK: 1 - math.Exp(-1/(0.15*fs)), glideK: 1}
	for _, sm := range []*Smoother{&s.cutoff, &s.res, &s.depth, &s.sens, &s.rate, &s.fm, &s.drive, &s.mix, &s.level} {
		sm.SetTime(fs, 20)
	}
	return s
}

// SynthLatency é a latência do filtro (oversampling).
const SynthLatency = osLatency

// Set ajusta o modo, o tipo, o corte (Hz), a ressonância (0..1), a profundidade da
// modulação (oitavas), a sensibilidade do envelope (0..1), o rate (Hz: ciclos do LFO ou
// passos por segundo da sequência), a FM (0..1), o drive de entrada (dB), o mix (0..1) e o
// nível de saída (dB).
func (s *SynthFilter) Set(mode, ftype int, cutoff, res, depthOct, sens, rate, fm, driveDB, mix, levelDB float64) {
	s.mode, s.ftype = mode, ftype
	s.cutoff.SetTarget(cutoff)
	s.res.SetTarget(res)
	s.depth.SetTarget(depthOct)
	s.sens.SetTarget(sens)
	s.rate.SetTarget(rate)
	s.fm.SetTarget(fm)
	s.drive.SetTarget(math.Pow(10, driveDB/20))
	s.mix.SetTarget(mix)
	s.level.SetTarget(math.Pow(10, levelDB/20))
}

// SetSteps ajusta os valores dos passos (0..1) e o glide entre eles (ms).
func (s *SynthFilter) SetSteps(steps [SynthSteps]float64, glideMs float64) {
	s.steps = steps
	if glideMs <= 0 {
		s.glideK = 1
	} else {
		s.glideK = 1 - math.Exp(-1/(glideMs/1000*s.fs))
	}
}

// Step devolve o passo atual da sequência (para acender na GUI).
func (s *SynthFilter) Step() int { return s.step }

// CutoffHz devolve o corte atual.
func (s *SynthFilter) CutoffHz() float64 { return s.fc }

// Reset zera o estado.
func (s *SynthFilter) Reset() {
	s.os.Reset()
	s.dry.Reset()
	s.y = [4]float64{}
	s.env, s.seqPhase, s.seqVal, s.step = 0, 0, s.steps[0], 0
	s.lfo.Reset()
}

// ProcessBlock processa in-place.
func (s *SynthFilter) ProcessBlock(buf []float64) {
	fsOS := s.fs * OversampleFactor
	for i, x := range buf {
		a := math.Abs(x)
		if a > s.env {
			s.env += s.attK * (a - s.env)
		} else {
			s.env += s.relK * (a - s.env)
		}
		rate := s.rate.Next()
		var mod float64
		switch s.mode {
		case SynthLFO:
			mod = 0.5 * (1 + s.lfo.Next(rate, s.fs))
		case SynthSequence:
			s.seqPhase += rate / s.fs
			if s.seqPhase >= SynthSteps {
				s.seqPhase -= SynthSteps
			}
			s.step = int(s.seqPhase)
			s.seqVal += s.glideK * (s.steps[s.step] - s.seqVal)
			mod = s.seqVal
		default:
			mod = math.Min(1, s.env*s.sens.Next()*12)
		}
		fc := s.cutoff.Next() * math.Exp2(s.depth.Next()*mod)
		s.fc = fc
		r := 1.05 * s.res.Next() // acima de ~1 o filtro oscila sozinho
		fm := 2 * s.fm.Next() / (s.env + 0.01)
		drive := s.drive.Next()

		s.os.Up(x*drive, &s.up)
		for k, u := range s.up {
			f := fc
			if fm != 0 {
				f *= math.Exp2(fm * u / drive) // FM pelo próprio sinal: até ±2 oitavas
			}
			f = math.Min(f, 0.2*fsOS)
			g := 1 - math.Exp(-2*math.Pi*f/fsOS)
			in := fastTanh(u - 4*r*(s.y[3]-0.5*u)) // realimentação com compensação de graves
			t0 := fastTanh(s.y[0])
			s.y[0] += g * (in - t0)
			t1 := fastTanh(s.y[1])
			s.y[1] += g * (t0 - t1)
			t2 := fastTanh(s.y[2])
			s.y[2] += g * (t1 - t2)
			s.y[3] += g * (t2 - fastTanh(s.y[3]))
			switch s.ftype {
			case SynthBP:
				s.up[k] = 4 * (s.y[1] - 2*s.y[2] + s.y[3])
			case SynthHP:
				s.up[k] = in - 4*s.y[0] + 6*s.y[1] - 4*s.y[2] + s.y[3]
			default:
				s.up[k] = s.y[3]
			}
		}
		// a realimentação já compensa os graves (−0,5·u); √drive desconta parte do ganho do drive
		wet := s.os.Down(&s.up) / math.Sqrt(drive)
		d := s.dry.ReadInt(osLatency)
		s.dry.Write(x)
		m := s.mix.Next()
		buf[i] = ((1-m)*d + m*wet) * s.level.Next()
	}
}
