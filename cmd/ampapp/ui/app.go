package ui

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/widget"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
	"github.com/tatianno/ampsim/wav"
)

// App é a tela principal.
type App struct {
	Session  *audio.Session
	Chain    *engine.Chain
	Player   *source.FilePlayer
	Renderer *audio.Renderer
	Monitor  *audio.Monitor

	// Invalidate pede um novo quadro (a janela define; usado por callbacks de outras goroutines).
	Invalidate func()
	// Pick abre o seletor de arquivo; por padrão PickWAV (os testes trocam).
	Pick func(title string) (string, error)
	// PickNAM abre o seletor de modelos .nam (nil = usa Pick).
	PickNAM func(title string) (string, error)
	// PickSave abre o diálogo de salvar; por padrão PickSaveWAV (os testes trocam).
	PickSave func(title, suggested string) (string, error)
	// RecordDir é a pasta das gravações (padrão: Música/ampsim).
	RecordDir string
	// PickZip e PickSaveZip abrem os diálogos do backup dos presets (os testes trocam).
	PickZip     func(title string) (string, error)
	PickSaveZip func(title, suggested string) (string, error)

	th         *Theme
	transport  *Transport
	head       *Head
	pedalboard *Pedalboard
	addPedal   *AddPedal
	about      *About
	menu       *Menu
	spectrum   *SpectrumView
	namList    *AssetList
	driveNAM   *AssetList // capturas de pedal do drive
	irList     *AssetList
	cab        *CabPanel
	board      *Board
	input      *InputPanel
	audioSet   *AudioPanel
	presetBar  *PresetBar
	tuner      *TunerPanel
	prevOpen   string // para saber quando o afinador abre/fecha
	meters     Meters
	pedals     map[string]*Pedal
	bypass     widget.Clickable
	stereoBtn  widget.Clickable
	settings   string // ajustes.json ("" = não salva)
	closeBtn   widget.Clickable
	recBtn     widget.Clickable
	diBtn      widget.Clickable
	recsBtn    widget.Clickable
	recs       *RecordingsPanel
	// recDry: o A/B SECO foi ligado pelo app para tocar uma gravação com efeitos (recPath);
	// desliga sozinho quando a fonte ou o arquivo mudam.
	recDry    bool
	recPath   string
	recDI     bool
	exporting bool
	exportPct float64

	open     string // pedal aberto na sobreposição ("" = nenhum)
	scrim    int    // tags de evento da sobreposição
	panelTag int

	status      string
	statusErr   bool
	statusUntil time.Time

	mu      sync.Mutex
	pending []func()
}

