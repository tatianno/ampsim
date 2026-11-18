//go:build linux

package audio

/*
#cgo LDFLAGS: -ldl
#include <stdint.h>
#include <stdlib.h>
int ampsim_jack_load(const char* path);
int ampsim_jack_open(const char* name, int nIn);
int ampsim_jack_activate(void);
void ampsim_jack_close(void);
uint32_t ampsim_jack_sample_rate(void);
uint32_t ampsim_jack_buffer_size(void);
int ampsim_jack_is_shutdown(void);
const char* ampsim_jack_port_name(int out, int idx);
int ampsim_jack_connect(const char* src, const char* dst);
char** ampsim_jack_list(int capture);
void ampsim_jack_free_ports(char** ports);
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"unsafe"
)

// jackMaxFrames é o maior ciclo aceito no callback (o PipeWire usa no máximo 8192).
const jackMaxFrames = 8192

// jackMaxIn é o número máximo de entradas registradas (igual a AMPSIM_MAX_IN no C).
const jackMaxIn = 8

var errJackUnavailable = errors.New("JACK indisponível (instale o pipewire-jack)")

// jackLibs são as bibliotecas tentadas, em ordem: a do PipeWire primeiro (dispensa o pw-jack).
func jackLibs() []string {
	libs, _ := filepath.Glob("/usr/lib/*/pipewire-0.3/jack/libjack.so.0")
	libs = append(libs, "/usr/lib/pipewire-0.3/jack/libjack.so.0", "/usr/lib64/pipewire-0.3/jack/libjack.so.0")
	return append(libs, "libjack.so.0")
}

var jackLoaded atomic.Bool

func jackLoad() error {
	if jackLoaded.Load() {
		return nil
	}
	for _, p := range jackLibs() {
		cs := C.CString(p)
		rc := C.ampsim_jack_load(cs)
		C.free(unsafe.Pointer(cs))
		if rc == 0 {
			jackLoaded.Store(true)
			return nil
		}
	}
	return errJackUnavailable
}

// jackDev é o estado usado pelo callback (thread de tempo real do JACK).
type jackDev struct {
	r   *Renderer
	in  []float32 // entrada intercalada (até jackMaxIn canais)
	out []float32 // saída intercalada estéreo
}

var jackActive atomic.Pointer[jackDev]

//export ampsimJackProcess
func ampsimJackProcess(ins **C.float, nin C.int, outL, outR *C.float, n C.uint32_t) {
	frames := int(n)
	l := unsafe.Slice((*float32)(unsafe.Pointer(outL)), frames)
	r := unsafe.Slice((*float32)(unsafe.Pointer(outR)), frames)
	d := jackActive.Load()
	if d == nil || frames > jackMaxFrames {
		clear(l)
		clear(r)
		return
	}
	k := int(nin)
	out := d.out[:2*frames]
	if k > 0 {
		il := d.in[:k*frames]
		for c, p := range unsafe.Slice(ins, k) {
			src := unsafe.Slice((*float32)(unsafe.Pointer(p)), frames)
			for i, v := range src {
				il[i*k+c] = v
			}
		}
		d.r.RenderDuplex(out, 2, il, k, frames)
	} else {
		d.r.Render(out, 2, frames)
	}
	for i := 0; i < frames; i++ {
		l[i], r[i] = out[2*i], out[2*i+1]
	}
}

func cStrings(p **C.char) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, s := range unsafe.Slice(p, 1<<16) {
		if s == nil {
			break
		}
		out = append(out, C.GoString(s))
	}
	return out
}

// jackRawPorts devolve os nomes das portas físicas de captura e de reprodução.
func jackRawPorts() (capture, playback []string, err error) {
	if err := jackLoad(); err != nil {
		return nil, nil, err
	}
	pc := C.ampsim_jack_list(1)
	pp := C.ampsim_jack_list(0)
	if pc == nil && pp == nil {
		return nil, nil, errors.New("não foi possível falar com o JACK/PipeWire")
	}
	capture, playback = cStrings(pc), cStrings(pp)
	C.ampsim_jack_free_ports(pc)
	C.ampsim_jack_free_ports(pp)
	return capture, playback, nil
}

// jackList lista as placas: entradas (com portas de captura) e saídas.
func jackList() (outs, ins []jackDevice, err error) {
	c, p, err := jackRawPorts()
	if err != nil {
		return nil, nil, err
	}
	return groupJackPorts(p), groupJackPorts(c), nil
}

