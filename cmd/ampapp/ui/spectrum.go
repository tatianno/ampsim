package ui

import (
	"fmt"
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/engine"
)

// AnalyzerID é a sobreposição do analisador de espectro grande.
const AnalyzerID = "__analisador__"

// Escala do analisador: dBFS de cada faixa de 1/6 de oitava (uma senoide de fundo de escala
// lê 0 dB). O eixo de frequência é o mesmo do gráfico do EQ (eqX, 20 Hz a 20 kHz).
const (
	specTopDB    = 0.0
	specBottomDB = -96.0
	specOct      = 1.0 / 6
	specPoints   = 240
	// faixa usada para alinhar a entrada com a saída (onde está a energia da guitarra)
	specAlignLo, specAlignHi = 100.0, 5000.0
)

// Cores das curvas.
var (
	specInColor  = rgb(0xaab2bd) // entrada: cinza
	specEQColor  = rgb(0x6fd3a8) // curva do EQ: verde
	specBg       = rgb(0x15181c)
	specGridLine = withAlpha(rgb(0xffffff), 22)
)

// SpectrumView mostra o espectro da entrada e da saída, a curva do EQ (se ele estiver na
// cadeia e ligado) e os cortes do gabinete. Tem uma versão compacta (na linha da ENTRADA;
// clicar abre a grande) e a grande (sobreposição, com os controles).
type SpectrumView struct {
	an    *audio.Analyzer
	chain *engine.Chain
	open  func(id string)

	freqs, inDB, outDB, eqDB []float64
	ready                    bool
	alignDB                  float64 // quanto a entrada foi deslocada no modo alinhado

	enabled, showIn, showOut, showEQ, align bool

	mini                           widget.Clickable
	inBtn, outBtn, eqBtn, alignBtn widget.Clickable
	onBtn                          widget.Clickable
	hoverTag                       int
	hoverX                         float32 // posição do ponteiro no gráfico grande (−1 = fora)
	plotW                          float32
}

// NewSpectrumView cria o analisador (ligado, com entrada, saída e EQ visíveis e a entrada
// alinhada pela média).
func NewSpectrumView(an *audio.Analyzer, chain *engine.Chain, open func(string)) *SpectrumView {
	v := &SpectrumView{an: an, chain: chain, open: open, enabled: true, showIn: true, showOut: true, showEQ: true,
		align: true, hoverX: -1,
		freqs: make([]float64, specPoints), inDB: make([]float64, specPoints), outDB: make([]float64, specPoints),
		eqDB: make([]float64, specPoints)}
	for k := range v.freqs {
		v.freqs[k] = eqPlotFMin * math.Pow(eqPlotFMax/eqPlotFMin, float64(k)/float64(specPoints-1))
	}
	return v
}

// Enabled informa se o analisador está ligado.
func (v *SpectrumView) Enabled() bool { return v.enabled }

// SetEnabled liga ou desliga o analisador (desligado, o áudio para de copiar o sinal).
func (v *SpectrumView) SetEnabled(on bool) {
	v.enabled = on
	if v.an != nil && !on {
		v.an.Stop()
	}
}

// refresh liga o analisador se preciso, esvazia a fila e recalcula as curvas.
func (v *SpectrumView) refresh() {
	if v.an == nil || !v.enabled {
		v.ready = false
		return
	}
	if !v.an.Running() {
		v.an.Start()
	}
	if !v.an.Update() {
		v.ready = false
		return
	}
	v.ready = true
	v.an.Bands(audio.SpecInput, v.freqs, specOct, v.inDB)
	v.an.Bands(audio.SpecOutput, v.freqs, specOct, v.outDB)
	v.alignDB = 0
	if v.align {
		pi, po := specMeanPower(v.freqs, v.inDB), specMeanPower(v.freqs, v.outDB)
		if pi > 1e-11 && po > 1e-11 { // entrada em silêncio: não desloca
			v.alignDB = 10 * math.Log10(po/pi)
			for k := range v.inDB {
				v.inDB[k] += v.alignDB
			}
		}
	}
}

