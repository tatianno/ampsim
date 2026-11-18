package engine

import (
	"math"
	"testing"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
)

// lerDI48 lê a DI de teste já reamostrada para 48 kHz (a taxa dos modelos NAM).
func lerDI48(t *testing.T) []float64 {
	t.Helper()
	fs, di := lerDI(t)
	return dsp.ResampleRate(di, int(fs), 48000)
}

func renderCadeia(c *Chain, x []float64, bloco int) []float64 {
	out := append([]float64(nil), x...)
	for i := 0; i < len(out); i += bloco {
		c.ProcessMono(out[i:min(i+bloco, len(out))])
	}
	return out
}

// No modo NAM, a normalização pelo loudness deixa as capturas perto do volume do amp
// simulado (mesmo knob de volume), para trocar de modo sem salto grande.
func TestNAMVolumeParecidoComSimulado(t *testing.T) {
	di := lerDI48(t)
	nova := func() *Chain {
		c := NewDefaultChain()
		if err := c.Cab().LoadIRFile(irPath); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := nova()
	c.Prepare(48000, 256)
	sim := rmsDB(renderCadeia(c, di, 256)[4800:])
	t.Logf("simulado (padrão): %.1f dB", sim)
	for _, name := range capturas.Names() {
		c := nova()
		c.Pre().Param("amp").Set(AmpNAM)
		if err := c.Pre().LoadNAM(capturas.Prefix + name); err != nil {
			t.Fatal(err)
		}
		c.Prepare(48000, 256)
		r := rmsDB(renderCadeia(c, di, 256)[4800:])
		t.Logf("%-32s %.1f dB (%+.1f)", name, r, r-sim)
		if math.Abs(r-sim) > 6 {
			t.Errorf("%s: %.1f dB longe do simulado", name, r-sim)
		}
	}
}

// Trocar de modo, de tamanho e de modelo com o áudio rodando: sem alocar, sem estalo
// (crossfade) e com a saída idêntica qualquer que seja o tamanho de bloco.
func TestNAMTrocasAoVivo(t *testing.T) {
	di := lerDI48(t)[:48000]
	names := capturas.Names()
	render := func(bloco int) []float64 {
		c := NewChain(NewPre())
		c.Prepare(48000, 1024)
		p := c.Pre()
		out := append([]float64(nil), di...)
		for i := 0; i < len(out); i += bloco {
			switch i {
			case 8192:
				p.Param("amp").Set(AmpNAM)
			case 16384:
				p.Param("nam_size").Set(NAMFull)
			case 24576:
				if err := p.LoadNAM(capturas.Prefix + names[0]); err != nil {
					t.Fatal(err)
				}
			case 32768:
				p.Param("amp").Set(AmpSimulated)
			}
			c.ProcessMono(out[i:min(i+bloco, len(out))])
		}
		return out
	}
	ref := render(512)
	maxJump, at := 0.0, 0
	for i := 1; i < len(ref); i++ {
		if math.IsNaN(ref[i]) || math.Abs(ref[i]) > 4 {
			t.Fatalf("valor inválido na amostra %d: %g", i, ref[i])
		}
		if d := math.Abs(ref[i] - ref[i-1]); d > maxJump {
			maxJump, at = d, i
		}
	}
	// Sem o crossfade, a troca seca dá saltos bem maiores; o limite fica acima do maior
	// salto natural destes sons sozinhos (~0,74 no modelo limpo, transientes da DI).
	t.Logf("salto máx %.2f na amostra %d", maxJump, at)
	if maxJump > 1 {
		t.Errorf("salto de %.2f entre amostras (estalo?)", maxJump)
	}
	for _, b := range []int{64, 1024} {
		r := render(b)
		for i := range r {
			if r[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere (%g × %g)", b, i, r[i], ref[i])
			}
		}
	}
	// o modelo final é o carregado ao vivo
	c := NewChain(NewPre())
	c.Prepare(48000, 256)
	p := c.Pre()
	p.Param("amp").Set(AmpNAM)
	c.ProcessMono(make([]float64, 256))
	if info := p.NAMInfo(); info == nil || info.Ref != DefaultNAM || info.Size != "lite" || info.Channels != 3 {
		t.Errorf("info padrão: %+v", info)
	}
}

func TestNAMSemAlocacao(t *testing.T) {
	c := NewDefaultChain()
	c.Prepare(48000, 256)
	p := c.Pre()
	if err := p.LoadNAM(capturas.Prefix + capturas.Names()[1]); err != nil {
		t.Fatal(err)
	}
	buf := make([]float64, 256)
	k := 0
	if a := testing.AllocsPerRun(80, func() {
		k++
		p.Param("amp").Set(float64(k / 20 % 2))
		p.Param("nam_size").Set(float64(k / 7 % 2))
		p.Param("nam_input").Set(float64(k%5) - 2)
		c.ProcessMono(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}

func TestNAMPresetGuardaModelo(t *testing.T) {
	c := NewDefaultChain()
	ref := capturas.Prefix + capturas.Names()[2]
	if err := c.Pre().LoadNAM(ref); err != nil {
		t.Fatal(err)
	}
	c.Pre().Param("amp").Set(AmpNAM)
	st := c.State()
	d := NewDefaultChain()
	if err := d.ApplyState(st); err != nil {
		t.Fatal(err)
	}
	if d.Pre().NAMRef() != ref || d.Pre().Mode() != AmpNAM {
		t.Errorf("preset não guardou o modelo: %q, modo %d", d.Pre().NAMRef(), d.Pre().Mode())
	}
	if err := d.Pre().LoadNAM("/não/existe.nam"); err == nil {
		t.Error("esperava erro com arquivo inexistente")
	}
	if err := d.Pre().LoadNAM(capturas.Prefix + "não existe"); err == nil {
		t.Error("esperava erro com captura inexistente")
	}
	if d.Pre().NAMRef() != ref {
		t.Error("erro ao carregar não pode trocar o modelo atual")
	}
}

// No modo NAM o som é alinhado com o do simulado (mesma latência declarada da cadeia).
func TestNAMLatenciaAlinhada(t *testing.T) {
	fs := 48000.0
	impulso := func(mode int) []float64 {
		c := NewChain(NewPre())
		c.Pre().Param("amp").Set(float64(mode))
		c.Prepare(fs, 256)
		x := make([]float64, 2048)
		c.ProcessMono(x[:1024]) // estado de repouso
		x = make([]float64, 2048)
		x[100] = 0.5
		c.ProcessMono(x)
		return x
	}
	for _, mode := range []int{AmpSimulated, AmpNAM} {
		y := impulso(mode)
		first := -1
		for i, v := range y {
			if math.Abs(v-y[0]) > 1e-4 {
				first = i
				break
			}
		}
		// o pico do sinal sai em 100 + latência; a resposta começa um pouco antes (filtro de fase linear)
		if first < 100 || first > 100+dsp.PreampLatency+2 {
			t.Errorf("modo %d: resposta começa na amostra %d", mode, first)
		}
		t.Logf("modo %d: resposta começa na amostra %d (latência declarada %d)", mode, first, dsp.PreampLatency)
	}
}
