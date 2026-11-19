package engine

import (
	"math"
	"slices"
	"testing"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
)

// seno preenche buf com um seno de 220 Hz a partir da amostra n0.
func seno(buf []float64, n0 int, amp float64) {
	for i := range buf {
		buf[i] = amp * math.Sin(float64(n0+i)*2*math.Pi*220/48000)
	}
}

// Trocar de tipo, de tamanho, de captura e de modo com o áudio rodando: sem estalo
// (crossfade) e com a saída idêntica qualquer que seja o tamanho de bloco.
func TestDriveNAMTrocasAoVivo(t *testing.T) {
	pedals := capturas.PedalNames()
	render := func(bloco int) []float64 {
		m := NewDrive()
		m.Prepare(48000, 1024)
		out := make([]float64, 48000)
		seno(out, 0, 0.2)
		for i := 0; i < len(out); i += bloco {
			switch i {
			case 4096:
				m.Param("type").Set(DriveNAM)
			case 8192:
				m.Param("mode").Set(dsp.DriveFuzz) // escondido no NAM: não muda nada
			case 12288:
				m.Param("nam_size").Set(NAMFull)
			case 16384:
				if err := m.LoadNAM(capturas.Prefix + pedals[0]); err != nil {
					t.Fatal(err)
				}
			case 24576:
				m.Param("level").Set(-6)
			case 32768:
				m.Param("type").Set(DriveModeled) // volta no Fuzz
			}
			m.Process(out[i:min(i+bloco, len(out))])
		}
		return out
	}
	ref := render(512)
	maxJump := 0.0
	for i := 1; i < len(ref); i++ {
		if math.IsNaN(ref[i]) || math.Abs(ref[i]) > 4 {
			t.Fatalf("valor inválido na amostra %d: %g", i, ref[i])
		}
		maxJump = math.Max(maxJump, math.Abs(ref[i]-ref[i-1]))
	}
	t.Logf("salto máx %.3f", maxJump)
	if maxJump > 0.6 {
		t.Errorf("salto de %.3f entre amostras (estalo?)", maxJump)
	}
	for _, b := range []int{64, 1024} {
		r := render(b)
		for i := range r {
			if r[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere (%g × %g)", b, i, r[i], ref[i])
			}
		}
	}
}