// specMeanPower é a potência média (linear) das faixas entre specAlignLo e specAlignHi.
func specMeanPower(freqs, db []float64) float64 {
	s, n := 0.0, 0
	for k, f := range freqs {
		if f >= specAlignLo && f <= specAlignHi {
			s += math.Pow(10, db[k]/10)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return s / float64(n)
}

// eqActive diz se há EQ ligado na cadeia (e calcula a curva dele em eqDB).
func (v *SpectrumView) eqActive() bool {
	if v.chain == nil || !v.chain.Enabled("eq") {
		return false
	}
	in := false
	for _, id := range v.chain.Order() {
		if id == "eq" {
			in = true
		}
	}
	m, ok := v.chain.Module("eq").(*engine.EQ)
	if !in || !ok {
		return false
	}
	m.ResponseDB(v.freqs, v.eqDB)
	return true
}

func specY(db float64, h float32) float32 {
	t := (db - specTopDB) / (specBottomDB - specTopDB)
	return float32(math.Max(0, math.Min(1, t))) * h
}

// curvePath monta o caminho de uma curva (y por ponto) no gráfico de largura w.
func (v *SpectrumView) curvePath(gtx layout.Context, w float32, y func(k int) float32, closeTo float32, fill bool) clip.PathSpec {
	var p clip.Path
	p.Begin(gtx.Ops)
	for k, f := range v.freqs {
		pt := f32.Pt(eqX(f, w), y(k))
		if k == 0 {
			if fill {
				p.MoveTo(f32.Pt(pt.X, closeTo))
				p.LineTo(pt)
			} else {
				p.MoveTo(pt)
			}
			continue
		}
		p.LineTo(pt)
	}
	if fill {
		p.LineTo(f32.Pt(w, closeTo))
		p.Close()
	}
	return p.End()
}

// drawPlot desenha o gráfico (sem moldura) em sz; detail liga rótulos e escalas.
func (v *SpectrumView) drawPlot(gtx layout.Context, th *Theme, sz image.Point, detail bool) {
	defer clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(6)).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, specBg)
	w, h := float32(sz.X), float32(sz.Y)
	if v.showEQ {
		drawCabCuts(gtx, th, v.chain, sz, detail && v.hoverX < 0) // os valores somem sob a leitura do ponteiro
	}
	freqLines := []float64{100, 1000, 10000}
	if detail {
		freqLines = []float64{50, 100, 200, 500, 1000, 2000, 5000, 10000}
		for db := -12.0; db > specBottomDB; db -= 12 {
			strokeLine(gtx.Ops, f32.Pt(0, specY(db, h)), f32.Pt(w, specY(db, h)), 1, specGridLine)
		}
	}
	for _, f := range freqLines {
		strokeLine(gtx.Ops, f32.Pt(eqX(f, w), 0), f32.Pt(eqX(f, w), h), 1, specGridLine)
	}
	small := withAlpha(rgb(0xffffff), 110)
	if detail {
		for _, l := range []struct {
			f float64
			s string
		}{{50, "50"}, {100, "100"}, {200, "200"}, {500, "500"}, {1000, "1k"}, {2000, "2k"}, {5000, "5k"}, {10000, "10k"}} {
			func() {
				defer offset(gtx.Ops, int(eqX(l.f, w))+gtx.Dp(3), sz.Y-gtx.Dp(15)).Pop()
				drawText(gtx, th, l.s, textOpts{size: 10, color: small})
			}()
		}
		for db := -12.0; db > specBottomDB; db -= 24 {
			func() {
				defer offset(gtx.Ops, gtx.Dp(4), int(specY(db, h))-gtx.Dp(13)).Pop()
				drawText(gtx, th, fmt.Sprintf("%.0f dB", db), textOpts{size: 10, color: small})
			}()
		}
	}

	if v.ready {
		if v.showOut {
			yo := func(k int) float32 { return specY(v.outDB[k], h) }
			paint.FillShape(gtx.Ops, withAlpha(th.Gold, 70), clip.Outline{Path: v.curvePath(gtx, w, yo, h, true)}.Op())
			paint.FillShape(gtx.Ops, th.Gold, clip.Stroke{Path: v.curvePath(gtx, w, yo, 0, false), Width: float32(gtx.Dp(1.5))}.Op())
		}
		if v.showIn {
			yi := func(k int) float32 { return specY(v.inDB[k], h) }
			paint.FillShape(gtx.Ops, specInColor, clip.Stroke{Path: v.curvePath(gtx, w, yi, 0, false), Width: float32(gtx.Dp(1.2))}.Op())
		}
	}
	if v.showEQ && v.eqActive() {
		ye := func(k int) float32 { return eqY(v.eqDB[k], h) }
		if detail { // linha do 0 dB do EQ e escala à direita
			strokeLine(gtx.Ops, f32.Pt(0, eqY(0, h)), f32.Pt(w, eqY(0, h)), 1, withAlpha(specEQColor, 60))
			for _, db := range []float64{12, 0, -12} {
				func() {
					defer offset(gtx.Ops, sz.X-gtx.Dp(60), int(eqY(db, h))-gtx.Dp(13)).Pop()
					drawText(gtx, th, fmt.Sprintf("EQ %+.0f", db), textOpts{size: 10, color: withAlpha(specEQColor, 200),
						width: gtx.Dp(56), align: text.End})
				}()
			}
		}
		paint.FillShape(gtx.Ops, specEQColor, clip.Stroke{Path: v.curvePath(gtx, w, ye, 0, false), Width: float32(gtx.Dp(1.5))}.Op())
	}
	if !v.enabled {
		defer offset(gtx.Ops, 0, sz.Y/2-gtx.Sp(8)).Pop()
		drawText(gtx, th, "analisador desligado", textOpts{size: 11, color: th.Dim, width: sz.X, align: text.Middle})
	}
}

