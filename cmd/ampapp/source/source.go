// Pacote source fornece as fontes de áudio do app standalone: arquivo WAV (FilePlayer)
// e, na Fase 4, a entrada ao vivo da guitarra.
//
// Read roda na thread de áudio: não aloca, não trava. Os comandos (Play, Seek, Load...)
// vêm da GUI/console e são passados por atomics.
package source

import (
	"fmt"
	"math"
	"path/filepath"
	"sync/atomic"

	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/wav"
)

// Source preenche um bloco mono com as próximas amostras.
type Source interface {
	Read(buf []float64)
}

// Clip é um áudio já decodificado, em mono e na taxa do dispositivo.
type Clip struct {
	Name    string
	Path    string
	FS      int
	Samples []float64
}

// Duration devolve a duração em segundos.
func (c *Clip) Duration() float64 { return float64(len(c.Samples)) / float64(c.FS) }

// FadeMs é a rampa usada em play/pause/seek e nas emendas do loop.
const FadeMs = 5.0 // float: 5/1000 entre constantes inteiras daria 0

// FilePlayer toca um Clip com play/pause, posição, loop e trecho de loop.
type FilePlayer struct {
	fs float64

	// escritos pela GUI, lidos pela thread de áudio
	clip      atomic.Pointer[Clip]
	playing   atomic.Bool
	loop      atomic.Bool
	loopStart atomic.Int64 // amostras
	loopEnd   atomic.Int64 // amostras; <= 0 = até o fim
	seekReq   atomic.Int64 // -1 = nenhum pedido

	// publicado pela thread de áudio
	posPub atomic.Int64

	// estado da thread de áudio
	cur  *Clip
	pos  int
	g    float64 // rampa de play/pause
	step float64
	fade int
}

// NewFilePlayer cria um player para a taxa do dispositivo fs.
func NewFilePlayer(fs float64) *FilePlayer {
	p := &FilePlayer{fs: fs}
	p.step = 1 / (FadeMs / 1000 * fs)
	p.fade = int(FadeMs / 1000 * fs)
	p.seekReq.Store(-1)
	p.loop.Store(true)
	return p
}

// SampleRate devolve a taxa do player.
func (p *FilePlayer) SampleRate() float64 { return p.fs }

// Load lê um WAV, converte para mono e reamostra para a taxa do dispositivo.
// Roda fora da thread de áudio (aloca e faz I/O); a troca é atômica.
func (p *FilePlayer) Load(path string) error {
	a, err := wav.ReadFile(path)
	if err != nil {
		return err
	}
	if a.Len() == 0 {
		return fmt.Errorf("%s: arquivo sem amostras", path)
	}
	mono := a.Mono() // normaliza cada canal antes da média (o bug do main.py)
	if a.SampleRate != int(p.fs) {
		mono = dsp.ResampleRate(mono, a.SampleRate, int(p.fs))
	}
	// rampas nas bordas: um corte abrupto no fim da gravação vira estalo no amp. Aplicadas
	// aqui, valem igual para o play e para a exportação.
	dsp.FadeEdges(mono, p.fs, dsp.FileFadeInMs, dsp.FileFadeOutMs)
	p.SetClip(&Clip{Name: filepath.Base(path), Path: path, FS: int(p.fs), Samples: mono})
	return nil
}

// SetClip troca o clip (o novo começa do início, com o loop no arquivo inteiro).
func (p *FilePlayer) SetClip(c *Clip) {
	p.loopStart.Store(0)
	p.loopEnd.Store(0)
	p.posPub.Store(0)
	p.clip.Store(c)
}

// Clip devolve o clip carregado (nil se nenhum).
func (p *FilePlayer) Clip() *Clip { return p.clip.Load() }

// Play começa ou continua a reprodução.
func (p *FilePlayer) Play() { p.playing.Store(true) }

// Pause pausa (com rampa curta).
func (p *FilePlayer) Pause() { p.playing.Store(false) }

// Stop pausa e volta ao início do trecho.
func (p *FilePlayer) Stop() {
	p.playing.Store(false)
	p.seekReq.Store(p.loopStart.Load())
}

