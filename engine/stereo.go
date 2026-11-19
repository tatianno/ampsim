package engine

import "math"

// Estéreo: a entrada do pré-amp é sempre mono, como num amp de verdade; com o estéreo ligado
// (SetStereo), a cadeia passa a ter dois canais logo depois do pré-amp. Daí em diante cada
// módulo processa L e R de um destes jeitos:
//
//   - StereoProcessor: o próprio módulo trata os dois canais (delay, reverb, gabinete);
//   - Twinner: uma segunda instância, com os mesmos parâmetros, processa o R (power amp, EQ,
//     vibrato...). Com TwinSyncer, o módulo acerta a gêmea antes de cada bloco: nas
//     modulações, o LFO do R fica defasado do L conforme a Largura;
//   - os demais (drive, wah, octave, synth) somam os canais e processam em mono.
//
// Com o estéreo desligado tudo roda em mono, exatamente como antes, e o R é cópia do L.
// Ligar ou desligar faz a rampa curta da troca de ordem e começa os módulos limpos.

// StereoProcessor é implementado pelos módulos que processam os dois canais juntos.
type StereoProcessor interface {
	ProcessStereo(l, r []float64)
}

// Twinner é implementado pelos módulos que, no trecho estéreo, usam uma segunda instância no
// canal R. A gêmea compartilha os parâmetros (os mesmos *Param) e é preparada pela cadeia.
type Twinner interface {
	Twin() Module
}

// TwinSyncer acerta a gêmea antes de cada bloco de n amostras (thread de áudio; não aloca).
type TwinSyncer interface {
	SyncTwin(twin Module, n int)
}

// Parâmetros que só valem em estéreo (a GUI os esconde com o estéreo desligado).
const (
	WidthParam      = "width"  // Largura, nas modulações e ambiências
	StereoModeParam = "stmode" // modo estéreo do delay (Duplo ou Ping-pong)
)

// StereoOnly informa se o parâmetro só faz efeito com o estéreo ligado.
func StereoOnly(id string) bool { return id == WidthParam || id == StereoModeParam }

// SetStereo liga ou desliga a saída estéreo. Seguro com o áudio rodando (rampa curta).
func (c *Chain) SetStereo(on bool) {
	c.stereoWant.Store(on)
	if !c.prepared {
		c.stereo = on
	}
}

// Stereo informa se a saída estéreo está ligada.
func (c *Chain) Stereo() bool { return c.stereoWant.Load() }

// widthInfo é o parâmetro de Largura (0 = o R igual ao L).
func widthInfo(def float64) ParamInfo {
	return ParamInfo{ID: WidthParam, Name: "Largura", Unit: "%", Min: 0, Max: 100, Default: def}
}

// StereoSweepMs é quanto a defasagem do R leva para ir de 0 a 180° ao girar a Largura.
const StereoSweepMs = 100.0

// phaseOffset é a defasagem (em ciclos) entre o LFO do L e o do R, suavizada por bloco: girar
// a Largura não faz o R pular de fase.
type phaseOffset struct {
	v      float64
	primed bool
}

// step anda até target (no máximo meio ciclo a cada StereoSweepMs) e devolve o valor.
func (p *phaseOffset) step(target float64, n int, fs float64) float64 {
	if !p.primed {
		p.v, p.primed = target, true
		return p.v
	}
	d := 0.5 * float64(n) / (StereoSweepMs / 1000 * fs)
	p.v += math.Max(-d, math.Min(d, target-p.v))
	return p.v
}

func (p *phaseOffset) reset() { p.primed = false }