func TestDriveNAMSemAlocacao(t *testing.T) {
	c := NewDefaultChain()
	c.Prepare(48000, 256)
	c.SetEnabled("drive", true)
	m := c.Module("drive").(*Drive)
	buf := make([]float64, 256)
	k := 0
	if a := testing.AllocsPerRun(120, func() {
		k++
		m.Param("type").Set(float64(k / 20 % 2))
		m.Param("mode").Set(float64(k / 9 % 5))
		m.Param("nam_size").Set(float64(k / 7 % 2))
		m.Param("nam_input").Set(float64(k%5) - 2)
		seno(buf, k*256, 0.2)
		c.ProcessMono(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}

// No NAM a saída não é normalizada: o ganho é o que o pedal tinha na captura (o TS9 com o
// drive no zero e o Level em 5 dá ~+6 dB), e o Level atua depois do modelo.
func TestDriveNAMGanhoDaCaptura(t *testing.T) {
	ganho := func(level float64) float64 {
		m := NewDrive()
		m.Param("type").Set(DriveNAM)
		m.Param("level").Set(level)
		if err := m.LoadNAM(capturas.Prefix + "TS7 TS9 D0 T0 L5 (Clean Boost)"); err != nil {
			t.Fatal(err)
		}
		m.Prepare(48000, 256)
		buf := make([]float64, 256)
		var out []float64
		for b := 0; b < 80; b++ {
			seno(buf, b*256, 0.1)
			m.Process(buf)
			out = append(out, buf...)
		}
		return rmsDB(out[9600:]) - 20*math.Log10(0.1/math.Sqrt2)
	}
	g0, g6 := ganho(0), ganho(-6)
	t.Logf("ganho: %.1f dB (Level 0), %.1f dB (Level −6)", g0, g6)
	if g0 < 4 || g0 > 8 {
		t.Errorf("ganho do Clean Boost %.1f dB, esperava ~+6 dB", g0)
	}
	if math.Abs(g0-g6-6) > 0.2 {
		t.Errorf("Level −6 baixou %.1f dB", g0-g6)
	}
}

// O NAM fica alinhado com o modelado (mesma latência declarada): a resposta ao impulso
// começa no mesmo ponto. O pico vem depois no NAM (os filtros e o clipping do pedal espalham o
// impulso), por isso o teste olha o início.
func TestDriveNAMLatenciaAlinhada(t *testing.T) {
	for _, typ := range []int{DriveModeled, DriveNAM} {
		for _, name := range capturas.PedalNames() {
			m := NewDrive()
			m.Param("type").Set(float64(typ))
			if err := m.LoadNAM(capturas.Prefix + name); err != nil {
				t.Fatal(err)
			}
			m.Prepare(48000, 2048)
			x := make([]float64, 2048)
			m.Process(x) // repouso
			y0 := x[len(x)-1]
			x = make([]float64, 2048)
			x[100] = 0.3
			m.Process(x)
			first := -1
			for i, v := range x {
				if math.Abs(v-y0) > 3e-4 {
					first = i - 100
					break
				}
			}
			if first < dsp.DriveLatency-3 || first > dsp.DriveLatency+3 {
				t.Errorf("tipo %d, %s: resposta começa %d amostras depois, latência declarada %d",
					typ, name, first, dsp.DriveLatency)
			}
			if typ == DriveModeled {
				break
			}
		}
	}
}

// Os controles do NAM: Entrada e Level; Tipo e Tamanho (o Modo some).
func TestDriveNAMControles(t *testing.T) {
	m := NewDrive()
	if got := m.SelectorView(); !slices.Equal(got, []string{"type", "mode"}) {
		t.Errorf("modelado: chaves %v", got)
	}
	m.Param("type").Set(DriveNAM)
	var names []string
	for _, s := range m.ParamView() {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"Entrada", "Level"}) {
		t.Errorf("NAM: knobs %v", names)
	}
	if got := m.SelectorView(); !slices.Equal(got, []string{"type", "nam_size"}) {
		t.Errorf("NAM: chaves %v", got)
	}
	for _, id := range append(m.SelectorView(), "nam_input", "level", "mode") {
		if m.Param(id) == nil {
			t.Errorf("parâmetro %q não existe", id)
		}
	}
}

// O preset guarda a captura só no tipo NAM; carregar volta com ela.
func TestDriveNAMPreset(t *testing.T) {
	c := NewDefaultChain()
	drv := func(c *Chain) *Drive { return c.Module("drive").(*Drive) }
	for _, ms := range c.State().Modules {
		if ms.ID == "drive" && ms.Extra != nil {
			t.Errorf("modelado não deveria guardar captura: %v", ms.Extra)
		}
	}
	if drv(c).NAMRef() != DefaultDriveNAM {
		t.Errorf("padrão %q", drv(c).NAMRef())
	}
	ref := capturas.Prefix + "SD-1 Oliver SD-10 D10 T6 L10 (Cranked)"
	if err := drv(c).LoadNAM(ref); err != nil {
		t.Fatal(err)
	}
	drv(c).Param("type").Set(DriveNAM)
	d := NewDefaultChain()
	if err := d.ApplyState(c.State()); err != nil {
		t.Fatal(err)
	}
	if drv(d).NAMRef() != ref || drv(d).Type() != DriveNAM {
		t.Errorf("preset não guardou a captura: %q, tipo %d", drv(d).NAMRef(), drv(d).Type())
	}
	if err := drv(d).LoadNAM(capturas.Prefix + "não existe"); err == nil || drv(d).NAMRef() != ref {
		t.Error("erro ao carregar não pode trocar a captura atual")
	}
	// o amp e o drive têm modelos independentes
	if d.Pre().NAMRef() == ref {
		t.Error("a captura do drive foi parar no amp")
	}
}

// Um preset salvo antes do tipo NAM (sem drive.type) volta o drive ao modelado, mesmo vindo de
// um preset com o drive no NAM.
func TestDrivePresetAntigoVoltaAoModelado(t *testing.T) {
	c := NewDefaultChain()
	c.Drive().Param("type").Set(DriveNAM)
	st := NewDefaultChain().State()
	for i, ms := range st.Modules {
		if ms.ID == "drive" {
			delete(st.Modules[i].Params, "type")
			delete(st.Modules[i].Params, "nam_size")
		}
	}
	if err := c.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	if c.Drive().Type() != DriveModeled {
		t.Error("o drive ficou no NAM do preset anterior")
	}
}