// MiniLayout desenha a versão compacta (ocupa a largura disponível); clicar abre a grande.
func (v *SpectrumView) MiniLayout(gtx layout.Context, th *Theme) layout.Dimensions {
	v.refresh()
	if v.mini.Clicked(gtx) && v.open != nil {
		v.open(AnalyzerID)
	}
	avail := gtx.Constraints.Max.X
	sz := image.Pt(min(avail, gtx.Dp(MiniSpectrumMaxDp)), gtx.Dp(58))
	if sz.X < gtx.Dp(MiniSpectrumMinDp) {
		return layout.Dimensions{Size: image.Pt(avail, 0)}
	}
	// alinhado à direita (embaixo do Menu); o espaço livre fica à esquerda
	defer offset(gtx.Ops, avail-sz.X, 0).Pop()
	return v.mini.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		v.drawPlot(gtx, th, sz, false)
		func() {
			defer offset(gtx.Ops, gtx.Dp(5), gtx.Dp(2)).Pop()
			drawText(gtx, th, "ESPECTRO", textOpts{size: 9, color: withAlpha(th.Dim, 220), bold: true})
		}()
		if v.mini.Hovered() {
			strokeRRect(gtx, image.Rectangle{Max: sz}, gtx.Dp(6), withAlpha(th.Gold, 140))
		}
		return layout.Dimensions{Size: sz}
	})
}

// Largura do analisador compacto: até MiniSpectrumMaxDp (proporção ~6:1 com a altura de 58 dp);
// com menos de MiniSpectrumMinDp disponíveis ele some (não vira uma tira ilegível).
const (
	MiniSpectrumMaxDp = 340
	MiniSpectrumMinDp = 160
)

// AnalyzerWidthDp é a largura máxima do analisador grande.
const AnalyzerWidthDp = 900

