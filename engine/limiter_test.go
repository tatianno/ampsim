package engine

import (
	"math"
	"testing"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
)

// O caso que motivou o limitador: a captura Clean V10 no volume padrão passa de 0 dBFS com a
// DI de teste. Com o limitador, a saída fica no teto; sem ele, é a mesma de sempre; e ele só
// mexe perto dos picos (o resto do sinal sai igual).
func TestLimitadorNaSaida(t *testing.T) {
	di := lerDI48(t)
	st := func() State {
		c := NewDefaultChain()
		c.Pre().Param("amp").Set(AmpNAM)
		if err := c.Pre().LoadNAM(capturas.Prefix + "Clean V10 B5 M5 T5 P5"); err != nil {
			t.Fatal(err)
		}
		return c.State()
	}()
	ceil := math.Pow(10, dsp.LimiterCeilingDB/20)
	for _, stereo := range []bool{false, true} {
		free, err := RenderOfflineChannels(st, di, 48000, 0, 256, Output{Stereo: stereo}, nil)
		if err != nil {
			t.Fatal(err)
		}
		lim, err := RenderOfflineChannels(st, di, 48000, 0, 256, Output{Stereo: stereo, Limiter: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		// tempo com redução acima de 0,1 dB e de 1 dB (o ganho é o mesmo nos dois canais)
		pk, over01, over1, n := 0.0, 0, 0, 0
		for ch := range free {
			for i, v := range free[ch] {
				pk = math.Max(pk, math.Abs(v))
				if math.Abs(lim[ch][i]) > ceil+1e-12 {
					t.Fatalf("estéreo=%v canal %d amostra %d: %.4f passou do teto", stereo, ch, i, lim[ch][i])
				}
				if ch > 0 || math.Abs(v) < 1e-3 {
					continue
				}
				n++
				switch g := -20 * math.Log10(lim[ch][i]/v); {
				case g > 1:
					over1++
					fallthrough
				case g > 0.1:
					over01++
				}
			}
		}
		f01, f1 := 100*float64(over01)/float64(n), 100*float64(over1)/float64(n)
		t.Logf("estéreo=%v: pico sem limitador %+.1f dBFS; redução > 0,1 dB em %.1f%% do tempo, > 1 dB em %.1f%%",
			stereo, 20*math.Log10(pk), f01, f1)
		if pk <= 1 {
			t.Skipf("a DI não passa mais de 0 dBFS nesse som (%.1f dBFS)", 20*math.Log10(pk))
		}
		// esse som passa 1,8 dB do teto em muitas palhetadas: o limitador segura, mas não é para
		// ficar atuando o tempo todo
		if f01 > 15 || f1 > 6 {
			t.Errorf("estéreo=%v: o limitador atua demais (%.1f%% / %.1f%%)", stereo, f01, f1)
		}
	}
}

func TestLimitadorSemAlocacao(t *testing.T) {
	c := NewDefaultChain()
	c.SetLimiter(true)
	c.Prepare(48000, 256)
	l, r := make([]float64, 256), make([]float64, 256)
	k := 0
	if a := testing.AllocsPerRun(50, func() {
		for i := range l {
			l[i] = 3 * math.Sin(float64(k+i)*0.05)
		}
		k += len(l)
		c.ProcessStereo(l, r)
		c.LimiterReduction()
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}
