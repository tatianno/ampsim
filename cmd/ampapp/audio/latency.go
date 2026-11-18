package audio

import (
	"errors"
	"math"
	"slices"
	"sync/atomic"

	"github.com/tatianno/ampsim/cmd/ampapp/source"
)

// Teste de latência por loopback: com um cabo ligando a saída da placa a uma entrada,
// o render emite um pulso curto e mede quantas amostras ele leva para voltar.
// O resultado é a latência de ida e volta (round trip) do sistema: buffers do app, do
// servidor de som (PipeWire) e dos conversores. Não inclui a latência da cadeia de efeitos.
//
// Detecção: depois de cada pulso, a janela de entrada é gravada; o pulso de volta é o pico
// da janela, e a chegada é a primeira amostra acima de metade desse pico. Assim não importa
// quanto volume volta pelo cabo, e resto de música/eco antes do pulso não atrapalha.
const (
	LatencyAttempts = 5     // medições; o resultado é a mediana
	latencySettleMs = 400.0 // silêncio antes de cada pulso
	latencyNoiseMs  = 100.0 // o ruído de fundo é medido só no fim do silêncio (ecos antigos já passaram)
	latencyWindowMs = 500.0 // janela gravada depois do pulso (latência máxima medível)
	latencyPulseAmp = 0.7
	latencyMinPeak  = 0.01 // pico mínimo para considerar que o pulso voltou (−40 dBFS)
	latencySNR      = 4.0  // e pelo menos 4× o ruído de fundo
	// LatencyMaxSpreadMs: se as medições variarem mais que isso, o resultado não é confiável
	// (ex.: voz ou outro som entrou na janela e foi confundido com o pulso).
	LatencyMaxSpreadMs = 1.0
)

// ErrNoInput indica que o teste precisa da entrada ao vivo aberta.
var ErrNoInput = errors.New("o teste de latência precisa de uma entrada aberta (cabo da saída para a entrada)")

// LatencyResult é o estado do teste de latência.
type LatencyResult struct {
	Running    bool
	Done       bool    // terminou (com ou sem sucesso)
	OK         bool    // conseguiu medir
	Frames     float64 // latência de ida e volta, em amostras
	Ms         float64
	Successes  int     // medições que acharam o pulso
	SpreadMs   float64 // diferença entre a maior e a menor medição
	Consistent bool    // SpreadMs ≤ LatencyMaxSpreadMs

	// diagnóstico da última medição (útil quando falha)
	PeakDB  float64 // pico que voltou pela entrada
	NoiseDB float64 // ruído de fundo medido antes do pulso
}

const (
	ltIdle = iota
	ltSettle
	ltWait
)

type latencyTest struct {
	// pedidos e resultado (atomics: GUI ↔ thread de áudio)
	request atomic.Bool
	running atomic.Bool
	done    atomic.Bool
	result  atomic.Uint64 // float64 bits (amostras); NaN = falhou
	spread  atomic.Uint64 // float64 bits (amostras)
	success atomic.Int32
	peak    atomic.Uint64 // float64 bits: pico da última janela
	noiseP  atomic.Uint64 // float64 bits: ruído da última medição

	// estado da thread de áudio
	state   int
	count   int
	noise   float64
	since   int
	win     []float32 // janela depois do pulso (pré-alocada)
	attempt int
	found   [LatencyAttempts]float64
	nFound  int
}

// StartLatencyTest inicia o teste (a saída vai tocar 5 cliques curtos).
func (r *Renderer) StartLatencyTest() error {
	if r.live.Load() == nil {
		return ErrNoInput
	}
	r.lt.done.Store(false)
	r.lt.request.Store(true)
	return nil
}

// LatencyResult devolve o estado do teste de latência.
func (r *Renderer) LatencyResult() LatencyResult {
	res := LatencyResult{Running: r.lt.running.Load() || r.lt.request.Load(), Done: r.lt.done.Load()}
	if res.Done {
		f := math.Float64frombits(r.lt.result.Load())
		res.Successes = int(r.lt.success.Load())
		if !math.IsNaN(f) {
			res.OK, res.Frames, res.Ms = true, f, 1000*f/r.fs
			res.SpreadMs = 1000 * math.Float64frombits(r.lt.spread.Load()) / r.fs
			res.Consistent = res.SpreadMs <= LatencyMaxSpreadMs
		}
		res.PeakDB = 20 * math.Log10(math.Float64frombits(r.lt.peak.Load())+1e-9)
		res.NoiseDB = 20 * math.Log10(math.Float64frombits(r.lt.noiseP.Load())+1e-9)
	}
	return res
}

// step roda o teste dentro do callback. Devolve true se o teste está ativo
// (aí a saída já foi preenchida e o processamento normal não roda).
func (t *latencyTest) step(r *Renderer, out []float32, outCh, frames int, l *source.LiveInput) bool {
	if t.request.Swap(false) {
		t.state, t.count, t.noise, t.attempt, t.nFound = ltSettle, 0, 0, 0, 0
		t.running.Store(true)
	}
	if t.state == ltIdle {
		return false
	}
	settleN := int(latencySettleMs / 1000 * r.fs)
	noiseFrom := settleN - int(latencyNoiseMs/1000*r.fs)

	for i := 0; i < frames; i++ {
		x := math.Abs(l.RawAt(i))
		y := 0.0
		switch t.state {
		case ltSettle:
			if t.count >= noiseFrom {
				t.noise = math.Max(t.noise, x)
			}
			if t.count++; t.count >= settleN {
				t.state, t.since = ltWait, 0
			}
		case ltWait:
			// pulso de 2 amostras (+, −): curto e sem componente DC
			switch t.since {
			case 0:
				y = latencyPulseAmp
			case 1:
				y = -latencyPulseAmp
			}
			t.win[t.since] = float32(x)
			if t.since++; t.since == len(t.win) {
				t.analyze()
			}
		}
		f := float32(y)
		for c := 0; c < outCh; c++ {
			if c < OutputChannels {
				out[i*outCh+c] = f
			} else {
				out[i*outCh+c] = 0
			}
		}
		if t.state == ltIdle {
			clear(out[(i+1)*outCh : frames*outCh])
			break
		}
	}
	return true
}

// analyze procura o pulso na janela gravada e passa para a próxima medição.
func (t *latencyTest) analyze() {
	peak := float32(0)
	for _, v := range t.win[1:] { // a amostra 0 é o instante do pulso
		peak = max(peak, v)
	}
	t.peak.Store(math.Float64bits(float64(peak)))
	t.noiseP.Store(math.Float64bits(t.noise))
	if float64(peak) >= math.Max(latencyMinPeak, latencySNR*t.noise) {
		half := peak / 2
		for i := 1; i < len(t.win); i++ {
			if t.win[i] >= half {
				t.found[t.nFound] = float64(i)
				t.nFound++
				break
			}
		}
	}
	t.next()
}

// next passa para a próxima medição ou encerra o teste.
func (t *latencyTest) next() {
	t.attempt++
	if t.attempt < LatencyAttempts {
		t.state, t.count, t.noise = ltSettle, 0, 0
		return
	}
	t.state = ltIdle
	res, spread := math.NaN(), 0.0
	if t.nFound >= 3 {
		v := t.found[:t.nFound]
		slices.Sort(v) // no máximo 5 valores, ordenação in-place sem alocar
		res = v[len(v)/2]
		spread = v[len(v)-1] - v[0]
	}
	t.spread.Store(math.Float64bits(spread))
	t.result.Store(math.Float64bits(res))
	t.success.Store(int32(t.nFound))
	t.running.Store(false)
	t.done.Store(true)
}
