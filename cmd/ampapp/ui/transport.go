package ui

import (
	"fmt"
	"image"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/widget"

	"ampsim/cmd/ampapp/source"
)

// Transport controla o arquivo: abrir, play/pause, stop, loop e a barra de posição.
//
// Na barra: clicar/arrastar = ir para a posição; Shift + arrastar = escolher o trecho do
// loop; duplo clique = loop no arquivo inteiro.
type Transport struct {
	player *source.FilePlayer
	OnOpen func()

	open, play, stop, loop widget.Clickable

	// barra
	barW      int
	selecting bool
	selA      float64 // frações 0..1
	selB      float64
	lastPress time.Duration
}

// NewTransport cria o transporte para o player.
func NewTransport(p *source.FilePlayer) *Transport { return &Transport{player: p} }

func fmtTime(s float64) string {
	m := int(s) / 60
	return fmt.Sprintf("%d:%04.1f", m, s-float64(m*60))
}

// Layout desenha o transporte.
func (t *Transport) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	p := t.player
	if t.open.Clicked(gtx) && t.OnOpen != nil {
		t.OnOpen()
	}
	if t.play.Clicked(gtx) {
		t.TogglePlay()
	}
	if t.stop.Clicked(gtx) {
		p.Stop()
	}
	if t.loop.Clicked(gtx) {
		p.SetLoop(!p.Loop())
	}
	clip := p.Clip()
	name := "nenhum arquivo"
	dur := 0.0
	if clip != nil {
		name, dur = clip.Name, clip.Duration()
	}
	playLabel := "► Play"
	if p.Playing() {
		playLabel = "|| Pause"
	}
	loopBg := th.PanelHi
	if p.Loop() {
		loopBg = darken(th.Gold, 0.35)
	}

	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &t.open, "Abrir WAV…", 0) }),
		layout.Rigid(spacer(8, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return coloredButton(gtx, th, &t.play, playLabel, gtx.Dp(92), darken(th.Good, 0.45), th.Text)
		}),
		layout.Rigid(spacer(6, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return smallButton(gtx, th, &t.stop, "■ Stop", 0) }),
		layout.Rigid(spacer(6, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return coloredButton(gtx, th, &t.loop, "Loop", 0, loopBg, th.Text)
		}),
		layout.Rigid(spacer(12, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, name, textOpts{size: 13, color: th.Text, width: gtx.Dp(170)})
		}),
		layout.Rigid(spacer(8, 0)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return t.bar(gtx, th, dur) }),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, fmtTime(p.Position())+" / "+fmtTime(dur), textOpts{size: 12, color: th.Dim, width: gtx.Dp(104), align: text.End})
		}),
	)
}

// TogglePlay alterna entre tocar e pausar.
func (t *Transport) TogglePlay() {
	if t.player.Playing() {
		t.player.Pause()
	} else {
		t.player.Play()
	}
}

func (t *Transport) frac(x float32) float64 {
	if t.barW <= 0 {
		return 0
	}
	return clamp01(float64(x) / float64(t.barW))
}

func (t *Transport) updateBar(gtx layout.Context, dur float64) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: t, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok || dur <= 0 {
			continue
		}
		f := t.frac(e.Position.X)
		switch e.Kind {
		case pointer.Press:
			if t.lastPress != 0 && e.Time-t.lastPress < doubleClick {
				t.player.SetLoopRegion(0, 0) // loop no arquivo inteiro
				t.lastPress = 0
				continue
			}
			t.lastPress = e.Time
			if e.Modifiers.Contain(key.ModShift) {
				t.selecting, t.selA, t.selB = true, f, f
			} else {
				t.player.Seek(f * dur)
			}
		case pointer.Drag:
			if t.selecting {
				t.selB = f
			} else {
				t.player.Seek(f * dur)
			}
		case pointer.Release:
			if t.selecting {
				a, b := math.Min(t.selA, t.selB), math.Max(t.selA, t.selB)
				if (b-a)*dur > 0.05 {
					t.player.SetLoopRegion(a*dur, b*dur)
					t.player.SetLoop(true)
					t.player.Seek(a * dur)
				}
			}
			t.selecting = false
		case pointer.Cancel:
			t.selecting = false
		}
	}
}

func (t *Transport) bar(gtx layout.Context, th *Theme, dur float64) layout.Dimensions {
	t.updateBar(gtx, dur)
	w := gtx.Constraints.Max.X
	h := gtx.Dp(26)
	t.barW = w
	area := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	event.Op(gtx.Ops, t)
	pointer.CursorPointer.Add(gtx.Ops)
	area.Pop()

	trackY0, trackY1 := h/2-gtx.Dp(4), h/2+gtx.Dp(4)
	fillRRect(gtx.Ops, image.Rect(0, trackY0, w, trackY1), gtx.Dp(4), th.Line)
	if dur > 0 {
		px := func(sec float64) int { return int(sec / dur * float64(w)) }
		if t.player.Loop() {
			ls, le := t.player.LoopRegion()
			fillRRect(gtx.Ops, image.Rect(px(ls), trackY0-gtx.Dp(3), px(le), trackY1+gtx.Dp(3)), gtx.Dp(3), withAlpha(th.Gold, 70))
		}
		pos := px(t.player.Position())
		fillRRect(gtx.Ops, image.Rect(0, trackY0, pos, trackY1), gtx.Dp(4), darken(th.Good, 0.2))
		strokeLine(gtx.Ops, f32.Pt(float32(pos), 0), f32.Pt(float32(pos), float32(h)), float32(gtx.Dp(2)), th.Text)
		if t.selecting {
			a, b := math.Min(t.selA, t.selB), math.Max(t.selA, t.selB)
			fillRRect(gtx.Ops, image.Rect(int(a*float64(w)), 0, int(b*float64(w)), h), gtx.Dp(3), withAlpha(th.Gold, 110))
		}
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}
