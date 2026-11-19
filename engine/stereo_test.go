package engine

import (
	"math"
	"testing"
)

// processaEstereo roda x pela cadeia em blocos e devolve L e R.
func processaEstereo(c *Chain, x []float64, bloco int) (l, r []float64) {
	l = append([]float64(nil), x...)
	r = make([]float64, len(x))
	for i := 0; i < len(l); i += bloco {
		j := min(i+bloco, len(l))
		c.ProcessStereo(l[i:j], r[i:j])
	}
	return l, r
}

func maxDiff(a, b []float64) float64 {
	d := 0.0
	for i := range a {
		d = math.Max(d, math.Abs(a[i]-b[i]))
	}
	return d
}

// cadeiaEstereo é a cadeia completa já preparada em estéreo (sem a rampa da troca de modo).
func cadeiaEstereo(t *testing.T, fs float64, maxBlock int) *Chain {
	c := cadeiaCompleta(t, fs, maxBlock)
	c.SetStereo(true)
	c.Prepare(fs, maxBlock)
	return c
}

// todas as larguras em 0
func semLargura(c *Chain) {
	for _, m := range c.AllModules() {
		if p := m.Param(WidthParam); p != nil {
			p.Set(0)
		}
	}
}

// Com o estéreo desligado, ProcessStereo é o mono de antes nos dois canais.
func TestEstereoDesligadoIgualMono(t *testing.T) {
	fs, di := lerDI(t)
	x := di[:30000]
	a := cadeiaCompleta(t, fs, 256)
	mono := append([]float64(nil), x...)
	a.ProcessMono(mono)
	b := cadeiaCompleta(t, fs, 256)
	l, r := processaEstereo(b, x, 256)
	if d := maxDiff(l, mono); d != 0 {
		t.Errorf("L difere do mono: %g", d)
	}
	if d := maxDiff(r, l); d != 0 {
		t.Errorf("R difere do L: %g", d)
	}
}

// Estéreo com todas as larguras em 0: L = R = mono (a cadeia toda: gêmeas, gabinete, delay,
// reverb e os módulos só mono no loop).
func TestEstereoLarguraZeroIgualMono(t *testing.T) {
	fs, di := lerDI(t)
	x := di[:30000]
	a := cadeiaCompleta(t, fs, 256)
	semLargura(a)
	mono := append([]float64(nil), x...)
	a.ProcessMono(mono)
	b := cadeiaEstereo(t, fs, 256)
	semLargura(b)
	l, r := processaEstereo(b, x, 256)
	if d := maxDiff(l, r); d > 1e-12 {
		t.Errorf("com largura 0, L e R deveriam ser iguais: %g", d)
	}
	if d := maxDiff(l, mono); d > 1e-9 {
		t.Errorf("com largura 0, o estéreo deveria soar como o mono: %g", d)
	}
}

// Cada modulação e ambiência abre o estéreo com a Largura.
func TestEstereoAbrePorPedal(t *testing.T) {
	fs, di := lerDI(t)
	x := di[:40000]
	for _, id := range []string{"chorus", "flanger", "phaser", "vibe", "tremolo", "rotary", "delay", "reverb"} {
		c := NewDefaultChain()
		if err := c.SetOrder([]string{PreID, id, PowerID, CabID}); err != nil {
			t.Fatal(err)
		}
		c.SetEnabled(id, true)
		c.Module(id).Param(WidthParam).Set(100)
		c.SetStereo(true)
		c.Prepare(fs, 256)
		l, r := processaEstereo(c, x, 256)
		dif := make([]float64, len(l))
		for i := range l {
			dif[i] = l[i] - r[i]
		}
		if rel := rmsDB(dif) - rmsDB(l); rel < -30 {
			t.Errorf("%s: L e R quase iguais com largura 100%% (diferença %.1f dB)", id, rel)
		}
	}
}

// Delay ping-pong: o 1º eco só no L, o 2º só no R.
func TestDelayPingPong(t *testing.T) {
	c := NewChain(NewDelay())
	m := c.Module("delay")
	for id, v := range map[string]float64{"time": 300, "feedback": 50, "tone": 100, "mix": 100,
		WidthParam: 100, StereoModeParam: DelayPingPong} {
		m.Param(id).Set(v)
	}
	c.SetStereo(true)
	c.Prepare(spillFS, 128)
	x := make([]float64, int(0.7*spillFS))
	x[0] = 1
	l, r := processaEstereo(c, x, 128)
	if pl, pr := pico(l, 295, 305), pico(r, 295, 305); pl < 0.9 || pr > 1e-6 {
		t.Errorf("1º eco: L %.3f, R %.3f (esperava só no L)", pl, pr)
	}
	if pl, pr := pico(l, 595, 605), pico(r, 595, 605); math.Abs(pr-0.5) > 0.02 || pl > 1e-6 {
		t.Errorf("2º eco: L %.3f, R %.3f (esperava 0,5 só no R)", pl, pr)
	}
}

