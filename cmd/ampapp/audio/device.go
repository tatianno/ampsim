package audio

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// NoInput em Config.Input abre só a saída (sem entrada ao vivo).
const NoInput = -2

// DeviceInfo descreve um dispositivo de áudio.
type DeviceInfo struct {
	Index     int
	Name      string
	IsDefault bool
	id        malgo.DeviceID
}

// Config escolhe a placa. Os índices são os de ListDevices.
type Config struct {
	// "" = automático (no Linux: JACK próprio se houver, senão PulseAudio), "jack"
	// (backend JACK próprio), "pulseaudio" ou "alsa" (via miniaudio).
	Backend string
	Output  int    // -1 = saída padrão do sistema
	Input   int    // -1 = entrada padrão; NoInput = sem entrada
	Buffer  uint32 // quadros por período (0 = 256)
}

// State descreve o que está aberto de fato.
type State struct {
	Open        bool
	Backend     string // ex.: "JACK (PipeWire)", "PulseAudio"
	Duplex      bool   // entrada e saída abertas
	OutputName  string
	InputName   string
	OutChannels int
	InChannels  int
	SampleRate  float64
	Buffer      uint32
	Warning     string // ex.: a entrada não abriu e ficou só a saída
}

// Host é a placa de som aberta, ligada a um Renderer. Pode ser reaberta (outra saída,
// outra entrada, outro buffer) com o app rodando; o Renderer e o engine continuam os mesmos.
type Host struct {
	r *Renderer

	mu      sync.Mutex
	ctx     *malgo.AllocatedContext
	backend string
	dev     *malgo.Device
	cfg     Config
	st      State

	// lidos pelo callback; só mudam com o dispositivo parado
	outCh, inCh int

	jack bool // o backend JACK próprio está aberto
}

// Se o usuário já definiu PIPEWIRE_LATENCY (ou usou pw-jack -p), respeitamos.
var userPipewireLatency = os.Getenv("PIPEWIRE_LATENCY") != ""

// NewHost cria o host para o render (a cadeia já preparada na taxa do render).
func NewHost(r *Renderer) *Host { return &Host{r: r, cfg: Config{Output: -1, Input: -1}} }

func backends(name string) ([]malgo.Backend, error) {
	switch strings.ToLower(name) {
	case "":
		return nil, nil
	case "pulse", "pulseaudio", "pipewire":
		return []malgo.Backend{malgo.BackendPulseaudio}, nil
	case "alsa":
		return []malgo.Backend{malgo.BackendAlsa}, nil
	}
	return nil, fmt.Errorf("backend desconhecido %q (use jack, pulseaudio ou alsa)", name)
}

func newContext(backend string) (*malgo.AllocatedContext, error) {
	b, err := backends(backend)
	if err != nil {
		return nil, err
	}
	ctx, err := malgo.InitContext(b, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("iniciando áudio: %w", err)
	}
	return ctx, nil
}

func list(ctx *malgo.AllocatedContext, kind malgo.DeviceType) ([]DeviceInfo, error) {
	infos, err := ctx.Devices(kind)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceInfo, len(infos))
	for i := range infos {
		out[i] = DeviceInfo{Index: i, Name: infos[i].Name(), IsDefault: infos[i].IsDefault != 0, id: infos[i].ID}
	}
	return out, nil
}

// useJack diz se a configuração usa o backend JACK próprio.
func useJack(backend string) bool {
	return strings.EqualFold(backend, "jack") || (backend == "" && runtime.GOOS == "linux" && jackAvailable())
}

// ListDevices lista as saídas e entradas do backend.
func ListDevices(backend string) (outs, ins []DeviceInfo, err error) {
	if useJack(backend) {
		jo, ji, err := jackList()
		if err == nil || backend != "" {
			return jackDeviceInfos(jo), jackDeviceInfos(ji), err
		}
	}
	ctx, err := newContext(backend)
	if err != nil {
		return nil, nil, err
	}
	defer func() { ctx.Uninit(); ctx.Free() }()
	if outs, err = list(ctx, malgo.Playback); err != nil {
		return nil, nil, err
	}
	ins, err = list(ctx, malgo.Capture)
	return outs, ins, err
}

// Devices lista as saídas e entradas do backend em uso.
func (h *Host) Devices() (outs, ins []DeviceInfo, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.jack || h.ctx == nil {
		return ListDevices(h.cfg.Backend)
	}
	if outs, err = list(h.ctx, malgo.Playback); err != nil {
		return nil, nil, err
	}
	ins, err = list(h.ctx, malgo.Capture)
	return outs, ins, err
}

// Open abre (ou reabre) a placa e começa a tocar. Se a entrada não abrir, tenta só a
// saída e avisa em State().Warning.
func (h *Host) Open(cfg Config) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cfg.Buffer == 0 {
		cfg.Buffer = 256
	}
	h.closeDevice()

	warning := ""
	if useJack(cfg.Backend) {
		err := h.openJack(cfg)
		if err == nil {
			h.cfg = cfg
			return nil
		}
		if cfg.Backend != "" {
			return err // pediu JACK explicitamente
		}
		warning = fmt.Sprintf("o JACK não abriu (%v); usando PulseAudio", err)
	}

	if h.ctx == nil || cfg.Backend != h.backend {
		h.closeContext()
		ctx, err := newContext(cfg.Backend)
		if err != nil {
			return err
		}
		h.ctx, h.backend = ctx, cfg.Backend
	}

	err := errors.New("sem entrada")
	if cfg.Input != NoInput {
		err = h.openDevice(cfg, true)
		if err != nil {
			warning = strings.TrimPrefix(warning+"; ", "; ") + fmt.Sprintf("a entrada não abriu (%v); tocando só a saída", err)
		}
	}
	if err != nil {
		if err = h.openDevice(cfg, false); err != nil {
			return err
		}
	}
	if err := h.dev.Start(); err != nil {
		h.closeDevice()
		return fmt.Errorf("iniciando o dispositivo: %w", err)
	}
	h.cfg = cfg
	h.st.Warning = warning
	h.st.Open = true
	return nil
}

