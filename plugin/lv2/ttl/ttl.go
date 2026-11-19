// Pacote ttl descreve os plugins LV2 (URIs e portas), gera os .ttl do bundle e mantém o bundle
// do usuário (~/.lv2/ampsim.lv2) em dia com os presets. É a única fonte do layout das portas:
// o .so (plugin/lv2) e os .ttl usam estas mesmas funções.
//
// Cada preset é um plugin LV2 ("AmpSim: Clean"), e cada pedal ou modulação também ("AmpSim
// Pedal: Chorus", com os knobs dele): o Audacity mostra cada um no menu de efeitos, e a lista
// sai em ordem. Nada de lista de opções (lv2:enumeration): a lilv embaralha a ordem delas e o
// Audacity mostra a opção errada ao reabrir. Seletores viram caixa de marcar (duas posições)
// ou número com a legenda no nome. A lista de presets muda quando o usuário salva ou apaga um
// preset; por isso o app reescreve os .ttl (Sync) e o bundle fica na pasta pessoal.
package ttl

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/tatianno/ampsim/plugin"
)

// URIBase é a base das URIs dos plugins.
const URIBase = "https://github.com/tatianno/ampsim"

// StateKey é a chave do estado salvo no projeto (atom:String com o JSON de plugin.SaveState).
const StateKey = URIBase + "#estado"

// Binary é o nome do .so dentro do bundle.
const Binary = "ampsim.so"

// SystemLibrary é onde o .deb instala o .so; o bundle do usuário aponta para ele (link).
var SystemLibrary = "/usr/lib/ampsim/" + Binary

// pedalPrefix é o começo do ID (depois do #) dos plugins de pedal.
const pedalPrefix = "pedal-"

// Port é uma porta de controle (entrada).
type Port struct {
	Symbol, Name, Unit string // Unit: a da tabela de parâmetros ("dB", "ms", "Hz", "%"…)
	Min, Max, Default  float64
	Integer, Toggle    bool
	Log                bool
	Enabled            bool     // designação lv2:enabled (o liga/desliga)
	Points             []string // nomes dos valores Min, Min+1… (sem lv2:enumeration)
}

// Plugin é um plugin do .so: um preset ou um pedal.
type Plugin struct {
	ID, Name, Comment string
	Controls          []Port // as entradas de controle, na ordem das portas
	Pedal             string // id do pedal ("" = preset)
	Preset            plugin.Preset
}

// URI devolve a URI do plugin.
func (p Plugin) URI() string { return URIBase + "#" + p.ID }

// Layout das portas: as entradas de controle, a latência, a entrada e a saída de áudio (mono).
func (p Plugin) PortLatency() int { return len(p.Controls) }
func (p Plugin) PortIn() int      { return len(p.Controls) + 1 }
func (p Plugin) PortOut() int     { return len(p.Controls) + 2 }
func (p Plugin) NumPorts() int    { return len(p.Controls) + 3 }

// ID devolve o ID de uma URI de plugin (ok = false se não for do AmpSim).
func ID(uri string) (string, bool) {
	return strings.CutPrefix(uri, URIBase+"#")
}

// Plugins devolve todos os plugins: os pedais e os presets ps.
func Plugins(ps []plugin.Preset) []Plugin {
	var out []Plugin
	for _, p := range plugin.Pedals() {
		out = append(out, PedalPlugin(p))
	}
	for _, p := range ps {
		out = append(out, PresetPlugin(p))
	}
	return out
}