// NewApp monta a tela sobre a sessão de áudio.
func NewApp(s *audio.Session) *App {
	chain := s.Chain
	a := &App{
		Session: s, Chain: chain, Player: s.Player, Renderer: s.Renderer, Monitor: s.Monitor,
		Pick:      PickWAV,
		PickNAM:   PickNAM,
		PickSave:  PickSaveWAV,
		RecordDir: audio.DefaultRecordDir(),
		recDI:     true,
		th:        NewTheme(),
		pedals:    map[string]*Pedal{},
	}
	a.transport = NewTransport(s.Player)
	a.input = NewInputPanel(s)
	a.audioSet = NewAudioPanel(s, a.Post)
	a.input.OnAudio = func() {
		a.audioSet.Refresh()
		a.open = AudioID
	}
	a.tuner = NewTunerPanel(s)
	a.spectrum = NewSpectrumView(s.Analyzer, chain, func(id string) { a.open = id })
	a.input.Extra = func(gtx layout.Context) layout.Dimensions { return a.spectrum.MiniLayout(gtx, a.th) }
	a.menu = &Menu{}
	a.PickZip, a.PickSaveZip = PickZip, PickSaveZip
	dir, err := presets.Dir()
	if err != nil {
		dir = "" // sem pasta de configuração: os de fábrica funcionam, salvar avisa o erro
	}
	a.UsePresetDir(dir)
	a.head = NewHead(chain)
	a.about = NewAbout("", a.setStatus)
	a.head.OnPickNAM = func() { a.open = NAMListID }
	a.namList = NewNAMList(chain)
	a.namList.Load = func(ref string) {
		a.loadAsync(ref, func() error { return a.Chain.Pre().LoadNAM(ref) }, "modelo NAM")
	}
	a.namList.OnOpenFile = func() {
		pick := a.PickNAM
		if pick == nil {
			pick = a.Pick
		}
		a.pickWith(pick, "Abrir modelo NAM", func(path string) error { return a.Chain.Pre().LoadNAM(path) })
	}
	a.cab = NewCabPanel(chain)
	a.board = NewBoard(chain)
	a.addPedal = NewAddPedal(chain)
	a.board.OnAdd = func() { a.open = AddPedalID }
	a.addPedal.OnAdded = func(id string) { a.open = id } // abre o pedal recém-adicionado
	for _, m := range chain.AllModules() {               // inclusive os de fora da cadeia (podem ser adicionados)
		if !isAmpBlock(m.ID()) {
			p := NewPedal(chain, m)
			p.OnRemoved = func() { a.open = "" }
			a.pedals[m.ID()] = p
		}
	}
	if p := a.pedals["drive"]; p != nil {
		p.OnPickNAM = func() { a.open = DriveNAMListID }
	}
	a.driveNAM = NewDriveNAMList(chain)
	a.driveNAM.Load = func(ref string) {
		a.loadAsync(ref, func() error { return a.Chain.Drive().LoadNAM(ref) }, "captura do drive")
	}
	a.driveNAM.OnOpenFile = func() {
		pick := a.PickNAM
		if pick == nil {
			pick = a.Pick
		}
		a.pickWith(pick, "Abrir captura de pedal (.nam)", func(path string) error { return a.Chain.Drive().LoadNAM(path) })
	}
	a.board.OnOpen = func(id string) {
		if _, ok := a.pedals[id]; ok {
			a.open = id
		}
	}
	a.transport.OnExport = a.exportAsync
	a.recs = NewRecordingsPanel(a.RecordDir)
	a.recs.Load = a.loadRecording
	a.transport.OnOpen = func() {
		a.pickAsync("Abrir WAV para tocar", func(path string) error { return a.Player.Load(path) })
	}
	a.cab.OnPick = func() { a.open = IRListID }
	a.irList = NewIRList(chain)
	a.irList.Load = func(ref string) {
		a.loadAsync(ref, func() error { return a.Chain.Cab().LoadIRRef(ref) }, "IR")
	}
	a.irList.OnOpenFile = func() {
		a.pickAsync("Escolher IR do gabinete", func(path string) error { return a.Chain.Cab().LoadIRFile(path) })
	}
	return a
}

// UsePresetDir troca a pasta dos presets do usuário (os testes usam uma pasta temporária).
func (a *App) UsePresetDir(dir string) {
	a.presetBar = NewPresetBar(a.Chain, dir, a.setStatus, func(id string) { a.open = id })
	cfg := "" // pedaleira.json fica ao lado da pasta de presets (~/.config/ampsim/)
	if dir != "" {
		cfg = filepath.Join(filepath.Dir(dir), "pedaleira.json")
	}
	a.pedalboard = NewPedalboard(a.presetBar, cfg, a.setStatus, func(id string) { a.open = id })
	a.settings = presets.SettingsPath(dir)
	st, err := presets.LoadSettings(a.settings)
	if err != nil {
		a.setStatus("ajustes.json inválido: usando os padrões", true)
	}
	a.Chain.SetStereo(st.StereoOn())
}

// SetStereo liga ou desliga a saída estéreo e salva em ajustes.json.
func (a *App) SetStereo(on bool) error {
	a.Chain.SetStereo(on)
	st, _ := presets.LoadSettings(a.settings)
	st.Stereo = &on
	return st.Save(a.settings)
}

// Pedalboard devolve a pedaleira de presets.
func (a *App) Pedalboard() *Pedalboard { return a.pedalboard }

// Presets devolve a barra de presets.
func (a *App) Presets() *PresetBar { return a.presetBar }

