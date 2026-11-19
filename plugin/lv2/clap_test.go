package main

import (
	"encoding/json"
	"testing"

	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
)

// Um projeto com um preset que não existe mais: o som vem da cópia e o Preset aponta para a
// "cópia do projeto" (o último valor da lista), com o nome dela.
func TestClapCopiaDoProjeto(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // sem presets do usuário
	c := newClapInst()
	if c.entries() != len(presets.Factory())+1 {
		t.Fatalf("%d valores no Preset", c.entries())
	}
	st := engine.NewDefaultChain().State()
	st.Name = "Meu Som Apagado"
	data, _ := json.Marshal(clapState{Version: 1, Name: st.Name, Preset: &st, InDB: 3, Bypass: true})
	if err := c.load(data); err != nil {
		t.Fatal(err)
	}
	i := int(c.value(clapPreset))
	if i != len(c.list) || c.text(clapPreset, float64(i)) != "Meu Som Apagado (cópia do projeto)" {
		t.Errorf("Preset = %d (%q)", i, c.text(clapPreset, float64(i)))
	}
	if c.value(clapInput) != 3 || c.value(clapBypass) != 1 || c.pending == nil {
		t.Error("controles ou cópia não restaurados")
	}
	c.activate(48000)
	defer c.deactivate()
	if s := c.in.Snapshot(); s == nil || s.Name != "Meu Som Apagado" {
		t.Errorf("activate deveria tocar a cópia: %v", s)
	}
	saved, err := c.save()
	if err != nil {
		t.Fatal(err)
	}
	var back clapState
	if json.Unmarshal(saved, &back) != nil || back.Name != "Meu Som Apagado" || back.Preset == nil {
		t.Errorf("salvar de novo perdeu a cópia: %s", saved)
	}
}

func TestClapTextos(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := newClapInst()
	if v, ok := c.parse(clapPreset, "metal"); !ok || c.text(clapPreset, v) != "Metal" {
		t.Errorf("Metal: %v %v", v, ok)
	}
	if v, ok := c.parse(clapOutput, "-3,5 dB"); !ok || v != -3.5 || c.text(clapOutput, v) != "-3.5 dB" {
		t.Errorf("saída: %v %v", v, ok)
	}
	if v, ok := c.parse(clapBypass, "Bypass"); !ok || v != 1 {
		t.Errorf("bypass: %v %v", v, ok)
	}
	c.setParam(clapPreset, 999) // fora da faixa: limitado
	if int(c.value(clapPreset)) != c.entries()-1 {
		t.Errorf("Preset fora da faixa: %v", c.value(clapPreset))
	}
}
