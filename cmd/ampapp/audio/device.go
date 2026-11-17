package audio

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/gen2brain/malgo"

	"github.com/tatianno/ampsim/engine"
)

// DeviceInfo descreve um dispositivo de saída.
type DeviceInfo struct {
	Index     int
	Name      string
	IsDefault bool
	id        malgo.DeviceID
}

// Config da saída de áudio.
type Config struct {
	Device     int    // índice em ListOutputs; -1 = padrão do sistema
	SampleRate uint32 // 0 = 48000
	Buffer     uint32 // quadros por período; 0 = 256
	Channels   uint32 // 0 = 2
}

// Output é a saída de áudio aberta.
type Output struct {
	ctx      *malgo.AllocatedContext
	dev      *malgo.Device
	renderer *Renderer
	fs       float64
	buffer   uint32
	channels int
	name     string
}

func initContext() (*malgo.AllocatedContext, error) {
	return malgo.InitContext(nil, malgo.ContextConfig{}, nil)
}

// ListOutputs lista os dispositivos de saída.
func ListOutputs() ([]DeviceInfo, error) {
	ctx, err := initContext()
	if err != nil {
		return nil, err
	}
	defer func() { ctx.Uninit(); ctx.Free() }()
	return listOutputs(ctx)
}

func listOutputs(ctx *malgo.AllocatedContext) ([]DeviceInfo, error) {
	infos, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceInfo, len(infos))
	for i := range infos {
		out[i] = DeviceInfo{Index: i, Name: infos[i].Name(), IsDefault: infos[i].IsDefault != 0, id: infos[i].ID}
	}
	return out, nil
}

// OpenOutput abre a saída e prepara a cadeia na taxa real do dispositivo.
// A fonte é ligada depois, com Renderer().SetSource.
func OpenOutput(cfg Config, chain *engine.Chain) (*Output, error) {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 48000
	}
	if cfg.Buffer == 0 {
		cfg.Buffer = 256
	}
	if cfg.Channels == 0 {
		cfg.Channels = 2
	}
	ctx, err := initContext()
	if err != nil {
		return nil, fmt.Errorf("iniciando áudio: %w", err)
	}
	o := &Output{ctx: ctx, channels: int(cfg.Channels), buffer: cfg.Buffer, name: "padrão do sistema"}

	dc := malgo.DefaultDeviceConfig(malgo.Playback)
	dc.Playback.Format = malgo.FormatF32
	dc.Playback.Channels = cfg.Channels
	dc.SampleRate = cfg.SampleRate
	dc.PeriodSizeInFrames = cfg.Buffer
	dc.Periods = 2
	dc.PerformanceProfile = malgo.LowLatency

	if cfg.Device >= 0 {
		devs, err := listOutputs(ctx)
		if err != nil {
			o.Close()
			return nil, err
		}
		if cfg.Device >= len(devs) {
			o.Close()
			return nil, fmt.Errorf("dispositivo %d não existe (há %d)", cfg.Device, len(devs))
		}
		id := devs[cfg.Device].id
		dc.Playback.DeviceID = id.Pointer()
		o.name = devs[cfg.Device].Name
	}

	// A cadeia precisa estar pronta antes do primeiro callback. Preparamos na taxa
	// pedida; se o dispositivo abrir em outra taxa, preparamos de novo antes do Start.
	chain.Prepare(float64(cfg.SampleRate), MaxBlock)
	o.renderer = NewRenderer(chain, float64(cfg.SampleRate))

	cb := malgo.DeviceCallbacks{
		Data: func(out, _ []byte, frames uint32) {
			if len(out) == 0 {
				return
			}
			f := unsafe.Slice((*float32)(unsafe.Pointer(&out[0])), len(out)/4)
			o.renderer.Render(f, o.channels, int(frames))
		},
	}
	dev, err := malgo.InitDevice(ctx.Context, dc, cb)
	if err != nil {
		o.Close()
		return nil, fmt.Errorf("abrindo dispositivo: %w", err)
	}
	o.dev = dev
	if dev.PlaybackFormat() != malgo.FormatF32 {
		o.Close()
		return nil, errors.New("dispositivo não aceitou float32")
	}
	o.channels = int(dev.PlaybackChannels())
	o.fs = float64(dev.SampleRate())
	if o.fs != float64(cfg.SampleRate) {
		chain.Prepare(o.fs, MaxBlock)
		o.renderer = NewRenderer(chain, o.fs)
	}
	return o, nil
}

// Start inicia o áudio.
func (o *Output) Start() error { return o.dev.Start() }

// Stop para o áudio.
func (o *Output) Stop() error { return o.dev.Stop() }

// Close libera o dispositivo.
func (o *Output) Close() {
	if o.dev != nil {
		o.dev.Uninit()
		o.dev = nil
	}
	if o.ctx != nil {
		o.ctx.Uninit()
		o.ctx.Free()
		o.ctx = nil
	}
}

// Renderer devolve o render (para trocar a fonte e ler os medidores).
func (o *Output) Renderer() *Renderer { return o.renderer }

// SampleRate devolve a taxa real do dispositivo.
func (o *Output) SampleRate() float64 { return o.fs }

// Buffer devolve o tamanho de período pedido, em quadros.
func (o *Output) Buffer() uint32 { return o.buffer }

// Channels devolve o número de canais de saída.
func (o *Output) Channels() int { return o.channels }

// Name devolve o nome do dispositivo.
func (o *Output) Name() string { return o.name }

// OutputLatencyMs estima a latência de saída: períodos do buffer + latência da cadeia.
// (Não inclui a latência interna do servidor de som / hardware.)
func (o *Output) OutputLatencyMs(chainLatency int) float64 {
	return 1000 * (2*float64(o.buffer) + float64(chainLatency)) / o.fs
}
