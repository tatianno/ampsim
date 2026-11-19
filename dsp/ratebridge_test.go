package dsp

import (
	"math"
	"testing"
)

// identity copia a entrada; contador conta as amostras recebidas na taxa interna.
type identity struct{ n int }

func (p *identity) Process(in, out []float32) { copy(out, in); p.n += len(in) }

// runBridge processa x em blocos de tamanho bs (o último pode ser menor).
func runBridge(b *RateBridge, x []float32, bs int) []float32 {
	y := make([]float32, len(x))
	for i := 0; i < len(x); i += bs {
		j := min(i+bs, len(x))
		b.Process(x[i:j], y[i:j])
	}
	return y
}

func sine(f, fs float64, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(0.5 * math.Sin(2*math.Pi*f*float64(i)/fs))
	}
	return x
}

// errDB é o erro RMS de y contra x atrasado de d amostras, em dB do RMS de x (ignora o começo).
func errDB(x, y []float32, d, skip int) float64 {
	var e, r float64
	for i := skip; i < len(y); i++ {
		diff := float64(y[i]) - float64(x[i-d])
		e += diff * diff
		r += float64(x[i-d]) * float64(x[i-d])
	}
	return 10 * math.Log10(e/r)
}

var bridgeRates = []float64{44100, 22050, 32000, 88200, 96000}

func TestRateBridgeRoundTrip(t *testing.T) {
	for _, fs := range bridgeRates {
		id := &identity{}
		b, err := NewRateBridge(fs, 48000, 512, PreampLatency, id)
		if err != nil {
			t.Fatalf("%g: %v", fs, err)
		}
		if b.Latency() < 1 || b.Latency() > PreampLatency {
			t.Errorf("%g: latência %d", fs, b.Latency())
		}
		n := int(fs) // 1 s
		// acima de 48 kHz as 32 amostras de latência são pouco tempo (0,33 ms a 96 kHz): o
		// filtro fica mais curto e a rejeição cai para ~50 dB
		limit := -60.0
		if fs > 48000 {
			limit = -45
		}
		// tons dentro da banda útil (até 0,41·min(fs, 48k))
		band := bridgeBand * math.Min(fs, 48000)
		for _, f := range []float64{100, 1000, 5000, 0.9 * band} {
			b.Reset()
			x := sine(f, fs, n)
			y := runBridge(b, x, 256)
			if e := errDB(x, y, b.Latency(), 1000); e > limit {
				t.Errorf("%g Hz → %g Hz: tom de %.0f Hz com erro %.1f dB", fs, 48000.0, f, e)
			}
		}
		// a taxa interna recebe a quantidade certa de amostras (≈ n·48k/fs)
		if want := float64(n) * 48000 / fs; math.Abs(float64(id.n)/4-want) > 2 {
			t.Errorf("%g: %d amostras internas em 4 passadas, esperado %.0f cada", fs, id.n, want)
		}
		t.Logf("%g Hz: latência %d amostras, %d coeficientes por fase (ida) e %d (volta)", fs, b.Latency(), b.up.taps, b.dn.taps)
	}
}

// Impulso: o pico sai exatamente na latência declarada.
func TestRateBridgeImpulse(t *testing.T) {
	for _, fs := range bridgeRates {
		b, _ := NewRateBridge(fs, 48000, 64, PreampLatency, &identity{})
		x := make([]float32, 400)
		x[100] = 1
		y := runBridge(b, x, 64)
		peak := 0
		for i := range y {
			if math.Abs(float64(y[i])) > math.Abs(float64(y[peak])) {
				peak = i
			}
		}
		if peak != 100+b.Latency() {
			t.Errorf("%g: pico em %d, esperado %d", fs, peak, 100+b.Latency())
		}
	}
}

// Rejeição: um tom acima da banda útil da taxa interna (que dobraria para dentro da banda)
// é atenuado na ida. Com 44,1 kHz → 48 kHz, as imagens do tom de 1 kHz ficariam em
// 43,1 kHz e 45,1 kHz (dobrando para 4,9 e 2,9 kHz); medimos o que entra na taxa interna.
func TestRateBridgeImages(t *testing.T) {
	cap := &capture{}
	b, _ := NewRateBridge(44100, 48000, 256, PreampLatency, cap)
	runBridge(b, sine(1000, 44100, 44100), 256)
	y := cap.buf[4800:]
	// remove o tom de 1 kHz por mínimos quadrados e mede o que sobra
	var ss, cc, sc, ys, yc float64
	for i, v := range y {
		w := 2 * math.Pi * 1000 * float64(i) / 48000
		s, c := math.Sin(w), math.Cos(w)
		ss += s * s
		cc += c * c
		sc += s * c
		ys += float64(v) * s
		yc += float64(v) * c
	}
	det := ss*cc - sc*sc
	a := (ys*cc - yc*sc) / det
	bb := (yc*ss - ys*sc) / det
	var res, tot float64
	for i, v := range y {
		w := 2 * math.Pi * 1000 * float64(i) / 48000
		r := float64(v) - a*math.Sin(w) - bb*math.Cos(w)
		res += r * r
		tot += float64(v) * float64(v)
	}
	if e := 10 * math.Log10(res/tot); e > -70 {
		t.Errorf("imagens na taxa interna: %.1f dB", e)
	}
}

type capture struct{ buf []float32 }

func (c *capture) Process(in, out []float32) { c.buf = append(c.buf, in...); copy(out, in) }

func TestRateBridgeBlockInvariance(t *testing.T) {
	x := sine(440, 44100, 20000)
	for i := range x { // um pouco de ruído para não ser só um tom
		x[i] += float32(0.1 * math.Sin(float64(i*i)*0.001))
	}
	var ref []float32
	for _, bs := range []int{1, 37, 64, 500, 512} {
		b, _ := NewRateBridge(44100, 48000, 512, PreampLatency, &identity{})
		y := runBridge(b, x, bs)
		if ref == nil {
			ref = y
			continue
		}
		for i := range y {
			if y[i] != ref[i] {
				t.Fatalf("bloco %d: amostra %d difere (%g × %g)", bs, i, y[i], ref[i])
			}
		}
	}
}

func TestRateBridgeNoAlloc(t *testing.T) {
	b, _ := NewRateBridge(44100, 48000, 512, PreampLatency, &identity{})
	x := sine(440, 44100, 512)
	y := make([]float32, 512)
	if a := testing.AllocsPerRun(50, func() {
		b.Process(x, y)
		b.Process(x[:37], y[:37])
		b.Process(x[:1], y[:1])
	}); a != 0 {
		t.Errorf("%v alocações por bloco", a)
	}
}

func TestRateBridgeErrors(t *testing.T) {
	if _, err := NewRateBridge(48000, 48000, 64, 32, &identity{}); err == nil {
		t.Error("taxas iguais deveriam dar erro")
	}
	if _, err := NewRateBridge(44100, 48000, 64, 3, &identity{}); err == nil {
		t.Error("latência de 3 amostras deveria ser curta demais")
	}
	if _, err := NewRateBridge(44100.5, 48000, 64, 32, &identity{}); err == nil {
		t.Error("taxa fracionária deveria dar erro")
	}
}

func BenchmarkRateBridge(b *testing.B) {
	br, _ := NewRateBridge(44100, 48000, 256, PreampLatency, &identity{})
	x := sine(440, 44100, 256)
	y := make([]float32, 256)
	b.ReportAllocs()
	for b.Loop() {
		br.Process(x, y)
	}
}