func jackDeviceInfos(devs []jackDevice) []DeviceInfo {
	out := make([]DeviceInfo, len(devs))
	for i, d := range devs {
		out[i] = DeviceInfo{Index: i, Name: jackDisplayName(d.Name), IsDefault: i == 0}
	}
	return out
}

func pick(devs []jackDevice, idx int, what string) (jackDevice, error) {
	if len(devs) == 0 {
		return jackDevice{}, fmt.Errorf("nenhuma %s física no JACK", what)
	}
	if idx < 0 {
		return devs[0], nil
	}
	if idx >= len(devs) {
		return jackDevice{}, fmt.Errorf("%s %d não existe (há %d)", what, idx, len(devs))
	}
	return devs[idx], nil
}

// openJack abre o cliente JACK "ampsim" ligado só às placas escolhidas.
func (h *Host) openJack(cfg Config) error {
	outs, ins, err := jackList()
	if err != nil {
		return err
	}
	od, err := pick(outs, cfg.Output, "saída")
	if err != nil {
		return err
	}
	var id jackDevice
	nIn := 0
	if cfg.Input != NoInput {
		if id, err = pick(ins, cfg.Input, "entrada"); err != nil {
			return err
		}
		nIn = min(len(id.Ports), jackMaxIn)
	}

	// O ciclo (quantum) do PipeWire vem da latência que o cliente pede.
	if !userPipewireLatency {
		os.Setenv("PIPEWIRE_LATENCY", fmt.Sprintf("%d/%d", cfg.Buffer, int(h.r.SampleRate())))
	}
	name := C.CString("ampsim")
	defer C.free(unsafe.Pointer(name))
	if rc := C.ampsim_jack_open(name, C.int(nIn)); rc != 0 {
		C.ampsim_jack_close()
		return fmt.Errorf("abrindo o cliente JACK (código %d)", int(rc))
	}
	if fs := float64(C.ampsim_jack_sample_rate()); fs != h.r.SampleRate() {
		C.ampsim_jack_close()
		return fmt.Errorf("o JACK está a %.0f Hz, o app a %.0f Hz", fs, h.r.SampleRate())
	}

	jackActive.Store(&jackDev{
		r:   h.r,
		in:  make([]float32, jackMaxIn*jackMaxFrames),
		out: make([]float32, 2*jackMaxFrames),
	})
	if rc := C.ampsim_jack_activate(); rc != 0 {
		jackActive.Store(nil)
		C.ampsim_jack_close()
		return fmt.Errorf("ativando o cliente JACK (código %d)", int(rc))
	}

	// Liga só às placas escolhidas: L/R nas duas primeiras portas da saída; cada entrada
	// da placa numa entrada nossa.
	connect := func(src, dst string) {
		s, d := C.CString(src), C.CString(dst)
		C.ampsim_jack_connect(s, d)
		C.free(unsafe.Pointer(s))
		C.free(unsafe.Pointer(d))
	}
	for i := 0; i < 2; i++ {
		dst := od.Ports[min(i, len(od.Ports)-1)]
		connect(C.GoString(C.ampsim_jack_port_name(1, C.int(i))), dst)
	}
	for i := 0; i < nIn; i++ {
		connect(id.Ports[i], C.GoString(C.ampsim_jack_port_name(0, C.int(i))))
	}

	h.jack = true
	h.st = State{
		Open:        true,
		Backend:     "JACK (PipeWire)",
		Duplex:      nIn > 0,
		OutputName:  jackDisplayName(od.Name),
		OutChannels: 2,
		InChannels:  nIn,
		SampleRate:  h.r.SampleRate(),
	}
	if nIn > 0 {
		h.st.InputName = jackDisplayName(id.Name)
	}
	return nil
}

func (h *Host) closeJack() {
	if !h.jack {
		return
	}
	C.ampsim_jack_close() // desativa antes: o callback não roda mais depois daqui
	jackActive.Store(nil)
	h.jack = false
}

// jackBuffer devolve o ciclo atual do JACK (o PipeWire pode mudar o quantum ao vivo).
func jackBuffer() uint32 { return uint32(C.ampsim_jack_buffer_size()) }

func jackShutdown() bool { return C.ampsim_jack_is_shutdown() != 0 }

func jackAvailable() bool { return jackLoad() == nil }
