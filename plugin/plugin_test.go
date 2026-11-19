package plugin

import (
	"math"
	"testing"

	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
	"github.com/tatianno/ampsim/wav"
)

const fs = 48000

// sinal de teste: notas com ataque e decaimento (o gate abre e fecha), em float32.
func testSignal(n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		t := float64(i) / fs
		env := math.Exp(-3 * math.Mod(t, 0.5))
		x[i] = float32(0.3 * env * (math.Sin(2*math.Pi*110*t) + 0.5*math.Sin(2*math.Pi*331*t)))
	}
	return x
}

// run processa x inteiro em blocos de tamanho block e devolve os canais de saída.
func run(in *Instance, x []float32, block, outs int, s Settings) [][]float32 {
	out := make([][]float32, outs)
	for j := range out {
		out[j] = make([]float32, len(x))
	}
	dst := make([][]float32, outs)
	for off := 0; off < len(x); off += block {
		k := min(block, len(x)-off)
		for j := range dst {
			dst[j] = out[j][off : off+k]
		}
		in.Process([][]float32{x[off : off+k]}, dst, k, s)
	}
	return out
}

func factoryState(t *testing.T, name string) *engine.State {
	t.Helper()
	for _, p := range presets.Factory() {
		if p.Name == name {
			return &p.State
		}
	}
	t.Fatalf("preset de fábrica %q não existe", name)
	return nil
}

var on = Settings{Enabled: true}

func TestPresets(t *testing.T) {
	dir := t.TempDir()
	if _, err := presets.Save(dir, "Meu Som Ácido", engine.NewDefaultChain().State()); err != nil {
		t.Fatal(err)
	}
	ps, err := Presets(dir)
	if err != nil {
		t.Fatal(err)
	}
	nf := len(presets.Factory())
	if len(ps) != nf+1 {
		t.Fatalf("%d presets, esperava %d", len(ps), nf+1)
	}
	ids := map[string]bool{}
	for _, p := range ps {
		if ids[p.ID] {
			t.Errorf("ID repetido: %s", p.ID)
		}
		ids[p.ID] = true
	}
	if ps[0].ID != "fabrica-clean" {
		t.Errorf("ID do Clean: %s", ps[0].ID)
	}
	u := ps[nf]
	if u.Factory || u.Name != "Meu Som Ácido" || u.ID != "usuario-meu-som-%C3%A1cido" {
		t.Errorf("preset do usuário: %+v", u)
	}
	if st, err := u.State(); err != nil || len(st.Modules) == 0 {
		t.Errorf("estado do preset do usuário: %v", err)
	}
}

// A instância já sai com o preset desde a primeira amostra: igual ao render offline da app
// (com a latência descontada), em blocos de tamanho variável.
func TestIgualAoOffline(t *testing.T) {
	x := testSignal(fs * 2)
	if _, di, err := wav.ReadMonoFile("../testdata/guitarra_di.wav"); err == nil {
		x = make([]float32, min(len(di), fs*3))
		for i := range x {
			x[i] = float32(di[i])
		}
	}
	for _, name := range []string{"Clean", "Metal"} {
		st := factoryState(t, name)
		in := New(fs, false, st)
		got := run(in, x, 700, 1, on)[0] // 700 > MaxBlock: fatiado
		lat := in.Latency()

		ref := make([]float64, len(x))
		for i, v := range x {
			ref[i] = float64(v)
		}
		want, err := engine.RenderOffline(*st, ref, fs, 0, 512, nil)
		if err != nil {
			t.Fatal(err)
		}
		var maxd, peak float64
		for i := 0; i+lat < len(got); i++ {
			maxd = math.Max(maxd, math.Abs(float64(got[i+lat])-want[i]))
			peak = math.Max(peak, math.Abs(want[i]))
		}
		if peak < 1e-3 || maxd > 1e-5 {
			t.Errorf("%s: diferença máxima %.2g (pico %.2g, latência %d)", name, maxd, peak, lat)
		}
	}
}

// O estado guarda uma cópia do preset: restaurado depois que o preset do usuário mudou,
// o som continua o de quando foi salvo.
func TestEstadoGuardaCopiaDoPreset(t *testing.T) {
	dir := t.TempDir()
	c := engine.NewDefaultChain()
	c.Module(engine.PowerID).Param("volume").Set(-7)
	if _, err := presets.Save(dir, "Meu Som", c.State()); err != nil {
		t.Fatal(err)
	}
	ps, _ := Presets(dir)
	p := ps[len(ps)-1]
	st, err := p.State()
	if err != nil {
		t.Fatal(err)
	}
	a := New(fs, false, &st)
	data, err := a.SaveState()
	if err != nil {
		t.Fatal(err)
	}

	c.Module(engine.PowerID).Param("volume").Set(-20) // o usuário edita o preset na app
	if _, err := presets.Save(dir, "Meu Som", c.State()); err != nil {
		t.Fatal(err)
	}

	st2, _ := p.State() // o host recria a instância com o preset atual…
	b := New(fs, false, &st2)
	if v := b.chain.Module(engine.PowerID).Param("volume").Get(); v != -20 {
		t.Fatalf("instância nova: volume %v (esperava o preset editado, −20)", v)
	}
	if err := b.RestoreState(data); err != nil { // …e restaura o estado do projeto
		t.Fatal(err)
	}
	if v := b.chain.Module(engine.PowerID).Param("volume").Get(); v != -7 {
		t.Errorf("depois de restaurar, volume %v (esperava a cópia, −7)", v)
	}
	x := testSignal(fs / 2)
	ya, yb := run(a, x, 256, 1, on)[0], run(b, x, 256, 1, on)[0]
	for i := range ya {
		if ya[i] != yb[i] {
			t.Fatalf("amostra %d: restaurado %v, original %v", i, yb[i], ya[i])
		}
	}
}