// PresetPlugin devolve o plugin do preset p.
func PresetPlugin(p plugin.Preset) Plugin {
	kind := "de fábrica"
	if !p.Factory {
		kind = "do usuário"
	}
	var ctl []Port
	for _, c := range plugin.Controls {
		q := Port{Symbol: c.Symbol, Name: c.Name, Min: c.Min, Max: c.Max, Default: c.Default}
		switch c.Kind {
		case plugin.KindDB:
			q.Unit = "dB"
		case plugin.KindToggle:
			q.Toggle, q.Enabled = true, c.Symbol == "ligado"
		}
		ctl = append(ctl, q)
	}
	return Plugin{
		ID: p.ID, Name: "AmpSim: " + p.Name, Preset: p, Controls: ctl,
		Comment: fmt.Sprintf("Aplica o preset %s %q do AmpSim (amp, gabinete e pedais) à faixa. "+
			"O preset é editado no app AmpSim.", kind, p.Name),
	}
}

// PedalPlugin devolve o plugin do pedal p: os knobs e o liga/desliga.
func PedalPlugin(p plugin.Pedal) Plugin {
	var ctl []Port
	for i, info := range p.Params {
		q := Port{Symbol: info.ID, Name: p.Label(i), Unit: info.Unit, Min: info.Min, Max: info.Max, Default: info.Default, Log: info.Log}
		switch {
		case plugin.Toggle(info):
			q.Toggle = true
		case info.Step > 0: // passos inteiros (1, ou 2 nos estágios do phaser: o módulo arredonda)
			q.Integer = true
			if len(info.Choices) > 0 && info.Step == 1 {
				q.Points = info.Choices
			}
		}
		ctl = append(ctl, q)
	}
	ctl = append(ctl, Port{Symbol: "ligado", Name: "Ligado", Min: 0, Max: 1, Default: 1, Toggle: true, Enabled: true})
	return Plugin{
		ID: pedalPrefix + p.ID, Name: "AmpSim Pedal: " + p.Name, Pedal: p.ID, Controls: ctl,
		Comment: fmt.Sprintf("O pedal %s do AmpSim, com os knobs do app.", p.Name),
	}
}

// Find devolve o plugin da URI uri, com a lista de presets atual (ps). ok = false se não existe.
func Find(uri string, ps []plugin.Preset) (Plugin, bool) {
	id, ok := ID(uri)
	if !ok {
		return Plugin{}, false
	}
	if pid, ok := strings.CutPrefix(id, pedalPrefix); ok {
		p, ok := plugin.FindPedal(pid)
		if !ok {
			return Plugin{}, false
		}
		return PedalPlugin(p), true
	}
	for _, p := range ps {
		if p.ID == id {
			return PresetPlugin(p), true
		}
	}
	return Plugin{}, false
}

// Manifest devolve o manifest.ttl do bundle com os plugins pl.
func Manifest(pl []Plugin) string {
	var b strings.Builder
	b.WriteString("@prefix lv2: <http://lv2plug.in/ns/lv2core#> .\n")
	b.WriteString("@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .\n\n")
	for _, p := range pl {
		fmt.Fprintf(&b, "<%s>\n\ta lv2:Plugin ;\n\tlv2:binary <%s> ;\n\trdfs:seeAlso <ampsim.ttl> .\n\n", p.URI(), Binary)
	}
	return b.String()
}

