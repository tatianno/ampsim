package audio

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/tatianno/ampsim/dsp"
)

// Analisador de espectro: a thread de áudio copia a entrada (antes da cadeia) e a saída (o
// que vai para a placa) para uma fila sem trava enquanto ele está ligado; a GUI esvazia a
// fila e calcula os espectros a cada quadro (Update).
const (
	AnalyzerFFT       = 8192 // ~5,9 Hz por raia a 48 kHz (o eixo é logarítmico desde 20 Hz)
	analyzerRingCap   = 1 << 15
	analyzerMinNew    = 512  // amostras novas para recalcular
	analyzerFloorDB   = -120 // piso dos valores devolvidos
	analyzerHannENBW  = 1.5  // largura de banda equivalente da janela de Hann (raias)
	analyzerRise      = 0.6  // suavização ao subir (0 = instantâneo)
	analyzerFall      = 0.85 // suavização ao descer (mais lenta: a curva não pisca)
	analyzerInputIdx  = 0
	analyzerOutputIdx = 1
)

// Sinais do analisador.
const (
	SpecInput  = analyzerInputIdx  // entrada, antes da cadeia
	SpecOutput = analyzerOutputIdx // saída, depois da cadeia
)

// Analyzer é o analisador de espectro (entrada e saída).
type Analyzer struct {
	ring    *Ring[[2]float32]
	running atomic.Bool
	fs      float64

	mu     sync.Mutex // só entre chamadas da GUI (Update × Bands)
	hist   [2][]float64
	pos    int // próxima escrita em hist (circular)
	filled int
	fresh  int // amostras novas desde o último cálculo
	fft    *dsp.FFT
	win    []float64
	z      []complex128
	power  [2][]float64 // potência suavizada por raia (1 = senoide de fundo de escala)
	ready  bool
}

// NewAnalyzer cria o analisador e o liga ao render (começa desligado).
func NewAnalyzer(r *Renderer) *Analyzer {
	n := AnalyzerFFT
	a := &Analyzer{ring: NewRing[[2]float32](analyzerRingCap), fs: r.SampleRate(), fft: dsp.NewFFT(n),
		win: make([]float64, n), z: make([]complex128, n)}
	for i := range a.win {
		a.win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	for c := range a.hist {
		a.hist[c] = make([]float64, n)
		a.power[c] = make([]float64, n/2+1)
	}
	r.analyzer.Store(a)
	return a
}

// Start liga o analisador.
func (a *Analyzer) Start() {
	if a.running.Swap(true) {
		return
	}
	a.mu.Lock()
	a.filled, a.fresh, a.ready = 0, 0, false
	for c := range a.power {
		clear(a.power[c])
	}
	a.mu.Unlock()
}

// Stop desliga o analisador (a thread de áudio para de copiar).
func (a *Analyzer) Stop() { a.running.Store(false) }

// Running informa se está ligado.
func (a *Analyzer) Running() bool { return a.running.Load() }

// SampleRate devolve a taxa do sinal analisado.
func (a *Analyzer) SampleRate() float64 { return a.fs }

// feed é chamado pela thread de áudio (só quando ligado). Não aloca.
func (a *Analyzer) feed(in, out []float64) {
	for i := range in {
		if !a.ring.Push([2]float32{float32(in[i]), float32(out[i])}) {
			return // fila cheia: a GUI não está lendo (janela minimizada); descarta
		}
	}
}

// Update esvazia a fila e, se chegou sinal novo suficiente, recalcula os espectros.
// Devolve true se há espectro para mostrar. Chamar da GUI.
func (a *Analyzer) Update() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.win)
	for {
		v, ok := a.ring.Pop()
		if !ok {
			break
		}
		a.hist[0][a.pos], a.hist[1][a.pos] = float64(v[0]), float64(v[1])
		a.pos = (a.pos + 1) % n
		a.filled = min(n, a.filled+1)
		a.fresh++
	}
	if a.filled < n || a.fresh < analyzerMinNew {
		return a.ready
	}
	a.fresh = 0
	// as duas FFTs reais numa complexa: z = entrada + j·saída
	for i := range a.z {
		k := (a.pos + i) % n // do mais antigo ao mais recente
		a.z[i] = complex(a.hist[0][k]*a.win[i], a.hist[1][k]*a.win[i])
	}
	a.fft.Forward(a.z)
	norm := 1 / (float64(n) / 4 * float64(n) / 4) // senoide de amplitude 1 → pico 1 na raia
	for k := 0; k <= n/2; k++ {
		zk, zn := a.z[k], a.z[(n-k)%n]
		cn := complex(real(zn), -imag(zn))
		xi := (zk + cn) / 2
		xo := (zk - cn) / complex(0, 2)
		for c, x := range [2]complex128{xi, xo} {
			p := (real(x)*real(x) + imag(x)*imag(x)) * norm
			old := a.power[c][k]
			s := analyzerFall
			if p > old {
				s = analyzerRise
			}
			if !a.ready {
				s = 0
			}
			a.power[c][k] = s*old + (1-s)*p
		}
	}
	a.ready = true
	return true
}

// Bands escreve em dst (dBFS) o nível do sinal which (SpecInput/SpecOutput) em faixas de
// largura oct (em oitavas, ex.: 1/6) centradas em freqs. Uma senoide de fundo de escala lê
// 0 dB. Chamar da GUI depois de Update.
func (a *Analyzer) Bands(which int, freqs []float64, oct float64, dst []float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pw := a.power[which]
	binHz := a.fs / float64(len(a.win))
	half := math.Pow(2, oct/2)
	for i, f := range freqs {
		lo := int(math.Ceil(f / half / binHz))
		hi := int(math.Floor(f * half / binHz))
		s := 0.0
		if hi-lo < 2 { // faixa com menos de 3 raias (graves): interpola entre as raias vizinhas
			x := f / binHz
			k := min(len(pw)-2, max(1, int(x)))
			t := math.Max(0, math.Min(1, x-float64(k)))
			// vezes a largura da faixa em raias (mín. 1): continua com a soma das faixas largas
			s = ((1-t)*pw[k] + t*pw[k+1]) * math.Max(1, f*(half-1/half)/binHz)
		} else {
			lo, hi = max(1, lo), min(len(pw)-1, hi)
			for k := lo; k <= hi; k++ {
				s += pw[k]
			}
		}
		db := float64(analyzerFloorDB)
		if s > 0 {
			db = math.Max(db, 10*math.Log10(s/analyzerHannENBW))
		}
		dst[i] = db
	}
}