// Layout desenha o analisador grande (sobreposição).
func (v *SpectrumView) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	if v.inBtn.Clicked(gtx) {
		v.showIn = !v.showIn
	}
	if v.outBtn.Clicked(gtx) {
		v.showOut = !v.showOut
	}
	if v.eqBtn.Clicked(gtx) {
		v.showEQ = !v.showEQ
	}
	if v.alignBtn.Clicked(gtx) {
		v.align = !v.align
	}
	if v.onBtn.Clicked(gtx) {
		v.SetEnabled(!v.enabled)
	}
	v.updateHover(gtx)
	w := min(gtx.Dp(AnalyzerWidthDp), gtx.Constraints.Max.X-gtx.Dp(40))
	gtx.Constraints = layout.Exact(image.Pt(w, gtx.Constraints.Max.Y))
	gtx.Constraints.Min.Y = 0
	return panel(gtx, th.Panel, 12, 16, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "ANALISADOR DE ESPECTRO", textOpts{size: 13, color: th.Dim, bold: true})
			}),
			layout.Rigid(spacer(0, 10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				alignLabel := "Entrada alinhada"
				if v.align && v.ready && v.alignDB != 0 {
					alignLabel = fmt.Sprintf("Entrada alinhada (%+.0f dB)", v.alignDB)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return toggle(gtx, th, &v.onBtn, v.enabled, "Ligado") }),
					layout.Rigid(spacer(16, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return toggle(gtx, th, &v.outBtn, v.showOut, "Saída") }),
					layout.Rigid(spacer(8, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return toggle(gtx, th, &v.inBtn, v.showIn, "Entrada") }),
					layout.Rigid(spacer(8, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return toggle(gtx, th, &v.eqBtn, v.showEQ, "EQ e gabinete")
					}),
					layout.Rigid(spacer(16, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return toggle(gtx, th, &v.alignBtn, v.align, alignLabel) }),
				)
			}),
			layout.Rigid(spacer(0, 10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(300))
				v.plotW = float32(sz.X)
				v.drawPlot(gtx, th, sz, true)
				v.drawHover(gtx, th, sz)
				area := clip.Rect{Max: sz}.Push(gtx.Ops)
				event.Op(gtx.Ops, &v.hoverTag)
				area.Pop()
				return layout.Dimensions{Size: sz}
			}),
			layout.Rigid(spacer(0, 8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return drawText(gtx, th, "Dourado: saída  ·  cinza: entrada (antes da cadeia)  ·  verde: curva do EQ (escala à direita)  ·  azul: cortes do gabinete",
					textOpts{size: 11, color: th.Dim, width: gtx.Constraints.Max.X})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !v.align {
					return layout.Dimensions{}
				}
				return drawText(gtx, th, "Entrada alinhada: a curva cinza tem a mesma média da saída entre 100 Hz e 5 kHz (compara o formato, não o volume)",
					textOpts{size: 11, color: th.Dim, width: gtx.Constraints.Max.X})
			}),
		)
	})
}

func (v *SpectrumView) updateHover(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &v.hoverTag, Kinds: pointer.Move | pointer.Leave | pointer.Enter})
		if !ok {
			return
		}
		if e, ok := ev.(pointer.Event); ok {
			if e.Kind == pointer.Leave {
				v.hoverX = -1
			} else {
				v.hoverX = e.Position.X
			}
		}
	}
}

// drawHover mostra a frequência e os níveis sob o ponteiro.
func (v *SpectrumView) drawHover(gtx layout.Context, th *Theme, sz image.Point) {
	if v.hoverX < 0 || v.hoverX > float32(sz.X) {
		return
	}
	strokeLine(gtx.Ops, f32.Pt(v.hoverX, 0), f32.Pt(v.hoverX, float32(sz.Y)), 1, withAlpha(rgb(0xffffff), 90))
	f := eqFreqAt(v.hoverX, float32(sz.X))
	k := int(math.Round(math.Log(f/eqPlotFMin) / math.Log(eqPlotFMax/eqPlotFMin) * float64(specPoints-1)))
	k = max(0, min(specPoints-1, k))
	s := fmtFreq(f)
	if v.ready {
		if v.showOut {
			s += fmt.Sprintf("  ·  saída %.0f dB", v.outDB[k])
		}
		if v.showIn {
			s += fmt.Sprintf("  ·  entrada %.0f dB", v.inDB[k])
		}
	}
	if v.showEQ && v.eqActive() {
		s += fmt.Sprintf("  ·  EQ %+.1f dB", v.eqDB[k])
	}
	defer offset(gtx.Ops, gtx.Dp(60), gtx.Dp(6)).Pop()
	drawText(gtx, th, s, textOpts{size: 11, color: rgb(0xffffff), bold: true})
}

// fmtFreq formata uma frequência (Hz ou kHz).
func fmtFreq(f float64) string {
	if f >= 1000 {
		return fmt.Sprintf("%.2f kHz", f/1000)
	}
	return fmt.Sprintf("%.0f Hz", f)
}
