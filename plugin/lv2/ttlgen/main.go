// Comando ttlgen grava os .ttl do bundle LV2 (manifest.ttl e ampsim.ttl) na pasta -out, com
// os presets de fábrica (o pacote); o app acrescenta os do usuário no bundle da pasta pessoal.
// Com -usuario, inclui também os presets do usuário (o build.sh --install usa).
package main

import (
	"flag"
	"log"

	"github.com/tatianno/ampsim/plugin"
	"github.com/tatianno/ampsim/plugin/lv2/ttl"
	"github.com/tatianno/ampsim/presets"
)

func main() {
	out := flag.String("out", ".", "pasta do bundle (ampsim.lv2)")
	user := flag.Bool("usuario", false, "inclui os presets do usuário (~/.config/ampsim/presets)")
	flag.Parse()
	dir := ""
	if *user {
		dir, _ = presets.Dir()
	}
	ps, err := plugin.Presets(dir)
	if err != nil {
		log.Print(err)
	}
	if err := ttl.WriteBundle(*out, ps); err != nil {
		log.Fatal(err)
	}
}
