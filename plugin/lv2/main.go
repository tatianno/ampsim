// Comando lv2 é o plugin LV2 do AmpSim: compilado com -buildmode=c-shared vira o ampsim.so
// do bundle (veja build.sh). Cada preset (de fábrica ou do usuário) é um plugin, com a URI
// de ttl.URI. A camada C (lv2.c) só repassa as chamadas do host para cá.
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

	"github.com/tatianno/ampsim/engine"
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
	for _, p := range ps {
		s.uris = append(s.uris, C.CString(ttl.URI(p)))
	}
	return s
})

//export ampsimNumPlugins
func ampsimNumPlugins() C.int { return C.int(len(cstrings().uris)) }

//export ampsimPluginURI
func ampsimPluginURI(i C.uint32_t) *C.char { return cstrings().uris[i] }

//export ampsimStateKey
func ampsimStateKey() *C.char { return cstrings().key }

// instance é uma instância do plugin com os buffers das portas.
type instance struct {
	in       *plugin.Instance
	src, dst [][]float32
}

// presetFor devolve o estado do preset do plugin uri, lido agora (o do usuário, na versão
// atual do arquivo); nil se o preset não existe mais.
func presetFor(uri string) *engine.State {
	id, ok := ttl.ID(uri)
	if !ok {
		return nil
	}
	ps, err := plugin.Presets(presetDir())
	if err != nil {
		logf("%v", err)
	}
	for _, p := range ps {
		if p.ID != id {
			continue
		}
		st, err := p.State()
		if err != nil {
			logf("preset %q: %v", p.Name, err)
			return nil
		}
		return &st
	}
	logf("preset %s não existe mais: o plugin deixa passar o sinal seco", id)
	return nil
}

//export ampsimNew
func ampsimNew(rate C.double, uri *C.char) C.uintptr_t {
	if rate <= 0 {
		return 0
	}
	it := &instance{
		in:  plugin.New(float64(rate), false, presetFor(C.GoString(uri))),
		src: make([][]float32, 1),
		dst: make([][]float32, 1),
	}
	return C.uintptr_t(cgo.NewHandle(it))
}

//export ampsimFree
func ampsimFree(h C.uintptr_t) { cgo.Handle(h).Delete() }

//export ampsimRun
func ampsimRun(h C.uintptr_t, ports **C.float, n C.uint32_t) {
	it := cgo.Handle(h).Value().(*instance)
	p := unsafe.Slice(ports, ttl.NumPorts)
	ctl := func(i int) float64 {
		if p[i] == nil {
			return plugin.Controls[i].Default
		}
		return float64(*p[i])
	}
	s := plugin.Settings{
		InputDB:  ctl(plugin.CtlInput),
		OutputDB: ctl(plugin.CtlOutput),
		Enabled:  ctl(plugin.CtlEnabled) > 0.5,
	}
	in, out := p[ttl.PortIn], p[ttl.PortOut]
	if in == nil || out == nil {
		return
	}
	k := int(n)
	it.src[0] = unsafe.Slice((*float32)(unsafe.Pointer(in)), k)
	it.dst[0] = unsafe.Slice((*float32)(unsafe.Pointer(out)), k)
	it.in.Process(it.src, it.dst, k, s)
	if lp := p[ttl.PortLatency]; lp != nil {
		*lp = C.float(it.in.Latency())
	}
}

//export ampsimSave
func ampsimSave(h C.uintptr_t, size *C.size_t) *C.char {
	it := cgo.Handle(h).Value().(*instance)
	data, err := it.in.SaveState()
	if err != nil {
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
