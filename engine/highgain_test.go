package engine

import (
	"math"
	"testing"

	"github.com/tatianno/ampsim/dsp"
)

// Trocar para o High Gain e voltar com o áudio rodando: sem estalo, saída idêntica com
// qualquer tamanho de bloco, e o som muda de verdade.
func TestHighGainTrocaDeVoz(t *testing.T) {
	fs, di := lerDI(t)
	di = di[:40000]
	render := func(bloco int, trocas map[int]float64) []float64 {
		c := NewChain(NewPre(), NewPower())
		c.Prepare(fs, 1024)
		out := append([]float64(nil), di...)
		for i := 0; i < len(out); i += bloco {
			if v, ok := trocas[i]; ok {
				c.Module(PreID).Param("stack").Set(v)
			}
			c.ProcessMono(out[i:min(i+bloco, len(out))])
		}
		return out
	}
	trocas := map[int]float64{10240: StackHighGain, 20480: StackBritish, 30720: StackHighGain}
	ref := render(512, trocas)
	maxJump := 0.0
	for i := 1; i < len(ref); i++ {
		if math.IsNaN(ref[i]) || math.Abs(ref[i]) > 4 {
			t.Fatalf("valor inválido na amostra %d: %g", i, ref[i])
		}
		maxJump = math.Max(maxJump, math.Abs(ref[i]-ref[i-1]))
	}
	t.Logf("salto máx %.3f", maxJump)
	if maxJump > 1 {
		t.Errorf("salto de %.3f entre amostras (estalo?)", maxJump)
	}
	for _, b := range []int{16, 64, 1024} {
		r := render(b, trocas)
		for i := range r {
			if r[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere", b, i)
			}
		}
	}
	britanico := render(512, map[int]float64{0: StackBritish})
	diff := 0.0
	for i := 12000; i < 20000; i++ {
		diff += (ref[i] - britanico[i]) * (ref[i] - britanico[i])
	}
	if diff < 1e-2 {
		t.Error("o High Gain deveria soar diferente do Britânico")
	}
}

// O High Gain tem a mesma latência do pré clássico (a cadeia não muda de latência): o pico da
// resposta a um impulso pequeno sai no mesmo lugar nas duas vozes, perto da latência declarada
// (a resposta começa um pouco antes: o FIR de fase linear do oversampling tem pré-eco).
func TestHighGainLatencia(t *testing.T) {
	pico := func(k float64) int {
		c := NewChain(NewPre())
		c.Module(PreID).Param("stack").Set(k)
		c.Module(PreID).Param("gain").Set(0)
		c.Prepare(48000, 2048)
		x := make([]float64, 2048)
		c.ProcessMono(x)
		x = make([]float64, 2048)
		x[100] = 1e-4 // pequeno: a resposta fica quase linear
		c.ProcessMono(x)
		at, peak := 0, 0.0
		for i, v := range x {
			if math.Abs(v) > peak {
				at, peak = i-100, math.Abs(v)
			}
		}
		return at
	}
	br, hg := pico(StackBritish), pico(StackHighGain)
	t.Logf("pico: Britânico %d, High Gain %d (latência declarada %d)", br, hg, dsp.PreampLatency)
	if hg < dsp.PreampLatency-2 || hg > dsp.PreampLatency+4 || abs(hg-br) > 4 {
		t.Errorf("High Gain com pico em %d, Britânico em %d", hg, br)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// A voz Britânico Mod vai do crunch ao saturado ao longo do knob de ganho (THD de uma senoide
// no nível da DI de teste), sem chegar ao High Gain.
func TestBritanicoModSatura(t *testing.T) {
	const fs, f0, amp = 48000.0, 187.5, 0.12
	thd := func(stack int, gain float64) float64 {
		c := NewDefaultChain()
		c.Pre().Param("stack").Set(float64(stack))
		c.Pre().Param("gain").Set(gain)
		x := make([]float64, int(fs*1.5))
		for i := range x {
			x[i] = amp * math.Sin(2*math.Pi*f0*float64(i)/fs)
		}
		y, err := RenderOffline(c.State(), x, fs, 0, 256, nil)
		if err != nil {
			t.Fatal(err)
		}
		seg := y[int(fs) : int(fs)+int(fs/2)]
		g := func(f float64) float64 {
			w := 2 * math.Pi * f / fs
			k := 2 * math.Cos(w)
			var s1, s2 float64
			for _, v := range seg {
				s1, s2 = v+k*s1-s2, s1
			}
			return s1*s1 + s2*s2 - k*s1*s2
		}
		var hn float64
		for k := 2; k <= 15; k++ {
			hn += g(f0 * float64(k))
		}
		return 100 * math.Sqrt(hn/g(f0))
	}
	low, mid, high := thd(StackBritishMod, 0), thd(StackBritishMod, 5), thd(StackBritishMod, 8)
	brit, hg := thd(StackBritish, 10), thd(StackHighGain, 8)
	t.Logf("Britânico Mod: %.1f%% (0), %.1f%% (5), %.1f%% (8); Britânico 10: %.1f%%; High Gain 8: %.1f%%", low, mid, high, brit, hg)
	if !(low < 3 && mid > low && high > mid && high > 1.5*brit && high < hg) {
		t.Error("a Britânico Mod deveria ir do limpo ao saturado, acima do Britânico e abaixo do High Gain")
	}
}