// Open abre na sobreposição o pedal id, as configurações de áudio (AudioID), a lista de
// presets (PresetListID) ou o "salvar preset" (SaveAsID).
func (a *App) Open(id string) {
	switch id {
	case AudioID:
		a.input.OnAudio()
	case PresetListID, TunerID, AboutID, NAMListID, DriveNAMListID, IRListID, AddPedalID, AnalyzerID, ShortcutsID:
		a.open = id
	case RecordingsID:
		a.recs.dir = a.RecordDir
		a.recs.Show(nil)
		a.open = id
	case SaveAsID:
		a.presetBar.startSaveAs("")
	default:
		a.board.OnOpen(id)
	}
}

// Opened devolve o pedal aberto ("" se nenhum).
func (a *App) Opened() string { return a.open }

// Post agenda f para rodar na goroutine da GUI no próximo quadro (seguro de qualquer goroutine).
func (a *App) Post(f func()) {
	a.mu.Lock()
	a.pending = append(a.pending, f)
	a.mu.Unlock()
	if a.Invalidate != nil {
		a.Invalidate()
	}
}

func (a *App) runPending() {
	a.mu.Lock()
	fs := a.pending
	a.pending = nil
	a.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

// pickAsync abre o seletor numa goroutine e aplica o arquivo escolhido.
func (a *App) pickAsync(title string, apply func(path string) error) {
	a.pickWith(a.Pick, title, apply)
}

func (a *App) pickWith(pick func(string) (string, error), title string, apply func(path string) error) {
	go func() {
		path, err := pick(title)
		if err == nil && path != "" {
			err = apply(path) // carregar e preparar acontece fora da thread de áudio
		}
		a.Post(func() {
			switch {
			case err != nil:
				a.setStatus(err.Error(), true)
			case path != "":
				a.setStatus("carregado: "+filepath.Base(path), false)
			}
		})
	}()
}

// loadAsync carrega um item incluído (modelo NAM ou IR) fora da goroutine da GUI
// (montar e aquecer as redes NAM leva algumas dezenas de ms).
func (a *App) loadAsync(ref string, load func() error, what string) {
	go func() {
		err := load()
		a.Post(func() {
			if err != nil {
				a.setStatus(err.Error(), true)
				return
			}
			a.setStatus(what+": "+strings.TrimPrefix(ref, capturas.Prefix), false)
		})
	}()
}

// SetStatus mostra uma mensagem no rodapé (ex.: aviso na abertura).
func (a *App) SetStatus(s string, isErr bool) { a.setStatus(s, isErr) }

func (a *App) setStatus(s string, isErr bool) {
	a.status, a.statusErr = s, isErr
	a.statusUntil = time.Now().Add(6 * time.Second)
}

// FrameInterval é o intervalo entre quadros enquanto há medidores animando (~30 fps).
const FrameInterval = 33 * time.Millisecond

// Layout desenha a tela inteira.
func (a *App) Layout(gtx layout.Context) layout.Dimensions {
	a.runPending()
	if a.Monitor != nil {
		a.meters.Update(a.Monitor.Take(), gtx.Now)
	}
	a.keys(gtx)
	if a.bypass.Clicked(gtx) && a.Renderer != nil {
		a.Renderer.SetBypass(!a.Renderer.Bypass())
		a.recDry = false // o usuário assumiu o A/B
	}
	if a.stereoBtn.Clicked(gtx) {
		on := !a.Chain.Stereo()
		msg := "saída estéreo: modulações e ambiências abrem em L e R (knob Largura)"
		if !on {
			msg = "saída mono: L e R iguais, como antes"
		}
		if err := a.SetStereo(on); err != nil {
			a.setStatus("ajustes: "+err.Error(), true)
		} else {
			a.setStatus(msg, false)
		}
	}
	if a.recDry {
		if c := a.Player.Clip(); a.Session.Source() != audio.SourceFile || c == nil || c.Path != a.recPath {
			a.Renderer.SetBypass(false)
			a.recDry = false
		}
	}
	if a.recBtn.Clicked(gtx) {
		a.toggleRecord()
	}
	if a.diBtn.Clicked(gtx) && !a.Session.Recorder.Recording() {
		a.recDI = !a.recDI
	}
	if a.recsBtn.Clicked(gtx) {
		a.Open(RecordingsID)
	}
	a.menuActions(gtx)
	th := a.th
	paint.Fill(gtx.Ops, th.Bg)

	tall := gtx.Constraints.Max.Y
	layout.UniformInset(14).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(a.topBar),
			layout.Rigid(spacer(0, 10)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th.Panel, 10, 8, func(gtx layout.Context) layout.Dimensions {
					return a.input.Layout(gtx, th, &a.meters)
				})
			}),
			// controles do arquivo: só com a fonte em Arquivo, logo abaixo da ENTRADA
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if a.Session.Source() != audio.SourceFile {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return panel(gtx, th.Panel, 10, 10, func(gtx layout.Context) layout.Dimensions {
						return a.transport.Layout(gtx, th)
					})
				})
			}),
			layout.Rigid(spacer(0, 12)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return a.head.Layout(gtx, th) }),
					layout.Rigid(spacer(12, 0)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.cab.Layout(gtx, th) }),
				)
			}),
			layout.Rigid(spacer(0, 12)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th.Panel, 10, 8, func(gtx layout.Context) layout.Dimensions {
					return a.presetBar.Layout(gtx, th)
				})
			}),
			layout.Rigid(spacer(0, 12)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th.Panel, 10, 12, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return drawText(gtx, th, "CADEIA DE SINAL", textOpts{size: 12, color: th.Dim, bold: true})
								}),
								layout.Rigid(spacer(8, 0)),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return toggle(gtx, th, &a.stereoBtn, a.Chain.Stereo(), "Estéreo")
								}),
								layout.Rigid(spacer(14, 0)),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return drawText(gtx, th, "clique: abrir o pedal  ·  LED: liga/desliga  ·  arraste: muda a ordem (entre o amp e o POWER = loop)  ·  +: adicionar",
										textOpts{size: 11, color: withAlpha(th.Dim, 200)})
								}),
							)
						}),
						layout.Rigid(spacer(0, 2)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.board.Layout(gtx, th) }),
					)
				})
			}),
			// pedaleira de presets: só se couber (em janelas baixas o rodapé tem prioridade;
			// as teclas 1 a 6 continuam valendo)
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if gtx.Metric.PxToDp(tall) < PedalboardMinHeightDp {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return panel(gtx, th.Panel, 10, 8, func(gtx layout.Context) layout.Dimensions {
						return a.pedalboard.Layout(gtx, th)
					})
				})
			}),
			layout.Flexed(1, vfill), // empurra o rodapé para baixo
			layout.Rigid(a.footer),
		)
	})

	if a.open != "" {
		a.overlay(gtx)
	}
	if a.menu.open {
		a.menu.Dropdown(gtx, th, gtx.Dp(14), gtx.Dp(14+30))
	}
	a.tunerTransitions()
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(FrameInterval)})
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