// Describe devolve o ampsim.ttl: a descrição dos plugins pl.
func Describe(pl []Plugin) string {
	var b strings.Builder
	b.WriteString(`@prefix doap: <http://usefulinc.com/ns/doap#> .
@prefix foaf: <http://xmlns.com/foaf/0.1/> .
@prefix lv2: <http://lv2plug.in/ns/lv2core#> .
@prefix pprops: <http://lv2plug.in/ns/ext/port-props#> .
@prefix rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix state: <http://lv2plug.in/ns/ext/state#> .
@prefix units: <http://lv2plug.in/ns/extensions/units#> .
@prefix urid: <http://lv2plug.in/ns/ext/urid#> .

`)
	for _, p := range pl {
		fmt.Fprintf(&b, "<%s>\n", p.URI())
		class := "lv2:SimulatorPlugin"
		if p.Pedal != "" {
			class = pedalClass[p.Pedal]
			if class == "" {
				class = "lv2:UtilityPlugin"
			}
		}
		fmt.Fprintf(&b, "\ta lv2:Plugin, %s ;\n", class)
		fmt.Fprintf(&b, "\tdoap:name %s ;\n", lit(p.Name))
		b.WriteString("\tdoap:license <http://opensource.org/licenses/MIT> ;\n")
		// o autor: o Audacity mostra "n/d" no fornecedor sem ele
		fmt.Fprintf(&b, "\tdoap:maintainer [ foaf:name %s ; foaf:homepage <%s> ] ;\n", lit("AmpSim"), URIBase)
		fmt.Fprintf(&b, "\trdfs:comment %s ;\n", lit(p.Comment))
		if p.Pedal == "" { // o estado (a cópia do preset) só existe nos presets
			b.WriteString("\tlv2:optionalFeature urid:map ;\n")
			b.WriteString("\tlv2:extensionData state:interface ;\n")
		}
		var ports []string
		for i, c := range p.Controls {
			ports = append(ports, controlPort(i, c))
		}
		ports = append(ports, fmt.Sprintf(`[
		a lv2:OutputPort, lv2:ControlPort ;
		lv2:index %d ;
		lv2:symbol "latencia" ;
		lv2:name "Latência" ;
		lv2:portProperty lv2:reportsLatency, lv2:integer ;
		lv2:designation lv2:latency ;
		lv2:minimum 0 ;
		lv2:maximum 16384 ;
		units:unit units:frame
	]`, p.PortLatency()),
			fmt.Sprintf("[\n\t\ta lv2:AudioPort, lv2:InputPort ;\n\t\tlv2:index %d ;\n\t\tlv2:symbol \"in\" ;\n\t\tlv2:name \"Entrada\"\n\t]", p.PortIn()),
			fmt.Sprintf("[\n\t\ta lv2:AudioPort, lv2:OutputPort ;\n\t\tlv2:index %d ;\n\t\tlv2:symbol \"out\" ;\n\t\tlv2:name \"Saída\"\n\t]", p.PortOut()))
		fmt.Fprintf(&b, "\tlv2:port %s .\n\n", strings.Join(ports, " , "))
	}
	return b.String()
}

// pedalClass é a classe LV2 de cada pedal (os hosts agrupam por ela).
var pedalClass = map[string]string{
	"gate": "lv2:GatePlugin", "comp": "lv2:CompressorPlugin", "wah": "lv2:FilterPlugin",
	"octave": "lv2:PitchPlugin", "synth": "lv2:FilterPlugin", "drive": "lv2:DistortionPlugin",
	"eq": "lv2:ParaEQPlugin", "chorus": "lv2:ChorusPlugin", "flanger": "lv2:FlangerPlugin",
	"phaser": "lv2:PhaserPlugin", "vibe": "lv2:PhaserPlugin", "vibrato": "lv2:ModulatorPlugin",
	"tremolo": "lv2:ModulatorPlugin", "rotary": "lv2:ModulatorPlugin", "pitch": "lv2:PitchPlugin",
	"delay": "lv2:DelayPlugin", "reverb": "lv2:ReverbPlugin",
}

// units são as unidades LV2 das unidades da tabela de parâmetros.
var units = map[string]string{
	"dB": "units:db", "ms": "units:ms", "Hz": "units:hz", "%": "units:pc",
	"st": "units:semitone12TET", "cents": "units:cent", "oct": "units:oct",
}

