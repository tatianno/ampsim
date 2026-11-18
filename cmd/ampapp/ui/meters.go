package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
)

// Faixa dos medidores, em dBFS.
const (
	meterFloorDB = -60.0
	meterYellow  = -12.0
	meterRed     = -3.0
)

// Meters mostra os níveis de entrada e saída (com queda suave), clip, gate e CPU.
type Meters struct {
	in, out   float32
	inRaw     float32 // pico da placa (antes do trim), com queda suave
	load      float32
	clips     uint64
	clipUntil time.Time
	inClips   uint64
	inUntil   time.Time
	overruns  uint64
	gate      bool
	bypass    bool
	last      time.Time
}

// Update incorpora uma leitura do monitor.
func (m *Meters) Update(l audio.Levels, now time.Time) {
	decay := float32(1)
	if !m.last.IsZero() {
		// cai pela metade a cada 120 ms
		decay = float32(math.Pow(0.5, now.Sub(m.last).Seconds()/0.12))
	}
	m.last = now
	m.in = max(l.InPeak, m.in*decay)
	m.out = max(l.OutPeak, m.out*decay)
	m.inRaw = max(l.InRaw, m.inRaw*decay)
	m.load = max(l.MaxLoad, m.load*decay)
	if l.Clips > m.clips {
		m.clipUntil = now.Add(time.Second)
	}
	m.clips = l.Clips
	if l.InClips > m.inClips {
		m.inUntil = now.Add(time.Second)
	}
	m.inClips = l.InClips
	m.overruns = l.Overruns
	m.gate = l.GateOpen
	m.bypass = l.Bypassed
}

// InputClipping informa se o LED de clip da placa (conversor de entrada) está aceso.
func (m *Meters) InputClipping(now time.Time) bool { return now.Before(m.inUntil) }

// Clipping informa se o LED de clip está aceso.
func (m *Meters) Clipping(now time.Time) bool { return now.Before(m.clipUntil) }

func toDB(v float32) float64 { return 20 * math.Log10(float64(v)+1e-9) }

// Layout desenha os medidores numa linha.
func (m *Meters) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(m.bar(th, "IN", m.in)),
		layout.Rigid(spacer(14, 0)),
		layout.Rigid(m.bar(th, "OUT", m.out)),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(ledLabel(th, "CLIP", m.Clipping(gtx.Now), th.LedOn, th.LedOff)),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(ledLabel(th, "GATE", m.gate, th.Good, darken(th.Good, 0.7))),
		layout.Rigid(spacer(14, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, fmt.Sprintf("CPU %2.0f%%", 100*m.load), textOpts{size: 11, color: th.Dim, width: gtx.Dp(58)})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if m.overruns == 0 {
				return layout.Dimensions{}
			}
			// callbacks que passaram do tempo: prováveis estalos (aumente o buffer)
			return drawText(gtx, th, fmt.Sprintf("atrasos %d", m.overruns), textOpts{size: 11, color: th.Warn})
		}),
	)
}

func ledLabel(th *Theme, label string, on bool, onC, offC color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				sz := image.Pt(gtx.Dp(16), gtx.Dp(16))
				led(gtx.Ops, f32.Pt(float32(sz.X)/2, float32(sz.Y)/2), float32(gtx.Dp(5)), on, onC, offC)
				return layout.Dimensions{Size: sz}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, label, textOpts{size: 11, color: th.Dim, bold: true})
			}),
		)
	}
}

func (m *Meters) bar(th *Theme, label string, v float32) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, label, textOpts{size: 11, color: th.Dim, bold: true, width: gtx.Dp(30)})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				w, h := gtx.Dp(160), gtx.Dp(10)
				fillRRect(gtx.Ops, image.Rect(0, 0, w, h), gtx.Dp(3), th.Line)
				x := func(db float64) int {
					return int(clamp01((db-meterFloorDB)/-meterFloorDB) * float64(w))
				}
				lv := x(toDB(v))
				if lv > 0 {
					fillRRect(gtx.Ops, image.Rect(0, 0, min(lv, x(meterYellow)), h), gtx.Dp(3), th.Good)
				}
				if lv > x(meterYellow) {
					fillRRect(gtx.Ops, image.Rect(x(meterYellow), 0, min(lv, x(meterRed)), h), 0, th.Warn)
				}
				if lv > x(meterRed) {
					fillRRect(gtx.Ops, image.Rect(x(meterRed), 0, lv, h), gtx.Dp(3), th.Clip)
				}
				return layout.Dimensions{Size: image.Pt(w, h)}
			}),
			layout.Rigid(spacer(6, 0)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				s := "−∞"
				if db := toDB(v); db > -90 {
					s = fmt.Sprintf("%.0f", db)
				}
				return drawText(gtx, th, s, textOpts{size: 11, color: th.Dim, width: gtx.Dp(28)})
			}),
		)
	}
}