// tunerTransitions liga o afinador quando a sobreposição dele abre e desliga quando fecha
// (por Esc, clique fora, × ou trocando de painel).
func (a *App) tunerTransitions() {
	if a.open == a.prevOpen {
		return
	}
	if a.prevOpen == TunerID {
		a.tuner.Close()
	}
	if a.open == TunerID {
		a.tuner.Open()
	}
	a.prevOpen = a.open
}

// Tap toca o tap tempo (tecla T): com um pedal com TAP aberto, só nele; senão, em todos os
// pedais com TAP (delay, tremolo e synth), como um andamento global.
func (a *App) Tap(now time.Time) {
	if p := a.pedals[a.open]; p != nil && p.tap != nil {
		p.tap.TapAt(now)
		return
	}
	for _, id := range []string{"delay", "tremolo", "synth"} {
		if p := a.pedals[id]; p != nil && p.tap != nil {
			p.tap.TapAt(now)
		}
	}
}

func (a *App) keys(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameSpace}, key.Filter{Name: "T"}, key.Filter{Name: "A"},
			key.Filter{Name: "1"}, key.Filter{Name: "2"}, key.Filter{Name: "3"}, key.Filter{Name: "4"}, key.Filter{Name: "5"}, key.Filter{Name: "6"})
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		// digitando um nome (preset ou banco da pedaleira): só o Esc vale
		typing := a.open == SaveAsID || (a.open == PedalboardEditID && a.pedalboard.Typing(gtx))
		if typing && e.Name != key.NameEscape {
			continue
		}
		switch e.Name {
		case key.NameEscape:
			if a.menu.open {
				a.menu.open = false
			} else {
				a.open = ""
			}
		case "A": // afinador: abre e fecha
			if a.open == TunerID {
				a.open = ""
			} else {
				a.open = TunerID
			}
		case key.NameSpace:
			// play/pause do arquivo: só com a fonte em Arquivo
			if a.Session.Source() == audio.SourceFile {
				a.transport.TogglePlay()
			}
		case "T":
			a.Tap(gtx.Now)
		case "1", "2", "3", "4", "5", "6": // footswitches da pedaleira
			a.pedalboard.Press(int(e.Name[0] - '1'))
		}
	}
}

