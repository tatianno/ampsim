package engine

import (
	"math"
	"testing"
)

const spillFS = 44100.0

// ganhoFixo é um "power amp" de teste: só multiplica (para ver por onde a cauda passa).
type ganhoFixo struct {
	base
	g float64
}

func novoGanho(g float64) *ganhoFixo {
	return &ganhoFixo{base: base{id: PowerID, name: "Power", paramSet: newParamSet()}, g: g}
}
func (m *ganhoFixo) Prepare(float64, int) {}
func (m *ganhoFixo) Reset()               {}
func (m *ganhoFixo) Latency() int         { return 0 }
func (m *ganhoFixo) Process(buf []float64) {
	for i := range buf {
		buf[i] *= m.g
	}
}

// eco: delay só com o sinal molhado, uma repetição limpa.
func eco(ms float64, on bool) ModuleState {
	return ModuleState{ID: "delay", Enabled: on, Params: map[string]float64{
		"time": ms, "feedback": 0, "tone": 100, "mix": 100}}
}

func estado(ms ...ModuleState) State { return State{Version: StateVersion, Modules: ms} }

// roda: impulso no início, troca de preset (next) em trocaMs e devolve durMs de saída.
func roda(t *testing.T, c *Chain, next State, trocaMs, durMs float64) []float64 {
	t.Helper()
	out := make([]float64, int(durMs/1000*spillFS))
	out[0] = 1
	troca := int(trocaMs / 1000 * spillFS)
	for i := 0; i < len(out); i += 128 {
		if i <= troca && troca < i+128 {
			if err := c.ApplyState(next); err != nil {
				t.Fatal(err)
			}
		}
		c.ProcessMono(out[i:min(i+128, len(out))])
	}
	for i, v := range out {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("amostra %d inválida", i)
		}
	}
	return out
}

func pico(x []float64, deMs, ateMs float64) float64 {
	p := 0.0
	for _, v := range x[int(deMs/1000*spillFS):int(ateMs/1000*spillFS)] {
		p = math.Max(p, math.Abs(v))
	}
	return p
}

func cadeiaEco(t *testing.T, mods ...Module) *Chain {
	t.Helper()
	c := NewChain(mods...)
	if err := c.ApplyState(estado(eco(300, true))); err != nil {
		t.Fatal(err)
	}
	c.Prepare(spillFS, 128)
	return c
}

// O eco do preset anterior continua, no tempo antigo, e o delay novo não herda nada.
func TestSpilloverMantemEcoAntigo(t *testing.T) {
	c := cadeiaEco(t, NewDelay())
	out := roda(t, c, estado(eco(500, true)), 100, 700)
	if p := pico(out, 295, 305); p < 0.9 {
		t.Errorf("eco antigo em 300 ms: pico %.3f, esperava ~1", p)
	}
	if p := pico(out, 310, 700); p > 1e-6 {
		t.Errorf("sobrou eco fora do lugar (o delay novo herdou o buffer?): pico %g", p)
	}
}

// Desligado, a cauda some num fade curto.
func TestSpilloverDesligadoCorta(t *testing.T) {
	c := cadeiaEco(t, NewDelay())
	c.SetSpillover(false)
	out := roda(t, c, estado(eco(500, true)), 100, 700)
	if p := pico(out, 100+SpillCutMs+5, 700); p > 1e-9 {
		t.Errorf("com o spillover desligado a cauda devia sumir: pico %g", p)
	}
}

// O preset novo sem delay não corta a cauda do anterior.
func TestSpilloverPresetSemDelay(t *testing.T) {
	c := cadeiaEco(t, NewDelay())
	out := roda(t, c, estado(eco(300, false)), 100, 400)
	if p := pico(out, 295, 305); p < 0.9 {
		t.Errorf("eco antigo sumiu ao desligar o delay: pico %.3f", p)
	}
}

// A cauda do loop volta antes do power amp (passa por ele, como o som antigo).
func TestSpilloverPassaPeloAmp(t *testing.T) {
	c := cadeiaEco(t, NewDelay(), novoGanho(0.5))
	out := roda(t, c, estado(eco(500, true), ModuleState{ID: PowerID, Enabled: true}), 100, 400)
	if p := pico(out, 295, 305); math.Abs(p-0.5) > 0.02 {
		t.Errorf("eco antigo depois do power: pico %.3f, esperava 0,5", p)
	}
}

// Delay → reverb: o eco antigo ainda passa pelo reverb antigo.
func TestSpilloverDelayNoReverb(t *testing.T) {
	c := cadeiaEco(t, NewDelay(), NewReverb())
	c.ApplyState(estado(eco(300, true), ModuleState{ID: "reverb", Enabled: true,
		Params: map[string]float64{"size": 80, "damping": 20, "mix": 100}}))
	// preset novo: sem delay, reverb seco
	out := roda(t, c, estado(eco(300, false), ModuleState{ID: "reverb", Enabled: true,
		Params: map[string]float64{"mix": 0}}), 100, 800)
	if p := pico(out, 299, 301); p > 0.05 {
		t.Errorf("o eco antigo saiu seco (pico %.3f): devia passar pelo reverb antigo", p)
	}
	if rmsDB(out[int(0.33*spillFS):int(0.6*spillFS)]) < -60 {
		t.Error("sem a cauda do reverb sobre o eco antigo")
	}
}

// A cauda acaba sozinha e as instâncias voltam a ser reservas.
func TestSpilloverDevolveReservas(t *testing.T) {
	c := cadeiaEco(t, NewDelay(), NewReverb())
	rev := ModuleState{ID: "reverb", Enabled: true, Params: map[string]float64{"size": 90}}
	c.ApplyState(estado(eco(300, true), rev))
	roda(t, c, estado(eco(300, true), rev), 100, 200)
	if c.Tails() == 0 {
		t.Fatal("esperava uma cauda soando logo depois da troca")
	}
	buf := make([]float64, 128)
	for range int(3*spillFS) / 128 { // 3 s de silêncio
		c.ProcessMono(buf)
	}
	if c.Tails() != 0 {
		t.Errorf("a cauda não terminou: %d grupos", c.Tails())
	}
	for _, s := range c.all {
		if s.sp != nil && len(s.free) != SpillSpares {
			t.Errorf("%s: %d reservas, esperava %d", s.m.ID(), len(s.free), SpillSpares)
		}
	}
}

// Trocas seguidas (rolando a lista de presets) não alocam nem perdem instâncias.
func TestSpilloverTrocasSeguidas(t *testing.T) {
	c := cadeiaEco(t, NewDelay(), NewReverb(), novoGanho(1))
	c.SetEnabled("reverb", true)
	buf := make([]float64, 128)
	k := 0
	if a := testing.AllocsPerRun(200, func() {
		k++
		for i := range buf {
			buf[i] = math.Sin(float64(k*128+i) * 0.05)
		}
		if k%3 == 0 {
			c.beginTransition()
		}
		c.ProcessMono(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
	for _, s := range c.all {
		if s.sp == nil {
			continue
		}
		n := len(s.free)
		for _, g := range c.groups {
			for _, p := range g.parts {
				if g.on && p.owner == s {
					n++
				}
			}
		}
		if n != SpillSpares {
			t.Errorf("%s: %d instâncias fora de uso, esperava %d", s.m.ID(), n, SpillSpares)
		}
	}
	if c.Tails() > maxTailGroups {
		t.Errorf("%d grupos de cauda", c.Tails())
	}
}
