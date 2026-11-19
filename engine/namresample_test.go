package engine

import (
	"math"
	"testing"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/nam"
)

// lerDI44 lê a DI de teste em 44,1 kHz (uma placa em taxa diferente da dos modelos).
func lerDI44(t *testing.T) []float64 {
	t.Helper()
	fs, di := lerDI(t)
	if fs != 44100 {
		di = dsp.ResampleRate(di, int(fs), 44100)
	}
	return di
}

// Com o áudio a 44,1 kHz, o modelo (48 kHz) roda reamostrado em streaming. A saída tem
// que bater com o caminho offline (reamostra a DI inteira para 48 kHz com resample_poly,
// roda a rede, volta para 44,1 kHz), atrasada da latência declarada do bloco.
func TestNAMReamostradoIgualAoOffline(t *testing.T) {
	di := lerDI44(t)[:44100]
	for _, name := range []string{"Clean V10 B5 M5 T5 P5", "Dist Sat5 V4 B5 M5 T5 P5"} {
		ref := capturas.Prefix + name
		for _, size := range []int{NAMLite, NAMFull} {
			n := &namBlock{latency: dsp.PreampLatency}
			if err := n.Load(ref); err != nil {
				t.Fatal(err)
			}
			n.prepare(44100, 256)
			y := append([]float64(nil), di...)
			for i := 0; i < len(y); i += 256 {
				n.process(y[i:min(i+256, len(y))], 1, size)
			}
			info := n.Info()
			if info.Warning != "" || info.Resampled == "" {
				t.Errorf("info: %+v", info)
			}

			// offline
			m := n.inst.model
			sm := m.Submodels[n.inst.netFor(size)]
			x48 := dsp.ResampleRate(di, 44100, 48000)
			in := make([]float32, len(x48))
			for i, v := range x48 {
				in[i] = float32(v)
			}
			net := nam.NewNet(sm.Spec, 512)
			net.Prewarm()
			net.Process(in, in)
			o48 := make([]float64, len(in))
			for i, v := range in {
				o48[i] = float64(v)
			}
			off := dsp.ResampleRate(o48, 48000, 44100)

			// compara até 15 kHz: acima de 18 kHz a ponte corta de propósito (e o pré distorcido,
			// sem gabinete, tem bastante energia lá); o mesmo filtro nos dois mantém o alinhamento
			yl, ol := passaBaixa15k(y), passaBaixa15k(off)
			lat := dsp.PreampLatency
			var e, r float64
			for i := 4410; i < len(y); i++ {
				d := yl[i] - ol[i-lat]
				e += d * d
				r += ol[i-lat] * ol[i-lat]
			}
			db := 10 * math.Log10(e/r)
			t.Logf("%-26s %s: erro %.1f dB contra o offline (até 15 kHz)", name, info.Size, db)
			if db > -40 {
				t.Errorf("%s %s: erro %.1f dB", name, info.Size, db)
			}
		}
	}
}

func passaBaixa15k(x []float64) []float64 {
	h := dsp.LowpassFIR(255, 15000/22050.0, 8)
	y := make([]float64, len(x))
	for i := range y {
		acc := 0.0
		for j, c := range h {
			if i-j >= 0 {
				acc += c * x[i-j]
			}
		}
		y[i] = acc
	}
	return y
}

// A 44,1 kHz a cadeia mantém a latência, a invariância de bloco e o zero alocação no modo NAM.
func TestNAMReamostradoCadeia(t *testing.T) {
	di := lerDI44(t)[:22050]
	var ref []float64
	for _, bs := range []int{1, 37, 256, 512} {
		c := NewDefaultChain()
		c.Pre().Param("amp").Set(AmpNAM)
		c.Prepare(44100, 512)
		y := renderCadeia(c, di, bs)
		if ref == nil {
			ref = y
			if c.Pre().Latency() != dsp.PreampLatency {
				t.Errorf("latência do pré %d", c.Pre().Latency())
			}
			continue
		}
		for i := range y {
			if y[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere", bs, i)
			}
		}
	}

	c := NewDefaultChain()
	c.Prepare(44100, 256)
	p := c.Pre()
	buf := make([]float64, 256)
	k := 0
	if a := testing.AllocsPerRun(80, func() {
		k++
		p.Param("amp").Set(float64(k / 20 % 2))
		p.Param("nam_size").Set(float64(k / 7 % 2))
		c.ProcessMono(buf)
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}
}

// O impulso sai alinhado com o do simulado também a 44,1 kHz.
func TestNAMReamostradoLatencia(t *testing.T) {
	c := NewChain(NewPre())
	c.Pre().Param("amp").Set(AmpNAM)
	c.Prepare(44100, 256)
	c.ProcessMono(make([]float64, 1024))
	x := make([]float64, 2048)
	x[100] = 0.5
	c.ProcessMono(x)
	first := -1
	for i, v := range x {
		if math.Abs(v-x[0]) > 1e-4 {
			first = i
			break
		}
	}
	if first < 100 || first > 100+dsp.PreampLatency+2 {
		t.Errorf("resposta começa na amostra %d", first)
	}
	t.Logf("resposta começa na amostra %d (latência declarada %d)", first, dsp.PreampLatency)
}
