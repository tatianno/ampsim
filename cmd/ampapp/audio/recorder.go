package audio

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tatianno/ampsim/wav"
)

// RecordFormat é o formato das gravações (24 bits: padrão de estúdio, abre em qualquer editor).
const RecordFormat = wav.PCM24

const (
	recRingCap = 1 << 17 // ~2,7 s a 48 kHz por fila
	recEvery   = 20 * time.Millisecond
)

// Take é o resultado de uma gravação.
type Take struct {
	Out, DI string // arquivos (DI vazio se não gravou a DI)
	Seconds float64
	Dropped uint64  // amostras perdidas (o disco não acompanhou)
	PeakDB  float64 // pico da gravação (dBFS); muito baixo = gravou silêncio
}

// SilentDB é o pico abaixo do qual uma gravação é considerada muda.
const SilentDB = -60.0

// Silent informa se a gravação ficou em silêncio (ex.: fonte parada, guitarra sem sinal).
func (t Take) Silent() bool { return t.PeakDB < SilentDB }

// Recorder grava a saída (em estéreo, se a cadeia estiver em estéreo ao começar) e,
// opcionalmente, a DI: o sinal limpo da entrada, para reamplificar depois. A thread de áudio
// só copia para filas sem trava; uma goroutine escreve os arquivos.
type Recorder struct {
	r       *Renderer
	outRing *Ring[[2]float32] // L, R
	diRing  *Ring[float32]
	on      atomic.Bool
	withDI  atomic.Bool
	stereo  bool // decidido no Start (lido pela goroutine de escrita)
	dropped atomic.Uint64
	peak    float64 // maior |amostra| da saída (escrito só pela thread de áudio)

	mu      sync.Mutex
	out, di *wav.StreamWriter
	take    Take
	started time.Time
	done    chan struct{}
	err     error
}

// NewRecorder cria o gravador e o liga ao render.
func NewRecorder(r *Renderer) *Recorder {
	c := &Recorder{r: r, outRing: NewRing[[2]float32](recRingCap), diRing: NewRing[float32](recRingCap)}
	r.rec.Store(c)
	return c
}

// DefaultRecordDir é a pasta das gravações: a pasta de Música do sistema + "ampsim".
func DefaultRecordDir() string {
	if out, err := exec.Command("xdg-user-dir", "MUSIC").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return filepath.Join(d, "ampsim")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "ampsim-gravacoes"
	}
	return filepath.Join(home, "ampsim", "gravacoes")
}

// Recording informa se está gravando.
func (c *Recorder) Recording() bool { return c.on.Load() }

// Elapsed devolve o tempo gravado até agora.
func (c *Recorder) Elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on.Load() {
		return 0
	}
	return time.Since(c.started)
}

// Start começa a gravar em dir (criada se preciso). withDI grava também a entrada limpa.
func (c *Recorder) Start(dir string, withDI bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.on.Load() {
		return "", errors.New("já está gravando")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := filepath.Join(dir, "ampsim-"+time.Now().Format("2006-01-02_15-04-05"))
	fs := int(c.r.SampleRate())
	c.stereo = c.r.chain.Stereo()
	ch := 1
	if c.stereo {
		ch = 2
	}
	out, err := wav.CreateChannels(base+".wav", fs, ch, RecordFormat)
	if err != nil {
		return "", err
	}
	c.out, c.di = out, nil
	c.take = Take{Out: base + ".wav"}
	if withDI {
		if c.di, err = wav.Create(base+"-DI.wav", fs, RecordFormat); err != nil {
			out.Close()
			os.Remove(base + ".wav")
			return "", err
		}
		c.take.DI = base + "-DI.wav"
	}
	for c.drainOut(nil) || c.drain(nil, c.diRing) { // descarta restos antigos
	}
	c.dropped.Store(0)
	c.peak = 0
	c.err = nil
	c.withDI.Store(withDI)
	c.started = time.Now()
	c.done = make(chan struct{})
	c.on.Store(true)
	go c.loop(c.done)
	return c.take.Out, nil
}

// Stop termina a gravação e fecha os arquivos.
func (c *Recorder) Stop() (Take, error) {
	c.mu.Lock()
	if !c.on.Load() {
		c.mu.Unlock()
		return Take{}, errors.New("não está gravando")
	}
	c.on.Store(false) // a thread de áudio para de enviar
	done := c.done
	c.mu.Unlock()
	<-done // a goroutine escreve o que sobrou nas filas e fecha os arquivos

	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.take
	t.Seconds = float64(c.out.Frames()) / c.r.SampleRate()
	t.Dropped = c.dropped.Load()
	t.PeakDB = 20 * math.Log10(c.peak+1e-12) // a thread de áudio já parou de escrever (on = false)
	return t, c.err
}

func (c *Recorder) loop(done chan struct{}) {
	defer close(done)
	tick := time.NewTicker(recEvery)
	defer tick.Stop()
	buf := make([]float64, 0, 8192)
	for {
		<-tick.C
		running := c.on.Load()
		for c.drainOut(&buf) {
			c.write(c.out, buf)
		}
		for c.drain(&buf, c.diRing) {
			if c.di != nil {
				c.write(c.di, buf)
			}
		}
		if !running {
			c.closeFiles()
			return
		}
	}
}

// drain tira até 8192 amostras da fila para buf; devolve false se a fila estava vazia.
func (c *Recorder) drain(buf *[]float64, q *Ring[float32]) bool {
	if buf != nil {
		*buf = (*buf)[:0]
	}
	n := 0
	for n < 8192 {
		v, ok := q.Pop()
		if !ok {
			break
		}
		if buf != nil {
			*buf = append(*buf, float64(v))
		}
		n++
	}
	return n > 0
}

// drainOut tira até 4096 quadros da saída para buf (L e R intercalados, ou só o L em
// mono); devolve false se a fila estava vazia.
func (c *Recorder) drainOut(buf *[]float64) bool {
	if buf != nil {
		*buf = (*buf)[:0]
	}
	n := 0
	for n < 4096 {
		v, ok := c.outRing.Pop()
		if !ok {
			break
		}
		if buf != nil {
			*buf = append(*buf, float64(v[0]))
			if c.stereo {
				*buf = append(*buf, float64(v[1]))
			}
		}
		n++
	}
	return n > 0
}

func (c *Recorder) write(w *wav.StreamWriter, x []float64) {
	if err := w.Write(x); err != nil && c.err == nil {
		c.mu.Lock()
		c.err = fmt.Errorf("gravando: %w", err)
		c.mu.Unlock()
	}
}

func (c *Recorder) closeFiles() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range []*wav.StreamWriter{c.out, c.di} {
		if w == nil {
			continue
		}
		if err := w.Close(); err != nil && c.err == nil {
			c.err = err
		}
	}
}

// feed é chamado pela thread de áudio com a DI (entrada antes da cadeia) e a saída (L, R).
func (c *Recorder) feed(di, l, r []float64) {
	for i, v := range l {
		c.peak = math.Max(c.peak, math.Max(math.Abs(v), math.Abs(r[i])))
		if !c.outRing.Push([2]float32{float32(v), float32(r[i])}) {
			c.dropped.Add(1)
		}
	}
	if c.withDI.Load() {
		for _, v := range di {
			if !c.diRing.Push(float32(v)) {
				c.dropped.Add(1)
			}
		}
	}
}
