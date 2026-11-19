// Pacote ttl descreve o plugin LV2 (URIs e portas), gera os .ttl do bundle e mantém o bundle
// do usuário (~/.lv2/ampsim.lv2) em dia com os presets. É a única fonte do layout das portas:
// o .so (plugin/lv2) e os .ttl usam estas mesmas funções.
//
// Cada preset é um plugin LV2 ("AmpSim: Clean"): o Audacity mostra cada um no menu de efeitos,
// e a lista sai em ordem (numa lista de opções dentro de um plugin, a lilv embaralha a ordem).
// A lista muda quando o usuário salva ou apaga um preset; por isso o app reescreve os .ttl
// (Sync) e o bundle fica na pasta pessoal.
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

// URI devolve a URI do plugin do preset p.
func URI(p plugin.Preset) string { return URIBase + "#" + p.ID }

// ID devolve o ID do preset de uma URI de plugin (ok = false se não for do AmpSim).
func ID(uri string) (string, bool) {
	return strings.CutPrefix(uri, URIBase+"#")
}

// Name devolve o nome do plugin do preset p (no menu do host).
func Name(p plugin.Preset) string { return "AmpSim: " + p.Name }

// Layout das portas (mono): os controles de plugin.Controls (na ordem), a latência, a entrada
// e a saída de áudio.
const (
	PortLatency = plugin.NumControls
	PortIn      = PortLatency + 1
	PortOut     = PortIn + 1
	NumPorts    = PortOut + 1
)

// Manifest devolve o manifest.ttl do bundle com os plugins ps.
func Manifest(ps []plugin.Preset) string {
	var b strings.Builder
	b.WriteString("@prefix lv2: <http://lv2plug.in/ns/lv2core#> .\n")
	b.WriteString("@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .\n\n")
	for _, p := range ps {
		fmt.Fprintf(&b, "<%s>\n\ta lv2:Plugin ;\n\tlv2:binary <%s> ;\n\trdfs:seeAlso <ampsim.ttl> .\n\n", URI(p), Binary)
	}
	return b.String()
}

// Plugins devolve o ampsim.ttl: a descrição dos plugins ps (todos com as mesmas portas).
func Plugins(ps []plugin.Preset) string {
	var b strings.Builder
	b.WriteString(`@prefix doap: <http://usefulinc.com/ns/doap#> .
@prefix foaf: <http://xmlns.com/foaf/0.1/> .
@prefix lv2: <http://lv2plug.in/ns/lv2core#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix state: <http://lv2plug.in/ns/ext/state#> .
@prefix units: <http://lv2plug.in/ns/extensions/units#> .
@prefix urid: <http://lv2plug.in/ns/ext/urid#> .

`)
	ports := portsTTL()
	for _, p := range ps {
		fmt.Fprintf(&b, "<%s>\n", URI(p))
		b.WriteString("\ta lv2:Plugin, lv2:SimulatorPlugin ;\n")
		fmt.Fprintf(&b, "\tdoap:name %s ;\n", lit(Name(p)))
		b.WriteString("\tdoap:license <http://opensource.org/licenses/MIT> ;\n")
		// o autor: o Audacity mostra "n/d" no fornecedor sem ele
		fmt.Fprintf(&b, "\tdoap:maintainer [ foaf:name %s ; foaf:homepage <%s> ] ;\n", lit("AmpSim"), URIBase)
		kind := "de fábrica"
		if !p.Factory {
			kind = "do usuário"
		}
		fmt.Fprintf(&b, "\trdfs:comment %s ;\n", lit(fmt.Sprintf("Aplica o preset %s %q do AmpSim (amp, gabinete e pedais) à faixa. "+
			"O preset é editado no app AmpSim.", kind, p.Name)))
		b.WriteString("\tlv2:optionalFeature urid:map ;\n")
		b.WriteString("\tlv2:extensionData state:interface ;\n")
		fmt.Fprintf(&b, "\tlv2:port %s .\n\n", ports)
	}
	return b.String()
}

// portsTTL devolve as portas (iguais em todos os plugins), como nós anônimos do Turtle.
func portsTTL() string {
	var ports []string
	for i, c := range plugin.Controls {
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
	]`, PortLatency),
		fmt.Sprintf("[\n\t\ta lv2:AudioPort, lv2:InputPort ;\n\t\tlv2:index %d ;\n\t\tlv2:symbol \"in\" ;\n\t\tlv2:name \"Entrada\"\n\t]", PortIn),
		fmt.Sprintf("[\n\t\ta lv2:AudioPort, lv2:OutputPort ;\n\t\tlv2:index %d ;\n\t\tlv2:symbol \"out\" ;\n\t\tlv2:name \"Saída\"\n\t]", PortOut))
	return strings.Join(ports, " , ")
}

func controlPort(i int, c plugin.Control) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[\n\t\ta lv2:InputPort, lv2:ControlPort ;\n\t\tlv2:index %d ;\n", i)
	fmt.Fprintf(&b, "\t\tlv2:symbol %s ;\n\t\tlv2:name %s ;\n", lit(c.Symbol), lit(c.Name))
	fmt.Fprintf(&b, "\t\tlv2:default %s ;\n\t\tlv2:minimum %s ;\n\t\tlv2:maximum %s", num(c.Default), num(c.Min), num(c.Max))
	switch c.Kind {
	case plugin.KindDB:
		b.WriteString(" ;\n\t\tunits:unit units:db")
	case plugin.KindToggle:
		b.WriteString(" ;\n\t\tlv2:portProperty lv2:toggled")
		if c.Symbol == "ligado" {
			b.WriteString(" ;\n\t\tlv2:designation lv2:enabled")
		}
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
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// WriteBundle grava os .ttl dos plugins ps na pasta dir (que já tem, ou terá, o .so). Só
// regrava o arquivo que mudou, e por troca atômica (o host nunca lê um arquivo pela metade).
func WriteBundle(dir string, ps []plugin.Preset) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range []struct{ name, text string }{
		{"ampsim.ttl", Plugins(ps)}, // a descrição antes do manifest que aponta para ela
		{"manifest.ttl", Manifest(ps)},
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

// Sync deixa o bundle do usuário com os plugins ps. Com o .so instalado no bundle (pelo
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
	return WriteBundle(dir, ps)
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