func (a *App) topBar(gtx layout.Context) layout.Dimensions {
	th := a.th
	info := "placa fechada"
	if st := a.Session.Host.State(); st.Open {
		in := "nenhuma"
		if st.Duplex {
			in = st.InputName
		}
		lat := fmt.Sprintf("latência ~%.1f ms", a.Session.Host.EstimatedLatencyMs(a.Chain.Latency()))
		if m := a.audioSet.Measured(); m > 0 {
			lat = fmt.Sprintf("latência medida %.1f ms", m)
		}
		info = fmt.Sprintf("%s  ·  saída: %s  ·  entrada: %s  ·  %.0f Hz  ·  buffer %d  ·  %s",
			st.Backend, st.OutputName, in, st.SampleRate, st.Buffer, lat)
	}
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "AmpSim", textOpts{size: 22, color: th.Gold, bold: true})
		}),
		layout.Rigid(spacer(10, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, "simulador de amp e pedais", textOpts{size: 12, color: th.Dim})
		}),
		layout.Rigid(spacer(16, 0)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return drawText(gtx, th, info, textOpts{size: 12, color: th.Dim, align: text.End, width: gtx.Constraints.Max.X})
		}),
		layout.Rigid(spacer(12, 0)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.menu.Button(gtx, th)
		}),
	)
}

// PedalboardMinHeightDp é a altura da janela a partir da qual a pedaleira aparece.
const PedalboardMinHeightDp = 720

// SetVersion define a versão mostrada na área Sobre.
func (a *App) SetVersion(v string) { a.about.version = v }

func (a *App) footer(gtx layout.Context) layout.Dimensions {
	th := a.th
	byp := a.Renderer != nil && a.Renderer.Bypass()
	label, bg := "A/B: processado", th.PanelHi
	if byp {
		label, bg = "A/B: SECO", darken(th.Warn, 0.45)
	}
	rec := a.Session.Recorder
	recLabel, recBg := "● Gravar", th.PanelHi
	recFg := th.Text
	if a.Session.Source() != audio.SourceGuitar && !rec.Recording() {
		recFg = withAlpha(th.Text, 90) // apagado: gravar é para a guitarra ao vivo
	}
	if rec.Recording() {
		e := rec.Elapsed()
		recLabel = fmt.Sprintf("■ Parar  %d:%02d", int(e.Minutes()), int(e.Seconds())%60)
		recBg = darken(th.Clip, 0.25)
		if (gtx.Now.UnixMilli()/500)%2 == 0 {
			recBg = darken(th.Clip, 0.45) // pisca
		}
	}
	return panel(gtx, th.Panel, 10, 10, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return coloredButton(gtx, th, &a.recBtn, recLabel, gtx.Dp(112), recBg, recFg)
			}),
			layout.Rigid(spacer(4, 0)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return toggle(gtx, th, &a.diBtn, a.recDI, "DI")
			}),
			layout.Rigid(spacer(4, 0)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return smallButton(gtx, th, &a.recsBtn, "Gravações", 0)
			}),
			layout.Rigid(spacer(8, 0)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return coloredButton(gtx, th, &a.bypass, label, gtx.Dp(120), bg, th.Text)
			}),
			layout.Rigid(spacer(10, 0)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.meters.Layout(gtx, th) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if a.exporting {
					return drawText(gtx, th, fmt.Sprintf("exportando… %.0f%%", 100*a.exportPct),
						textOpts{size: 12, color: th.Gold, align: text.End, width: gtx.Constraints.Max.X})
				}
				if a.status == "" || gtx.Now.After(a.statusUntil) {
					return layout.Dimensions{}
				}
				c := th.Dim
				if a.statusErr {
					c = th.Clip
				}
				return drawText(gtx, th, a.status, textOpts{size: 12, color: c, align: text.End, width: gtx.Constraints.Max.X})
			}),
		)
	})
}

