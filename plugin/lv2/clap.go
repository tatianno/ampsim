package main

// O plugin CLAP (clap.c repassa as chamadas do host para cá): um plugin só, estéreo, com o
// parâmetro Preset numa lista (os de fábrica e os do usuário, lida ao criar a instância), a
// troca de preset com o áudio rodando e a cópia do preset no estado. O mesmo .so serve ao
// LV2 e ao CLAP (~/.clap/ampsim.clap é um link para ele): um runtime Go só no processo.

/*
#cgo CFLAGS: -I${SRCDIR}/clap/include
#include <clap/clap.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"math"
	"runtime/cgo"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/plugin"
)

// version é a versão do AmpSim (o build.sh passa com -ldflags -X).
var version = "dev"

// IDs dos parâmetros CLAP (estáveis: ficam nos projetos e nas automações).
const (
	clapPreset = iota
	clapInput
	clapOutput
	clapBypass
	clapNumParams
)

// clapInst é uma instância do plugin CLAP.
type clapInst struct {
	list  []plugin.Preset              // os presets, lidos ao criar a instância
	extra atomic.Pointer[engine.State] // a cópia de um projeto cujo preset não está na lista (índice len(list))

	// parâmetros: escritos pelo host (thread de áudio ou principal), lidos por get_value
	preset       atomic.Int64
	inDB, outDB  atomic.Uint64 // bits de float64
	bypass       atomic.Bool
	want, loaded atomic.Int64 // preset pedido e aplicado (−1 = nenhum)
	mu           sync.Mutex   // in, pending
	in           *plugin.Instance
	pending      *engine.State // cópia carregada antes do activate
	sig, done    chan struct{}
	wg           sync.WaitGroup
	src, dst     [][]float32
	settings     plugin.Settings
}

func newClapInst() *clapInst {
	ps, err := plugin.Presets(presetDir())
	if err != nil {
		logf("%v", err)
	}
	c := &clapInst{list: ps}
	c.loaded.Store(-1)
	c.want.Store(-1)
	return c
}

// entries é o número de valores do parâmetro Preset: a lista e a "cópia do projeto".
func (c *clapInst) entries() int { return len(c.list) + 1 }

// label devolve o nome do valor i do parâmetro Preset.
func (c *clapInst) label(i int) string {
	switch {
	case i >= 0 && i < len(c.list):
		return c.list[i].Name
	case i == len(c.list) && c.extra.Load() != nil:
		return c.extra.Load().Name + " (cópia do projeto)"
	}
	return "(cópia do projeto)"
}

// resolve devolve o estado do valor i do Preset, lido agora (o preset do usuário, na versão
// atual do arquivo); nil sem preset. Fora da thread de áudio.
func (c *clapInst) resolve(i int) *engine.State {
	if i == len(c.list) {
		return c.extra.Load()
	}
	if i < 0 || i >= len(c.list) {
		return nil
	}
	st, err := c.list[i].State()
	if err != nil {
		logf("preset %q: %v", c.list[i].Name, err)
		return nil
	}
	return &st
}

func f64(a *atomic.Uint64) float64       { return math.Float64frombits(a.Load()) }
func setF64(a *atomic.Uint64, v float64) { a.Store(math.Float64bits(v)) }

// setParam aplica um valor vindo do host (thread de áudio, ou principal no flush).
func (c *clapInst) setParam(id int, v float64) {
	switch id {
	case clapPreset:
		i := max(0, min(int(math.Round(v)), c.entries()-1))
		c.preset.Store(int64(i))
		c.request(i)
	case clapInput:
		setF64(&c.inDB, v)
	case clapOutput:
		setF64(&c.outDB, v)
	case clapBypass:
		c.bypass.Store(v >= 0.5)
	}
}

// request pede o preset i: a goroutine da instância aplica (não bloqueia).
func (c *clapInst) request(i int) {
	if int64(i) == c.want.Load() {
		return
	}
	c.want.Store(int64(i))
	if c.sig == nil {
		return // inativo: o activate monta com o preset atual
	}
	select {
	case c.sig <- struct{}{}:
	default:
	}
}

// loop aplica os presets pedidos, fora da thread de áudio.
func (c *clapInst) loop(in *plugin.Instance, sig, done chan struct{}) {
	defer c.wg.Done()
	for {
		select {
		case <-done:
			return
		case <-sig:
		}
		if w := c.want.Load(); w != c.loaded.Load() {
			in.Apply(c.resolve(int(w)))
			c.loaded.Store(w)
		}
	}
}

func (c *clapInst) activate(fs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := int(c.preset.Load())
	st := c.pending
	if st == nil || int64(i) != c.loaded.Load() {
		st = c.resolve(i)
	}
	c.pending = nil
	c.in = plugin.NewLive(fs, st)
	c.loaded.Store(int64(i))
	c.want.Store(int64(i))
	c.sig, c.done = make(chan struct{}, 1), make(chan struct{})
	c.src, c.dst = make([][]float32, 0, 2), make([][]float32, 0, 2)
	c.wg.Add(1)
	go c.loop(c.in, c.sig, c.done)
}

func (c *clapInst) deactivate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.in == nil {
		return
	}
	close(c.done)
	c.wg.Wait()
	c.pending = c.in.Snapshot() // o som atual volta no próximo activate
	c.in, c.sig, c.done = nil, nil, nil
}

// clapState é o estado salvo no projeto: a cópia do preset e os controles.
type clapState struct {
	Version int           `json:"versao"`
	Name    string        `json:"nome"`
	Preset  *engine.State `json:"preset,omitempty"`
	InDB    float64       `json:"entrada"`
	OutDB   float64       `json:"saida"`
	Bypass  bool          `json:"bypass,omitempty"`
}

func (c *clapInst) save() ([]byte, error) {
	c.mu.Lock()
	var st *engine.State
	switch {
	case c.in != nil:
		st = c.in.Snapshot()
	case c.pending != nil:
		st = c.pending
	default:
		st = c.resolve(int(c.preset.Load()))
	}
	c.mu.Unlock()
	s := clapState{Version: 1, Preset: st, InDB: f64(&c.inDB), OutDB: f64(&c.outDB), Bypass: c.bypass.Load()}
	if st != nil {
		s.Name = st.Name
	}
	return json.Marshal(s)
}

// load volta ao estado salvo: o som é o da cópia; o Preset aponta para o preset de mesmo
// nome, ou para a "cópia do projeto" se ele não existe mais. Thread principal.
func (c *clapInst) load(data []byte) error {
	var s clapState
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("estado do plugin: %w", err)
	}
	if s.Version > 1 {
		return fmt.Errorf("estado do plugin versão %d é mais nova que a suportada (1)", s.Version)
	}
	setF64(&c.inDB, s.InDB)
	setF64(&c.outDB, s.OutDB)
	c.bypass.Store(s.Bypass)
	i := len(c.list)
	for k, p := range c.list {
		if p.Name == s.Name {
			i = k
			break
		}
	}
	if i == len(c.list) {
		c.extra.Store(s.Preset)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.preset.Store(int64(i))
	c.want.Store(int64(i))
	c.loaded.Store(int64(i))
	if c.in != nil {
		c.in.Apply(s.Preset)
	} else {
		c.pending = s.Preset
	}
	return nil
}

func (c *clapInst) value(id int) float64 {
	switch id {
	case clapPreset:
		return float64(c.preset.Load())
	case clapInput:
		return f64(&c.inDB)
	case clapOutput:
		return f64(&c.outDB)
	case clapBypass:
		if c.bypass.Load() {
			return 1
		}
	}
	return 0
}

func (c *clapInst) text(id int, v float64) string {
	switch id {
	case clapPreset:
		return c.label(int(math.Round(v)))
	case clapBypass:
		if v >= 0.5 {
			return "Desligado (bypass)"
		}
		return "Ligado"
	}
	return fmt.Sprintf("%.1f dB", v)
}

func (c *clapInst) parse(id int, s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if id == clapPreset {
		for i := range c.entries() {
			if strings.EqualFold(c.label(i), s) {
				return float64(i), true
			}
		}
	}
	if id == clapBypass {
		switch strings.ToLower(s) {
		case "ligado", "off", "0":
			return 0, true
		case "bypass", "desligado", "desligado (bypass)", "on", "1":
			return 1, true
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.ReplaceAll(s, ",", "."), " dB"), 64)
	return v, err == nil
}

func clap(h C.uintptr_t) *clapInst { return cgo.Handle(h).Value().(*clapInst) }

// cstr copia s para o buffer C de tamanho n (com o '\0').
func cstr(dst *C.char, n int, s string) {
	b := unsafe.Slice((*byte)(unsafe.Pointer(dst)), n)
	k := copy(b[:n-1], s)
	b[k] = 0
}

//export ampsimVersion
func ampsimVersion() *C.char { return cversion() }

var cversion = sync.OnceValue(func() *C.char { return C.CString(version) })

//export ampsimClapNew
func ampsimClapNew() C.uintptr_t { return C.uintptr_t(cgo.NewHandle(newClapInst())) }

//export ampsimClapFree
func ampsimClapFree(h C.uintptr_t) {
	clap(h).deactivate()
	cgo.Handle(h).Delete()
}

//export ampsimClapActivate
func ampsimClapActivate(h C.uintptr_t, fs C.double) C.bool {
	if fs <= 0 {
		return false
	}
	clap(h).activate(float64(fs))
	return true
}

//export ampsimClapDeactivate
func ampsimClapDeactivate(h C.uintptr_t) { clap(h).deactivate() }

//export ampsimClapReset
func ampsimClapReset(h C.uintptr_t) {
	if in := clap(h).in; in != nil {
		in.Activate(nil) // limpa as caudas
	}
}

//export ampsimClapLatency
func ampsimClapLatency(h C.uintptr_t) C.uint32_t {
	if in := clap(h).in; in != nil {
		return C.uint32_t(in.Latency())
	}
	return 0
}

//export ampsimClapParam
func ampsimClapParam(h C.uintptr_t, id C.uint32_t, v C.double) {
	clap(h).setParam(int(id), float64(v))
}

//export ampsimClapProcess
func ampsimClapProcess(h C.uintptr_t, in **C.float, nin C.uint32_t, out **C.float, nout C.uint32_t, n C.uint32_t) {
	c := clap(h)
	if c.in == nil || nout == 0 {
		return
	}
	k := int(n)
	c.src, c.dst = c.src[:0], c.dst[:0]
	for _, p := range unsafe.Slice(in, int(min(nin, 2))) {
		c.src = append(c.src, unsafe.Slice((*float32)(unsafe.Pointer(p)), k))
	}
	for _, p := range unsafe.Slice(out, int(min(nout, 2))) {
		c.dst = append(c.dst, unsafe.Slice((*float32)(unsafe.Pointer(p)), k))
	}
	c.settings.InputDB = f64(&c.inDB)
	c.settings.OutputDB = f64(&c.outDB)
	c.settings.Enabled = !c.bypass.Load()
	c.in.Process(c.src, c.dst, k, c.settings)
}

//export ampsimClapParamInfo
func ampsimClapParamInfo(h C.uintptr_t, index C.uint32_t, info *C.clap_param_info_t) C.bool {
	c := clap(h)
	auto := C.CLAP_PARAM_IS_AUTOMATABLE
	var name string
	switch int(index) {
	case clapPreset:
		name = "Preset"
		info.flags = C.clap_param_info_flags(auto | C.CLAP_PARAM_IS_STEPPED | C.CLAP_PARAM_IS_ENUM)
		info.min_value, info.max_value, info.default_value = 0, C.double(c.entries()-1), 0
	case clapInput, clapOutput:
		ctl := plugin.Controls[plugin.CtlInput]
		if index == clapOutput {
			ctl = plugin.Controls[plugin.CtlOutput]
		}
		name = ctl.Name
		info.flags = C.clap_param_info_flags(auto)
		info.min_value, info.max_value, info.default_value = C.double(ctl.Min), C.double(ctl.Max), C.double(ctl.Default)
	case clapBypass:
		name = "Bypass"
		info.flags = C.clap_param_info_flags(auto | C.CLAP_PARAM_IS_STEPPED | C.CLAP_PARAM_IS_BYPASS)
		info.min_value, info.max_value, info.default_value = 0, 1, 0
	default:
		return false
	}
	info.id = C.clap_id(index)
	info.cookie = nil
	cstr(&info.name[0], len(info.name), name)
	cstr(&info.module[0], len(info.module), "")
	return true
}

//export ampsimClapValue
func ampsimClapValue(h C.uintptr_t, id C.uint32_t, out *C.double) C.bool {
	if id >= clapNumParams {
		return false
	}
	*out = C.double(clap(h).value(int(id)))
	return true
}

//export ampsimClapText
func ampsimClapText(h C.uintptr_t, id C.uint32_t, v C.double, buf *C.char, n C.uint32_t) C.bool {
	if id >= clapNumParams || n == 0 {
		return false
	}
	cstr(buf, int(n), clap(h).text(int(id), float64(v)))
	return true
}

//export ampsimClapParse
func ampsimClapParse(h C.uintptr_t, id C.uint32_t, text *C.char, out *C.double) C.bool {
	if id >= clapNumParams {
		return false
	}
	v, ok := clap(h).parse(int(id), C.GoString(text))
	*out = C.double(v)
	return C.bool(ok)
}

//export ampsimClapSave
func ampsimClapSave(h C.uintptr_t, size *C.size_t) *C.char {
	data, err := clap(h).save()
	if err != nil {
		logf("%v", err)
		return nil
	}
	*size = C.size_t(len(data))
	return (*C.char)(C.CBytes(data))
}

//export ampsimClapLoad
func ampsimClapLoad(h C.uintptr_t, data *C.char, size C.size_t) C.bool {
	if err := clap(h).load(C.GoBytes(unsafe.Pointer(data), C.int(size))); err != nil {
		logf("%v", err)
		return false
	}
	return true
}