func (h *Host) openDevice(cfg Config, duplex bool) error {
	kind := malgo.Playback
	if duplex {
		kind = malgo.Duplex
	}
	dc := malgo.DefaultDeviceConfig(kind)
	dc.SampleRate = uint32(h.r.SampleRate())
	dc.PeriodSizeInFrames = cfg.Buffer
	dc.Periods = 2
	dc.PerformanceProfile = malgo.LowLatency
	dc.Playback.Format = malgo.FormatF32
	dc.Playback.Channels = 2
	st := State{OutputName: "padrão do sistema", Duplex: duplex, Buffer: cfg.Buffer, Backend: "PulseAudio"}
	if strings.EqualFold(cfg.Backend, "alsa") {
		st.Backend = "ALSA"
	}

	outs, err := list(h.ctx, malgo.Playback)
	if err != nil {
		return err
	}
	if cfg.Output >= 0 {
		if cfg.Output >= len(outs) {
			return fmt.Errorf("saída %d não existe (há %d)", cfg.Output, len(outs))
		}
		dc.Playback.DeviceID = outs[cfg.Output].id.Pointer()
		st.OutputName = outs[cfg.Output].Name
	}
	if duplex {
		dc.Capture.Format = malgo.FormatF32
		dc.Capture.Channels = 0 // canais nativos da placa
		st.InputName = "padrão do sistema"
		if cfg.Input >= 0 {
			ins, err := list(h.ctx, malgo.Capture)
			if err != nil {
				return err
			}
			if cfg.Input >= len(ins) {
				return fmt.Errorf("entrada %d não existe (há %d)", cfg.Input, len(ins))
			}
			dc.Capture.DeviceID = ins[cfg.Input].id.Pointer()
			st.InputName = ins[cfg.Input].Name
		}
	}

	cb := malgo.DeviceCallbacks{
		Data: func(out, in []byte, frames uint32) {
			if len(out) == 0 {
				return
			}
			o := unsafe.Slice((*float32)(unsafe.Pointer(&out[0])), len(out)/4)
			if len(in) > 0 {
				i := unsafe.Slice((*float32)(unsafe.Pointer(&in[0])), len(in)/4)
				h.r.RenderDuplex(o, h.outCh, i, h.inCh, int(frames))
			} else {
				h.r.Render(o, h.outCh, int(frames))
			}
		},
	}
	dev, err := malgo.InitDevice(h.ctx.Context, dc, cb)
	if err != nil {
		return err
	}
	if dev.PlaybackFormat() != malgo.FormatF32 || (duplex && dev.CaptureFormat() != malgo.FormatF32) {
		dev.Uninit()
		return errors.New("o dispositivo não aceitou float32")
	}
	if float64(dev.SampleRate()) != h.r.SampleRate() {
		dev.Uninit()
		return fmt.Errorf("o dispositivo abriu a %d Hz, esperava %.0f Hz", dev.SampleRate(), h.r.SampleRate())
	}
	h.dev = dev
	h.outCh = int(dev.PlaybackChannels())
	st.OutChannels = h.outCh
	if duplex {
		h.inCh = int(dev.CaptureChannels())
		st.InChannels = h.inCh
	} else {
		h.inCh = 0
	}
	st.SampleRate = h.r.SampleRate()
	h.st = st
	return nil
}

func (h *Host) closeDevice() {
	h.closeJack()
	if h.dev != nil {
		h.dev.Uninit() // para o dispositivo e espera o callback terminar
		h.dev = nil
	}
	h.st = State{}
}

func (h *Host) closeContext() {
	if h.ctx != nil {
		h.ctx.Uninit()
		h.ctx.Free()
		h.ctx = nil
	}
}

// Close fecha a placa.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeDevice()
	h.closeContext()
}

// State devolve o que está aberto.
func (h *Host) State() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.st
	if h.jack {
		st.Buffer = jackBuffer() // o ciclo real (o PipeWire pode mudar ao vivo)
		if jackShutdown() {
			st.Open = false
			st.Warning = "o servidor JACK/PipeWire fechou a conexão"
		}
	}
	return st
}

// Config devolve a configuração em uso.
func (h *Host) Config() Config {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg
}

// Renderer devolve o render.
func (h *Host) Renderer() *Renderer { return h.r }

// EstimatedLatencyMs estima a latência de ida e volta pelos buffers do app (entrada +
// 2 períodos de saída) e a cadeia. O servidor de som e os conversores somam mais:
// a medida real vem do teste de latência.
func (h *Host) EstimatedLatencyMs(chainLatency int) float64 {
	st := h.State()
	if !st.Open {
		return 0
	}
	periods := 2.0
	if st.Duplex {
		periods = 3
	}
	return 1000 * (periods*float64(st.Buffer) + float64(chainLatency)) / st.SampleRate
}