// overlay mostra o pedal aberto por cima da tela; clicar fora, Esc ou "×" fecha.
func (a *App) overlay(gtx layout.Context) {
	th := a.th
	var content func(gtx layout.Context) layout.Dimensions
	closeOverlay := func() { a.open = "" }
	if a.open == AudioID {
		content = func(gtx layout.Context) layout.Dimensions { return a.audioSet.Layout(gtx, th) }
	} else if a.open == RecordingsID {
		content = func(gtx layout.Context) layout.Dimensions { return a.recs.Layout(gtx, th) }
	} else if a.open == TunerID {
		content = func(gtx layout.Context) layout.Dimensions { return a.tuner.Layout(gtx, th) }
	} else if a.open == PedalboardEditID {
		content = func(gtx layout.Context) layout.Dimensions { return a.pedalboard.EditLayout(gtx, th) }
	} else if a.open == AddPedalID {
		content = func(gtx layout.Context) layout.Dimensions { return a.addPedal.Layout(gtx, th) }
	} else if a.open == ShortcutsID {
		content = func(gtx layout.Context) layout.Dimensions { return shortcutsLayout(gtx, th) }
	} else if a.open == AnalyzerID {
		content = func(gtx layout.Context) layout.Dimensions { return a.spectrum.Layout(gtx, th) }
	} else if a.open == AboutID {
		content = func(gtx layout.Context) layout.Dimensions { return a.about.Layout(gtx, th) }
	} else if a.open == IRListID {
		content = func(gtx layout.Context) layout.Dimensions { return a.irList.Layout(gtx, th, closeOverlay) }
	} else if a.open == NAMListID {
		content = func(gtx layout.Context) layout.Dimensions { return a.namList.Layout(gtx, th, closeOverlay) }
	} else if a.open == DriveNAMListID { // escolher volta para o pedal
		content = func(gtx layout.Context) layout.Dimensions {
			return a.driveNAM.Layout(gtx, th, func() { a.open = "drive" })
		}
	} else if a.open == PresetListID {
		content = func(gtx layout.Context) layout.Dimensions { return a.presetBar.ListLayout(gtx, th, closeOverlay) }
	} else if a.open == SaveAsID {
		content = func(gtx layout.Context) layout.Dimensions { return a.presetBar.SaveAsLayout(gtx, th, closeOverlay) }
	} else if p := a.pedals[a.open]; p != nil {
		content = func(gtx layout.Context) layout.Dimensions { return p.Layout(gtx, th) }
	} else {
		a.open = ""
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &a.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			a.open = ""
		}
	}
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &a.panelTag, Kinds: pointer.Press}); !ok {
			break
		}
	}
	if a.closeBtn.Clicked(gtx) {
		a.open = ""
	}

	full := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	paint.ColorOp{Color: withAlpha(rgb(0x000000), 170)}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	event.Op(gtx.Ops, &a.scrim)
	full.Pop()

	gtx.Constraints.Min = image.Point{}
	m := op.Record(gtx.Ops)
	dims := content(gtx)
	call := m.Stop()
	x := (gtx.Constraints.Max.X - dims.Size.X) / 2
	y := (gtx.Constraints.Max.Y - dims.Size.Y) / 2
	defer offset(gtx.Ops, x, y).Pop()
	// absorve os cliques no corpo do pedal (para não fechar)
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &a.panelTag)
	area.Pop()
	call.Add(gtx.Ops)
	// botão de fechar
	func() {
		defer offset(gtx.Ops, dims.Size.X-gtx.Dp(34), gtx.Dp(8)).Pop()
		closeButton(gtx, &a.closeBtn, 26)
	}()
}

