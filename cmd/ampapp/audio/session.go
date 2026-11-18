package audio

import (
	"sync/atomic"

	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/engine"
)

// SourceMode é a fonte que alimenta o amp.
type SourceMode int32

const (
	SourceFile   SourceMode = iota // arquivo WAV (FilePlayer)
	SourceGuitar                   // entrada ao vivo (LiveInput)
)

func (m SourceMode) String() string {
	if m == SourceGuitar {
		return "guitarra"
	}
	return "arquivo"
}

// Session junta tudo o que o app usa: cadeia, render, placa, fontes e medidores.
// O console e a GUI operam sobre a mesma Session.
type Session struct {
	Chain    *engine.Chain
	Renderer *Renderer
	Host     *Host
	Player   *source.FilePlayer
	Live     *source.LiveInput
	Monitor  *Monitor
	Tuner    *Tuner

	mode atomic.Int32
}

// NewSession prepara a cadeia na taxa fs e monta render, fontes e monitor.
// A placa não é aberta aqui: chame Host.Open.
func NewSession(chain *engine.Chain, fs float64) *Session {
	chain.Prepare(fs, MaxBlock)
	r := NewRenderer(chain, fs)
	s := &Session{
		Chain:    chain,
		Renderer: r,
		Host:     NewHost(r),
		Player:   source.NewFilePlayer(fs),
		Live:     source.NewLiveInput(fs),
	}
	r.SetLive(s.Live)
	s.Monitor = NewMonitor(r)
	s.Tuner = NewTuner(r)
	s.SetSource(SourceFile)
	return s
}

// SetSource troca a fonte (seguro com o áudio rodando).
func (s *Session) SetSource(m SourceMode) {
	s.mode.Store(int32(m))
	if m == SourceGuitar {
		s.Renderer.SetSource(s.Live)
	} else {
		s.Renderer.SetSource(s.Player)
	}
}

// Source devolve a fonte atual.
func (s *Session) Source() SourceMode { return SourceMode(s.mode.Load()) }

// Close fecha a placa e o monitor.
func (s *Session) Close() {
	s.Tuner.Stop()
	s.Host.Close()
	s.Monitor.Close()
}
