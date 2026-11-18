package audio

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/tatianno/ampsim/dsp"
)

// Faixa do afinador: de Si 1 (guitarra de 7 cordas / afinações baixas) a ~Fá 6.
const (
	tunerMinHz   = 55.0
	tunerMaxHz   = 1400.0
	tunerWindow  = 2048 // ~43 ms a 48 kHz
	tunerEvery   = 40 * time.Millisecond
	tunerRingCap = 1 << 15
)

// Reading é uma leitura do afinador.
type Reading struct {
	OK      bool
	Freq    float64
	Note    string
	Octave  int
	Cents   float64 // −50..+50 até a nota mais próxima
	Clarity float64
}

// Tuner é o afinador: a thread de áudio só copia o sinal de entrada (antes da cadeia)
// para uma fila sem trava enquanto ele está ligado; a detecção roda numa goroutine.
type Tuner struct {
	r       *Renderer
	ring    *Ring[float32]
	running atomic.Bool
	a4      atomic.Uint64 // float64 bits
	reading atomic.Pointer[Reading]
	stop    chan struct{}
}

// NewTuner cria o afinador e o liga ao render.
func NewTuner(r *Renderer) *Tuner {
	t := &Tuner{r: r, ring: NewRing[float32](tunerRingCap)}
	t.SetA4(440)
	t.reading.Store(&Reading{})
	r.tuner.Store(t)
	return t
}

// SetA4 ajusta a referência do Lá 4 (430–450 Hz).
func (t *Tuner) SetA4(hz float64) {
	t.a4.Store(math.Float64bits(math.Max(430, math.Min(450, hz))))
}

// A4 devolve a referência atual.
func (t *Tuner) A4() float64 { return math.Float64frombits(t.a4.Load()) }

// Running informa se o afinador está ligado.
func (t *Tuner) Running() bool { return t.running.Load() }

// Reading devolve a última leitura.
func (t *Tuner) Reading() Reading { return *t.reading.Load() }

// Start liga o afinador (a goroutine de detecção).
func (t *Tuner) Start() {
	if t.running.Swap(true) {
		return
	}
	for { // descarta o que sobrou de uma sessão anterior
		if _, ok := t.ring.Pop(); !ok {
			break
		}
	}
	t.reading.Store(&Reading{})
	t.stop = make(chan struct{})
	go t.loop(t.stop)
}

// Stop desliga o afinador.
func (t *Tuner) Stop() {
	if !t.running.Swap(false) {
		return
	}
	close(t.stop)
}

func (t *Tuner) loop(stop chan struct{}) {
	det := dsp.NewPitchDetector(t.r.SampleRate(), tunerMinHz, tunerMaxHz, tunerWindow)
	need := det.Need()
	buf := make([]float64, 0, 4*need)
	tick := time.NewTicker(tunerEvery)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		for {
			v, ok := t.ring.Pop()
			if !ok {
				break
			}
			buf = append(buf, float64(v))
		}
		if len(buf) > 2*need { // mantém só o trecho mais recente
			buf = append(buf[:0], buf[len(buf)-need:]...)
		}
		if len(buf) < need {
			continue
		}
		x := buf[len(buf)-need:]
		f, clarity, ok := det.Detect(x)
		rd := &Reading{OK: ok, Freq: f, Clarity: clarity}
		if ok {
			rd.Note, rd.Octave, rd.Cents = dsp.NoteOf(f, t.A4())
		}
		t.reading.Store(rd)
	}
}

// feed é chamado pela thread de áudio com o sinal de entrada (só quando ligado).
func (t *Tuner) feed(buf []float64) {
	for _, v := range buf {
		if !t.ring.Push(float32(v)) {
			return // fila cheia: a goroutine está atrasada; descarta
		}
	}
}