func controlPort(i int, c Port) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[\n\t\ta lv2:InputPort, lv2:ControlPort ;\n\t\tlv2:index %d ;\n", i)
	fmt.Fprintf(&b, "\t\tlv2:symbol %s ;\n\t\tlv2:name %s ;\n", lit(c.Symbol), lit(c.Name))
	fmt.Fprintf(&b, "\t\tlv2:default %s ;\n\t\tlv2:minimum %s ;\n\t\tlv2:maximum %s", num(c.Default), num(c.Min), num(c.Max))
	var props []string
	switch {
	case c.Toggle:
		props = append(props, "lv2:toggled")
	case c.Integer:
		props = append(props, "lv2:integer")
	}
	if c.Log {
		props = append(props, "pprops:logarithmic")
	}
	if len(props) > 0 {
		fmt.Fprintf(&b, " ;\n\t\tlv2:portProperty %s", strings.Join(props, ", "))
	}
	if c.Enabled {
		b.WriteString(" ;\n\t\tlv2:designation lv2:enabled")
	}
	if u := units[c.Unit]; u != "" {
		fmt.Fprintf(&b, " ;\n\t\tunits:unit %s", u)
	}
	if len(c.Points) > 0 {
		var sp []string
		for k, name := range c.Points {
			sp = append(sp, fmt.Sprintf("[ rdfs:label %s ; rdf:value %s ]", lit(name), num(c.Min+float64(k))))
		}
		fmt.Fprintf(&b, " ;\n\t\tlv2:scalePoint %s", strings.Join(sp, " , "))
	}
	b.WriteString("\n\t]")
	return b.String()
}

// lit escreve uma string literal do Turtle.
func lit(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

// num escreve um número do Turtle (decimal, para o tipo não variar com o valor).
func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(s, ".") && !strings.ContainsAny(s, "eE") {
		s += ".0"
	}
	return s
}

// WriteBundle grava os .ttl dos plugins pl na pasta dir (que já tem, ou terá, o .so). Só
// regrava o arquivo que mudou, e por troca atômica (o host nunca lê um arquivo pela metade).
func WriteBundle(dir string, pl []Plugin) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range []struct{ name, text string }{
		{"ampsim.ttl", Describe(pl)}, // a descrição antes do manifest que aponta para ela
		{"manifest.ttl", Manifest(pl)},
	} {
		path := filepath.Join(dir, f.name)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, []byte(f.text)) {
			continue
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(f.text), 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return nil
}

// UserBundle devolve o bundle do usuário (~/.lv2/ampsim.lv2), onde os hosts procuram.
func UserBundle() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lv2", "ampsim.lv2"), nil
}

var syncMu sync.Mutex

// Sync deixa o bundle do usuário com os pedais e os presets ps. Com o .so instalado no bundle (pelo
// install.sh do .tar.gz ou pelo build.sh), só reescreve os .ttl. Sem ele, e com o .so do
// .deb (SystemLibrary), cria o bundle com um link para o .so. Sem nenhum dos dois, não faz
// nada (o plugin não está instalado). Se o link aponta para um .so que sumiu (o .deb foi
// removido), apaga o bundle. Seguro para chamar de várias goroutines.
func Sync(ps []plugin.Preset) error {
	syncMu.Lock()
	defer syncMu.Unlock()
	dir, err := UserBundle()
	if err != nil {
		return err
	}
	so := filepath.Join(dir, Binary)
	lst, lerr := os.Lstat(so)
	_, serr := os.Stat(so) // segue o link
	switch {
	case serr == nil: // .so no bundle (arquivo ou link válido)
	case lerr == nil && lst.Mode()&os.ModeSymlink != 0: // link quebrado
		if _, err := os.Stat(SystemLibrary); err != nil {
			return removeBundle(dir)
		}
		if err := os.Remove(so); err != nil {
			return err
		}
		fallthrough
	case errors.Is(lerr, os.ErrNotExist):
		if _, err := os.Stat(SystemLibrary); err != nil {
			return nil // plugin não instalado
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.Symlink(SystemLibrary, so); err != nil {
			return err
		}
	default:
		return lerr
	}
	return WriteBundle(dir, Plugins(ps))
}

// removeBundle apaga o bundle criado por Sync (só os arquivos dele).
func removeBundle(dir string) error {
	for _, f := range []string{Binary, "manifest.ttl", "ampsim.ttl"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Remove(dir)
}
