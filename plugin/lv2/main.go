// Comando lv2 é o plugin LV2 do AmpSim: compilado com -buildmode=c-shared vira o ampsim.so
// do bundle (veja build.sh). Cada pedal e cada preset (de fábrica ou do usuário) é um plugin
// (ttl.Plugins). A camada C (lv2.c) só repassa as chamadas do host para cá.
package main

/*
#cgo CFLAGS: -O2 -Wall
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"os"
	"runtime/cgo"
	"sync"
	"unsafe"

	"github.com/tatianno/ampsim/plugin"
	"github.com/tatianno/ampsim/plugin/lv2/ttl"
	"github.com/tatianno/ampsim/presets"
)

// O build.sh liga com -z nodelete: assim o dlclose do host não descarrega o .so, porque o
// runtime do Go não pode ser descarregado (as threads dele continuariam rodando código que sumiu).

func main() {}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ampsim: "+format+"\n", args...)
}

func presetDir() string {
	dir, _ := presets.Dir()
	return dir
}

// cstrings são as strings C entregues ao host (ficam até o fim do processo): a chave do
// estado e as URIs dos plugins, na ordem dos descritores.
var cstrings = sync.OnceValue(func() (s struct {
	key  *C.char
	uris []*C.char
}) {
	s.key = C.CString(ttl.StateKey)
	ps, err := plugin.Presets(presetDir())
	if err != nil {
		logf("%v", err)
	}
	for _, p := range ttl.Plugins(ps) {
		s.uris = append(s.uris, C.CString(p.URI()))
	}
	return s
})

//export ampsimNumPlugins
func ampsimNumPlugins() C.int { return C.int(len(cstrings().uris)) }

//export ampsimPluginURI
func ampsimPluginURI(i C.uint32_t) *C.char { return cstrings().uris[i] }

//export ampsimStateKey
func ampsimStateKey() *C.char { return cstrings().key }

// instance é uma instância do plugin com o layout das portas e os buffers.
type instance struct {
	in       *plugin.Instance
	pl       ttl.Plugin
	src, dst [][]float32
	vals     []float64 // valores das entradas de controle no bloco
	s        plugin.Settings
}

// find devolve o plugin da URI, com a lista de presets lida agora (o preset do usuário, na
// versão atual do arquivo).
func find(uri string) (ttl.Plugin, bool) {
	ps, err := plugin.Presets(presetDir())
	if err != nil {
		logf("%v", err)
	}
	return ttl.Find(uri, ps)
}

//export ampsimNew
func ampsimNew(rate C.double, curi *C.char) C.uintptr_t {
	uri := C.GoString(curi)
	pl, ok := find(uri)
	if rate <= 0 || !ok {
		logf("plugin %s não existe mais", uri)
		return 0
	}
	it := &instance{pl: pl, src: make([][]float32, 1), dst: make([][]float32, 1), vals: make([]float64, len(pl.Controls))}
	if pl.Pedal != "" {
		in, err := plugin.NewPedal(float64(rate), pl.Pedal)
		if err != nil {
			logf("%v", err)
			return 0
		}
		it.in = in
		it.s.Params = it.vals[:len(pl.Controls)-1] // o último controle é o Ligado
	} else {
		st, err := pl.Preset.State()
		if err != nil {
			logf("preset %q: %v", pl.Preset.Name, err)
			it.in = plugin.New(float64(rate), false, nil) // sinal seco
		} else {
			it.in = plugin.New(float64(rate), false, &st)
		}
	}
	return C.uintptr_t(cgo.NewHandle(it))
}

//export ampsimFree
func ampsimFree(h C.uintptr_t) { cgo.Handle(h).Delete() }

// read lê as entradas de controle (o padrão do .ttl na porta não conectada) para it.s.
func (it *instance) read(p []*C.float) {
	for i, c := range it.pl.Controls {
		v := c.Default
		if p[i] != nil {
			v = float64(*p[i])
		}
		it.vals[i] = v
	}
	n := len(it.vals)
	if it.pl.Pedal != "" {
		it.s.Enabled = it.vals[n-1] > 0.5
		return
	}
	it.s.InputDB = it.vals[plugin.CtlInput]
	it.s.OutputDB = it.vals[plugin.CtlOutput]
	it.s.Enabled = it.vals[plugin.CtlEnabled] > 0.5
}

//export ampsimActivate
func ampsimActivate(h C.uintptr_t, ports **C.float) {
	it := cgo.Handle(h).Value().(*instance)
	it.read(unsafe.Slice(ports, it.pl.NumPorts()))
	it.in.Activate(it.s.Params)
}

//export ampsimRun
func ampsimRun(h C.uintptr_t, ports **C.float, n C.uint32_t) {
	it := cgo.Handle(h).Value().(*instance)
	p := unsafe.Slice(ports, it.pl.NumPorts())
	it.read(p)
	in, out := p[it.pl.PortIn()], p[it.pl.PortOut()]
	if in == nil || out == nil {
		return
	}
	k := int(n)
	it.src[0] = unsafe.Slice((*float32)(unsafe.Pointer(in)), k)
	it.dst[0] = unsafe.Slice((*float32)(unsafe.Pointer(out)), k)
	it.in.Process(it.src, it.dst, k, it.s)
	if lp := p[it.pl.PortLatency()]; lp != nil {
		*lp = C.float(it.in.Latency())
	}
}

//export ampsimSave
func ampsimSave(h C.uintptr_t, size *C.size_t) *C.char {
	it := cgo.Handle(h).Value().(*instance)
	data, err := it.in.SaveState()
	if err != nil || data == nil {
		return nil
	}
	*size = C.size_t(len(data) + 1)
	return (*C.char)(C.CBytes(append(data, 0)))
}

//export ampsimRestore
func ampsimRestore(h C.uintptr_t, data *C.char, size C.size_t) C.int {
	it := cgo.Handle(h).Value().(*instance)
	b := C.GoBytes(unsafe.Pointer(data), C.int(size))
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	if err := it.in.RestoreState(b); err != nil {
		logf("%v", err)
		return 0
	}
	return 1
}