// Playing informa se está tocando.
func (p *FilePlayer) Playing() bool { return p.playing.Load() }

// SetLoop liga ou desliga o loop.
func (p *FilePlayer) SetLoop(on bool) { p.loop.Store(on) }

// Loop informa se o loop está ligado.
func (p *FilePlayer) Loop() bool { return p.loop.Load() }

// SetLoopRegion define o trecho do loop em segundos (fim <= 0 = até o fim do arquivo).
func (p *FilePlayer) SetLoopRegion(startS, endS float64) error {
	c := p.clip.Load()
	if c == nil {
		return fmt.Errorf("nenhum arquivo carregado")
	}
	n := int64(len(c.Samples))
	s := int64(math.Round(startS * p.fs))
	e := int64(math.Round(endS * p.fs))
	if endS <= 0 || e > n {
		e = n
	}
	if s < 0 || s >= e || e-s < int64(4*p.fade) {
		return fmt.Errorf("trecho inválido: %.2f s a %.2f s (arquivo tem %.2f s)", startS, endS, c.Duration())
	}
	p.loopStart.Store(s)
	p.loopEnd.Store(e)
	return nil
}

// LoopRegion devolve o trecho do loop em segundos.
func (p *FilePlayer) LoopRegion() (startS, endS float64) {
	c := p.clip.Load()
	if c == nil {
		return 0, 0
	}
	e := p.loopEnd.Load()
	if e <= 0 {
		e = int64(len(c.Samples))
	}
	return float64(p.loopStart.Load()) / p.fs, float64(e) / p.fs
}

// Seek pula para a posição em segundos.
func (p *FilePlayer) Seek(seconds float64) {
	p.seekReq.Store(max(0, int64(math.Round(seconds*p.fs))))
}

// Position devolve a posição atual em segundos.
func (p *FilePlayer) Position() float64 { return float64(p.posPub.Load()) / p.fs }

// region devolve o trecho ativo [start, end) em amostras.
func (p *FilePlayer) region(n int) (start, end int, loop bool) {
	if !p.loop.Load() {
		return 0, n, false
	}
	start, end = int(p.loopStart.Load()), int(p.loopEnd.Load())
	if end <= 0 || end > n {
		end = n
	}
	if start < 0 || start >= end {
		start = 0
	}
	return start, end, true
}

// Read implementa Source (thread de áudio).
func (p *FilePlayer) Read(buf []float64) {
	if c := p.clip.Load(); c != p.cur {
		p.cur, p.pos, p.g = c, 0, 0
	}
	if s := p.seekReq.Swap(-1); s >= 0 && p.cur != nil {
		p.pos = min(int(s), len(p.cur.Samples))
		p.g = 0 // entra com rampa, sem estalo
	}
	if p.cur == nil {
		clear(buf)
		return
	}
	x := p.cur.Samples
	start, end, loop := p.region(len(x))
	target := 0.0
	if p.playing.Load() {
		target = 1
	}

	for i := range buf {
		if p.g == 0 && target == 0 {
			clear(buf[i:])
			break
		}
		if p.pos >= end || p.pos < start && loop {
			if !loop {
				// fim do arquivo: para e volta ao início
				p.playing.Store(false)
				target, p.g, p.pos = 0, 0, 0
				clear(buf[i:])
				break
			}
			p.pos = start
		}
		if p.g < target {
			p.g = math.Min(target, p.g+p.step)
		} else if p.g > target {
			p.g = math.Max(target, p.g-p.step)
		}
		edge := 1.0
		if loop && p.fade > 0 {
			// rampa nas emendas do loop: o salto do fim para o início não estala
			if d := min(p.pos-start, end-1-p.pos); d < p.fade {
				edge = float64(d) / float64(p.fade)
			}
		}
		buf[i] = x[p.pos] * p.g * edge
		p.pos++
	}
	p.posPub.Store(int64(p.pos))
}

// Silence é uma fonte que só produz silêncio.
type Silence struct{}

// Read implementa Source.
func (Silence) Read(buf []float64) { clear(buf) }
