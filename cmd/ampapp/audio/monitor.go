package audio

import (
	"sync"
	"time"
)

// Levels é o resumo dos medidores desde a última leitura.
type Levels struct {
	InPeak, OutPeak float32 // pico linear
	Clips           uint64  // total acumulado de amostras cortadas
	MaxLoad         float32 // maior carga de CPU de um callback (0..1+)
	GateOpen        bool
	Bypassed        bool
}

// Monitor consome a fila de medidores do Renderer numa goroutine e acumula os picos
// entre leituras (a GUI e o console leem com Take).
type Monitor struct {
	r    *Renderer
	mu   sync.Mutex
	acc  Levels
	stop chan struct{}
}

// NewMonitor cria e inicia o monitor.
func NewMonitor(r *Renderer) *Monitor {
	m := &Monitor{r: r, stop: make(chan struct{})}
	go m.run()
	return m
}

func (m *Monitor) run() {
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		m.mu.Lock()
		for {
			v, ok := m.r.Meters().Pop()
			if !ok {
				break
			}
			m.acc.InPeak = max(m.acc.InPeak, v.InPeak)
			m.acc.OutPeak = max(m.acc.OutPeak, v.OutPeak)
			m.acc.Clips += uint64(v.OutClips)
			m.acc.MaxLoad = max(m.acc.MaxLoad, v.Load)
			m.acc.GateOpen = v.GateOpen
			m.acc.Bypassed = v.Bypassed
		}
		m.mu.Unlock()
	}
}

// Take devolve os valores acumulados e zera os picos (Clips continua acumulando).
func (m *Monitor) Take() Levels {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.acc
	m.acc.InPeak, m.acc.OutPeak, m.acc.MaxLoad = 0, 0, 0
	return l
}

// Close para a goroutine.
func (m *Monitor) Close() { close(m.stop) }