// Sem preset (o arquivo sumiu) e com o plugin desligado sai o sinal seco, com o atraso da
// cadeia (a faixa não sai do lugar ao ligar e desligar).
func TestSinalSeco(t *testing.T) {
	x := testSignal(fs / 2)
	for _, tc := range []struct {
		st *engine.State
		s  Settings
	}{
		{nil, on},
		{factoryState(t, "Metal"), Settings{Enabled: false}},
	} {
		in := New(fs, false, tc.st)
		y := run(in, x, 300, 1, tc.s)[0]
		lat := in.Latency()
		for i := 0; i+lat < len(y); i++ {
			if y[i+lat] != x[i] {
				t.Fatalf("%+v: amostra %d = %v, esperava %v (latência %d)", tc.s, i, y[i+lat], x[i], lat)
			}
		}
	}
}

// +6 dB na saída dobra o nível do som processado.
func TestSaida(t *testing.T) {
	x := testSignal(fs)
	st := factoryState(t, "Clean")
	ya := run(New(fs, false, st), x, 512, 1, on)[0]
	yb := run(New(fs, false, st), x, 512, 1, Settings{OutputDB: 6, Enabled: true})[0]
	g := dbToLin(6)
	for i := range ya {
		if d := math.Abs(float64(yb[i]) - g*float64(ya[i])); d > 1e-6 {
			t.Fatalf("amostra %d: %v, esperava %v", i, yb[i], g*float64(ya[i]))
		}
	}
}

func TestProcessSemAlocar(t *testing.T) {
	in := New(fs, true, factoryState(t, "Clean"))
	x := testSignal(256)
	out := [][]float32{make([]float32, 256), make([]float32, 256)}
	src := [][]float32{x}
	in.Process(src, out, 256, on)
	if a := testing.AllocsPerRun(50, func() { in.Process(src, out, 256, on) }); a != 0 {
		t.Errorf("Process aloca %v vezes por bloco", a)
	}
}

// Em estéreo, o preset sai igual ao render estéreo da app (L e R).
func TestEstereoIgualAoOffline(t *testing.T) {
	x := testSignal(fs)
	st := factoryState(t, "Modulação")
	in := New(fs, true, st)
	got := run(in, x, 700, 2, on)
	lat := in.Latency()
	ref := make([]float64, len(x))
	for i, v := range x {
		ref[i] = float64(v)
	}
	want, err := engine.RenderOfflineChannels(*st, ref, fs, 0, 512, engine.Output{Stereo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lr float64
	for ch := range 2 {
		var maxd float64
		for i := 0; i+lat < len(x); i++ {
			maxd = math.Max(maxd, math.Abs(float64(got[ch][i+lat])-want[ch][i]))
			lr = math.Max(lr, math.Abs(want[0][i]-want[1][i]))
		}
		if maxd > 1e-5 {
			t.Errorf("canal %d: diferença máxima %.2g", ch, maxd)
		}
	}
	if lr < 1e-3 {
		t.Error("L e R iguais: o preset deveria abrir em estéreo")
	}
}

// NewLive: a troca de preset com o áudio rodando chega ao som, a latência não muda e a cópia
// salva acompanha o preset novo.
func TestTrocaAoVivo(t *testing.T) {
	in := NewLive(fs, factoryState(t, "Clean"))
	lat := in.Latency()
	x := testSignal(256)
	out := [][]float32{make([]float32, 256), make([]float32, 256)}
	in.Process([][]float32{x}, out, 256, on)
	done := make(chan struct{})
	go func() { // como o host: o preset novo é aplicado fora da thread de áudio
		in.Apply(factoryState(t, "Metal"))
		close(done)
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			in.Process([][]float32{x}, out, 256, on)
		}
	}
	for range 50 { // a transição termina
		in.Process([][]float32{x}, out, 256, on)
	}
	if in.Latency() != lat {
		t.Errorf("latência mudou de %d para %d", lat, in.Latency())
	}
	if s := in.Snapshot(); s == nil || s.Name != "Metal" {
		t.Fatalf("cópia salva: %v", s)
	}
	got := in.chain.State()
	if got.Modules[0].ID != "gate" || !in.chain.Enabled("drive") {
		t.Error("a cadeia não ficou com o preset Metal (drive ligado)")
	}
	in.Apply(nil) // sem preset: sinal seco
	if !in.empty.Load() {
		t.Error("Apply(nil) deveria deixar só o sinal seco")
	}
}