// toggleRecord começa ou termina uma gravação.
func (a *App) toggleRecord() {
	rec := a.Session.Recorder
	if rec.Recording() {
		take, err := rec.Stop()
		if err != nil {
			a.setStatus(err.Error(), true)
			return
		}
		a.setStatus(fmt.Sprintf("gravação encerrada (%s)", humanDuration(take.Seconds)), take.Dropped > 0 || take.Silent())
		a.recs.dir = a.RecordDir
		a.recs.Show(&take) // painel "Gravação salva" com os arquivos e a lista
		a.open = RecordingsID
		return
	}
	if a.Session.Source() != audio.SourceGuitar {
		a.setStatus("Gravar é para tocar ao vivo (fonte Guitarra). Para salvar o arquivo com efeitos, use Exportar…", true)
		return
	}
	if _, err := rec.Start(a.RecordDir, a.recDI); err != nil {
		a.setStatus("gravação: "+err.Error(), true)
		return
	}
	a.setStatus("gravando em "+a.RecordDir, false)
}

// exportAsync processa o arquivo carregado pela cadeia atual e salva (numa goroutine,
// numa cópia da cadeia: o som ao vivo não muda).
func (a *App) exportAsync() {
	if a.exporting {
		return
	}
	clip := a.Player.Clip()
	if clip == nil {
		a.setStatus("abra um arquivo antes de exportar", true)
		return
	}
	st := a.Chain.State() // o som de agora; mexer nos knobs durante a exportação não afeta
	stereo := a.Chain.Stereo()
	suggested := strings.TrimSuffix(clip.Name, filepath.Ext(clip.Name)) + "-ampsim.wav"
	a.exporting, a.exportPct = true, 0
	go func() {
		path, err := a.PickSave("Exportar áudio processado", suggested)
		if err == nil && path != "" {
			var out [][]float64
			out, err = engine.RenderOfflineChannels(st, clip.Samples, float64(clip.FS), 2, 512, stereo, func(f float64) {
				a.Post(func() { a.exportPct = f })
			})
			if err == nil {
				err = wav.WriteFile(path, clip.FS, out, wav.PCM24)
			}
		}
		a.Post(func() {
			a.exporting = false
			switch {
			case err != nil:
				a.setStatus("exportar: "+err.Error(), true)
			case path != "":
				a.setStatus("exportado: "+path, false)
			}
		})
	}()
}

// loadRecording abre uma gravação no player e passa a fonte para Arquivo.
//
// A gravação principal já tem os efeitos: toca em A/B SECO, para a cadeia não ser
// aplicada de novo (amp em cima de amp). A DI é a guitarra limpa: passa pela cadeia,
// e trocar preset/pedais muda o som dela (reamplificar).
func (a *App) loadRecording(path string, processed bool) {
	go func() {
		err := a.Player.Load(path)
		a.Post(func() {
			if err != nil {
				a.setStatus(err.Error(), true)
				return
			}
			a.Session.SetSource(audio.SourceFile)
			a.Renderer.SetBypass(processed)
			a.recDry, a.recPath = processed, path
			a.Player.Play()
			a.open = ""
			if processed {
				a.setStatus("tocando a gravação como foi gravada (A/B em SECO: os efeitos já estão no arquivo)", false)
			} else {
				a.setStatus("DI carregada: troque preset, pedais e knobs para reamplificar", false)
			}
		})
	}()
}