// Delay duplo: o R repete DelayWideMs depois do L, com a largura toda.
func TestDelayDuplo(t *testing.T) {
	c := NewChain(NewDelay())
	m := c.Module("delay")
	for id, v := range map[string]float64{"time": 300, "feedback": 0, "tone": 100, "mix": 100, WidthParam: 100} {
		m.Param(id).Set(v)
	}
	c.SetStereo(true)
	c.Prepare(spillFS, 128)
	x := make([]float64, int(0.5*spillFS))
	x[0] = 1
	l, r := processaEstereo(c, x, 128)
	if p := pico(l, 299, 301); p < 0.9 {
		t.Errorf("eco do L em 300 ms: %.3f", p)
	}
	if p := pico(r, 299, 301); p > 1e-6 {
		t.Errorf("o R não deveria repetir em 300 ms: %.3f", p)
	}
	soma := 0.0 // o atraso extra do R cai entre duas amostras: o eco se divide entre elas
	for _, v := range r[int(324*spillFS)/1000 : int(326*spillFS)/1000] {
		soma += v
	}
	if math.Abs(soma-1) > 0.01 {
		t.Errorf("eco do R em 325 ms: soma %.3f, esperava 1", soma)
	}
}

// O resultado não depende do tamanho de bloco do host, também em estéreo.
func TestEstereoInvarianciaDeBloco(t *testing.T) {
	fs, di := lerDI(t)
	x := di[:30000]
	a := cadeiaEstereo(t, fs, 1024)
	b := cadeiaEstereo(t, fs, 1024)
	la, ra := processaEstereo(a, x, 64)
	lb, rb := processaEstereo(b, x, 1024)
	if d := math.Max(maxDiff(la, lb), maxDiff(ra, rb)); d > 1e-6 {
		t.Errorf("blocos de 64 e 1024 diferem: %g", d)
	}
}

// Ligar e desligar o estéreo com o som rolando: rampa até o silêncio, sem NaN e sem alocar.
func TestEstereoTrocaAoVivo(t *testing.T) {
	fs, di := lerDI(t)
	c := cadeiaCompleta(t, fs, 256)
	l, r := make([]float64, 256), make([]float64, 256)
	k, liga := 0, false
	zeros := 0
	if a := testing.AllocsPerRun(120, func() {
		k++
		if k%20 == 0 {
			liga = !liga
			c.SetStereo(liga)
		}
		if k%7 == 0 {
			c.beginTransition() // spillover junto
		}
		copy(l, di[(k*256)%20000:])
		c.ProcessStereo(l, r)
		for i := range l {
			if math.IsNaN(l[i]) || math.IsNaN(r[i]) {
				t.Fatalf("NaN no bloco %d", k)
			}
			if l[i] == 0 && r[i] == 0 {
				zeros++
			}
		}
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
	if zeros == 0 {
		t.Error("esperava a rampa até o silêncio ao trocar mono/estéreo")
	}
}

// Spillover em estéreo: o eco ping-pong antigo continua nos dois canais.
func TestSpilloverEstereo(t *testing.T) {
	c := NewChain(NewDelay())
	pp := func(on bool) State {
		ms := eco(300, on)
		ms.Params["feedback"] = 50
		ms.Params[WidthParam] = 100
		ms.Params[StereoModeParam] = DelayPingPong
		return estado(ms)
	}
	c.ApplyState(pp(true))
	c.SetStereo(true)
	c.Prepare(spillFS, 128)
	x := make([]float64, int(0.7*spillFS))
	x[0] = 1
	troca := int(0.1 * spillFS)
	l, r := append([]float64(nil), x...), make([]float64, len(x))
	for i := 0; i < len(l); i += 128 {
		if i <= troca && troca < i+128 {
			c.ApplyState(pp(false)) // preset sem delay
		}
		c.ProcessStereo(l[i:min(i+128, len(l))], r[i:min(i+128, len(l))])
	}
	if p := pico(l, 295, 305); p < 0.9 {
		t.Errorf("eco antigo no L: %.3f", p)
	}
	if p := pico(r, 595, 605); math.Abs(p-0.5) > 0.02 {
		t.Errorf("eco antigo no R: %.3f", p)
	}
}

// A interface multicanal entrega L e R nas duas primeiras saídas.
func TestProcessMulticanalEstereo(t *testing.T) {
	c := NewChain(NewDelay())
	m := c.Module("delay")
	for id, v := range map[string]float64{"time": 20, "feedback": 50, "tone": 100, "mix": 100,
		WidthParam: 100, StereoModeParam: DelayPingPong} {
		m.Param(id).Set(v)
	}
	c.SetStereo(true)
	c.Prepare(spillFS, 256)
	in := [][]float32{make([]float32, 2048)}
	in[0][0] = 1
	out := [][]float32{make([]float32, 2048), make([]float32, 2048)}
	c.Process(in, out, 2048)
	d := int(0.02 * spillFS)
	if out[0][d] < 0.9 || math.Abs(float64(out[1][d])) > 1e-6 {
		t.Errorf("1º eco: L %.3f R %.3f", out[0][d], out[1][d])
	}
	if math.Abs(float64(out[1][2*d])-0.5) > 0.02 || math.Abs(float64(out[0][2*d])) > 1e-6 {
		t.Errorf("2º eco: L %.3f R %.3f (esperava 0,5 só no R)", out[0][2*d], out[1][2*d])
	}
}
