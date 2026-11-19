package plugin

import (
	"math"
	"slices"
	"testing"

	"github.com/tatianno/ampsim/engine"
)

func TestPedals(t *testing.T) {
	ps := Pedals()
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.ID)
		for _, q := range p.Params {
			if engine.StereoOnly(q.ID) {
				t.Errorf("%s: parâmetro só de estéreo %q exposto", p.ID, q.ID)
			}
		}
	}
	if len(ps) != 17 || slices.Contains(ids, engine.PreID) || slices.Contains(ids, engine.CabID) {
		t.Errorf("pedais: %v", ids)
	}
	d, _ := FindPedal("drive")
	for _, q := range d.Params {
		if q.ID == "type" || q.ID == "nam_input" {
			t.Errorf("drive expõe %q", q.ID)
		}
	}
}

func TestLabels(t *testing.T) {
	label := func(pedal, param string) string {
		p, ok := FindPedal(pedal)
		if !ok {
			t.Fatalf("pedal %s", pedal)
		}
		for i, q := range p.Params {
			if q.ID == param {
				return p.Label(i)
			}
		}
		t.Fatalf("%s.%s", pedal, param)
		return ""
	}
	for _, c := range []struct{ pedal, param, want string }{
		{"eq", "f2", "Freq 2"},
		{"eq", "level", "Level"},
		{"synth", "s3", "Passo 3"},
		{"drive", "mode", "Modo (0 Boost, 1 Overdrive, 2 Distorção, 3 Fuzz, 4 Muff)"},
		{"rotary", "speed", "Velocidade (Rápido)"},
		{"phaser", "stages", "Estágios (4, 6, 8)"},
		{"chorus", "rate", "Rate"},
	} {
		if got := label(c.pedal, c.param); got != c.want {
			t.Errorf("%s.%s: %q, esperava %q", c.pedal, c.param, got, c.want)
		}
	}
}

// knobs devolve valores fora do padrão para os parâmetros do pedal (já na grade do passo).
func knobs(p Pedal) []float64 {
	m := engine.NewDefaultChain().Module(p.ID)
	vs := make([]float64, len(p.Params))
	for i, info := range p.Params {
		q := m.Param(info.ID)
		q.Set(info.Min + 0.7*(info.Max-info.Min))
		vs[i] = q.Get()
	}
	return vs
}

// Cada pedal soa igual ao módulo da app com os mesmos knobs.
func TestPedalIgualAoModulo(t *testing.T) {
	x := testSignal(fs)
	for _, p := range Pedals() {
		vs := knobs(p)
		in, err := NewPedal(fs, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		in.Activate(vs)
		got := run(in, x, 300, 1, Settings{Enabled: true, Params: vs})[0]
		lat := in.Latency()

		m := engine.NewDefaultChain().Module(p.ID)
		for i, info := range p.Params {
			m.Param(info.ID).Set(vs[i])
		}
		c := engine.NewChain(m)
		c.Prepare(fs, MaxBlock)
		ref := make([]float64, len(x)+lat)
		for i, v := range x {
			ref[i] = float64(v)
		}
		for off := 0; off < len(ref); off += 256 {
			c.ProcessMono(ref[off:min(off+256, len(ref))])
		}
		var maxd, diff float64
		for i := 0; i+lat < len(got); i++ {
			maxd = math.Max(maxd, math.Abs(float64(got[i+lat])-ref[i+lat]))
			diff = math.Max(diff, math.Abs(float64(got[i+lat])-float64(x[i])))
		}
		if maxd > 1e-6 {
			t.Errorf("%s: diferença máxima para o módulo %.2g (latência %d)", p.ID, maxd, lat)
		}
		if diff < 1e-3 {
			t.Errorf("%s: a saída é igual à entrada (o pedal não fez nada)", p.ID)
		}
	}
}

func TestPedalSemAlocar(t *testing.T) {
	p, _ := FindPedal("delay")
	in, err := NewPedal(fs, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	vs := knobs(p)
	x := testSignal(256)
	out := [][]float32{make([]float32, 256)}
	src := [][]float32{x}
	s := Settings{Enabled: true, Params: vs}
	in.Process(src, out, 256, s)
	k := 0
	if a := testing.AllocsPerRun(50, func() {
		vs[0] = 300 + float64(k%50) // mexendo no Time a cada bloco
		k++
		in.Process(src, out, 256, s)
	}); a != 0 {
		t.Errorf("Process aloca %v vezes por bloco", a)
	}
	if st, err := in.SaveState(); st != nil || err != nil {
		t.Errorf("pedal com estado: %s, %v", st, err)
	}
}
