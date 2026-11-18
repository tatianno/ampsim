// Pacote presets cuida dos presets de fábrica e dos salvos pelo usuário.
//
// Um preset é um engine.State em JSON (o mesmo formato do -preset do ampcli), com o
// nome dentro do arquivo. Os de fábrica são gerados em código a partir da cadeia
// padrão, então sempre trazem todos os módulos e parâmetros; eles não guardam IR
// (o gabinete em uso continua).
package presets

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/tatianno/ampsim/capturas"
	"github.com/tatianno/ampsim/engine"
)

// Preset é um preset carregável.
type Preset struct {
	Name    string
	Factory bool
	Path    string // só para presets do usuário
	State   engine.State
}

// Dir devolve a pasta dos presets do usuário (~/.config/ampsim/presets no Linux).
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ampsim", "presets"), nil
}

// Factory devolve os presets de fábrica. Cada um escolhe o amp (uma captura NAM incluída
// ou o amp simulado), o gabinete (uma IR incluída) e os pedais. Os volumes (power.volume)
// foram acertados com a DI de teste para −18 dBFS RMS, sem passar de −1 dBFS de pico: trocar
// de preset não dá salto de volume. Os limpos e dinâmicos (Clean, Funk, Country) ficam ~2 dB
// abaixo em RMS, limitados pelo pico (veja TestFabricaNoMesmoVolume).
func Factory() []Preset {
	type set map[string]float64
	const (
		simulado  = ""
		cleanV10  = "Clean V10 B5 M5 T5 P5"
		cleanV10G = "Clean V10 G B5 M5 T5 P5"
		cleanV7G  = "Clean V7 G B5 M5 T5 P5"
		sat3      = "Dist Sat3 V4 B5 M5 T5 P5"
		sat3MD    = "Dist Sat3 V4 MD B5 M5 T5 P5"
		sat7MD    = "Dist Sat7 V4 MD B5 M5 T5 P5"
	)
	// mk monta o preset: amp = nome de uma captura NAM (ou simulado), ir = IR incluída.
	mk := func(name, amp, ir string, on []string, params map[string]set) Preset {
		c := engine.NewDefaultChain()
		var ids []string // na cadeia: o amp e os pedais do preset, na ordem padrão
		for _, id := range engine.DefaultOrder {
			if engine.Required(id) || slices.Contains(on, id) {
				ids = append(ids, id)
				c.SetEnabled(id, true)
			}
		}
		if err := c.SetOrder(ids); err != nil {
			panic(err) // preset de fábrica mal escrito
		}
		mode := float64(engine.AmpSimulated)
		if amp != simulado {
			mode = engine.AmpNAM
		}
		c.Module(engine.PreID).Param("amp").Set(mode)
		for mod, ps := range params {
			for id, v := range ps {
				p := c.Module(mod).Param(id)
				if p == nil {
					panic(fmt.Sprintf("preset %s: %s.%s não existe", name, mod, id))
				}
				p.Set(v)
			}
		}
		st := c.State()
		st.Name = name
		for i, m := range st.Modules {
			st.Modules[i].Extra = nil
			switch m.ID {
			case engine.PreID:
				if amp != simulado {
					st.Modules[i].Extra = map[string]string{"nam": capturas.Prefix + amp}
				}
			case engine.CabID:
				st.Modules[i].Extra = map[string]string{"ir": capturas.Prefix + ir}
			}
		}
		return Preset{Name: name, Factory: true, State: st}
	}
	return []Preset{
		mk("Clean", cleanV10, "2x12 SM57", []string{"gate", "comp", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -30, "ratio": 4, "attack": 5, "release": 200, "level": 4},
			engine.PowerID: {"volume": -10.4},
			"reverb":       {"size": 55, "damping": 40, "mix": 18},
		}),
		mk("Crunch", sat3, "2x12 SM57", []string{"gate", "reverb"}, map[string]set{
			"gate":         {"threshold": -50},
			engine.PowerID: {"volume": -12.0},
			"reverb":       {"size": 50, "damping": 40, "mix": 14},
		}),
		mk("Blues", cleanV10G, "1x10 SM58", []string{"gate", "drive", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"drive":        {"mode": 1, "drive": 4, "tone": 6, "level": 3},
			engine.PowerID: {"volume": -17.0},
			"reverb":       {"size": 55, "damping": 35, "mix": 22},
		}),
		mk("Rock Clássico", sat3MD, "4x12 SM7", []string{"gate", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -50},
			engine.PowerID: {"volume": -8.0},
			"delay":        {"time": 320, "feedback": 15, "tone": 60, "mix": 12},
			"reverb":       {"size": 45, "damping": 40, "mix": 12},
		}),
		// estilo Slash: Les Paul num Marshall modificado, médios na frente, delay no solo
		mk("Solo Hard Rock", sat7MD, "4x12 SM7", []string{"gate", "drive", "eq", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -45, "release": 120},
			"drive":        {"mode": 1, "drive": 2, "tone": 6, "level": 3},
			"eq":           {"f3": 800, "g3": 3, "q3": 1, "g1": -1},
			engine.PowerID: {"volume": -6.5},
			"delay":        {"time": 430, "feedback": 30, "tone": 60, "mix": 22},
			"reverb":       {"size": 60, "damping": 40, "mix": 15},
		}),
		// o clássico do metal: Tube Screamer de boost (pouco drive, muito level) no amp saturado
		mk("Metal", sat7MD, "4x12 SM7", []string{"gate", "drive", "eq", "reverb"}, map[string]set{
			"gate":         {"threshold": -42, "release": 40},
			"drive":        {"mode": 1, "drive": 0.5, "tone": 7, "level": 6},
			"eq":           {"f1": 90, "g1": 2, "f3": 700, "g3": -3, "q3": 0.8, "g5": 1},
			engine.PowerID: {"volume": -5.4},
			"reverb":       {"size": 40, "damping": 50, "mix": 10},
		}),
		mk("Funk", cleanV7G, "2x12 SM57", []string{"gate", "comp", "wah", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -34, "ratio": 8, "attack": 1, "release": 120, "level": 8},
			"wah":          {"mode": 0, "sens": 70, "range": 55, "q": 4, "response": 120},
			engine.PowerID: {"volume": -11.1},
			"reverb":       {"size": 30, "damping": 50, "mix": 12},
		}),
		mk("Country", cleanV10, "2x12 SM57", []string{"gate", "comp", "eq", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -34, "ratio": 8, "attack": 1, "release": 150, "level": 7},
			"eq":           {"g1": -2, "f5": 5000, "g5": 1.5},
			engine.PowerID: {"volume": -6.2},
			"delay":        {"time": 110, "feedback": 0, "tone": 70, "mix": 30}, // slapback
			"reverb":       {"size": 35, "damping": 40, "mix": 12},
		}),
		mk("Surf", simulado, "2x12 SM57", []string{"gate", "tremolo", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			engine.PreID:   {"gain": 3, "bass": 5, "mid": 4, "treble": 7, "stack": engine.StackAmerican},
			engine.PowerID: {"presence": 6, "volume": -4.9},
			"tremolo":      {"rate": 6, "depth": 45, "shape": 0},
			"reverb":       {"size": 85, "damping": 20, "mix": 45},
		}),
		mk("Psicodélico", cleanV10G, "4x12 SM7", []string{"gate", "drive", "vibe", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -55},
			"drive":        {"mode": 3, "drive": 7, "tone": 5, "level": 0},
			"vibe":         {"rate": 1.2, "intensity": 85, "mode": 0},
			engine.PowerID: {"volume": -10.2},
			"delay":        {"time": 400, "feedback": 35, "tone": 45, "mix": 22},
			"reverb":       {"size": 60, "damping": 40, "mix": 20},
		}),
		mk("Modulação", cleanV10, "2x12 SM57", []string{"gate", "comp", "chorus", "tremolo", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -28, "ratio": 3, "attack": 25, "level": 4},
			"chorus":       {"rate": 0.9, "depth": 2, "delay": 20, "mix": 35},
			"tremolo":      {"rate": 3.5, "depth": 30, "shape": 1},
			engine.PowerID: {"volume": -6.4},
			"delay":        {"time": 380, "feedback": 30, "tone": 55, "mix": 20},
			"reverb":       {"size": 60, "damping": 40, "mix": 22},
		}),
		// delay em colcheia pontuada (375 ms = 120 BPM) e um "shimmer" (+1 oitava suave antes
		// do delay e do reverb)
		mk("Worship", cleanV10G, "2x12 SM57", []string{"gate", "comp", "drive", "eq", "pitch", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -28, "ratio": 3, "attack": 30, "level": 5},
			"drive":        {"mode": 1, "drive": 3, "tone": 6, "level": 1},
			"eq":           {"f1": 100, "g1": -2, "g5": 2},
			"pitch":        {"voice1": 12, "level2": 0, "mix": 12},
			engine.PowerID: {"volume": -15.8},
			"delay":        {"time": 375, "feedback": 45, "tone": 55, "mix": 30},
			"reverb":       {"size": 92, "damping": 30, "mix": 40},
		}),
		mk("Ambiente", simulado, "2x12 SM57", []string{"gate", "comp", "chorus", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -55},
			"comp":         {"threshold": -30, "ratio": 4, "attack": 30, "level": 7},
			engine.PreID:   {"gain": 3, "bass": 5, "mid": 4.5, "treble": 6.5, "stack": engine.StackAmerican},
			engine.PowerID: {"presence": 6, "volume": -1.9},
			"chorus":       {"rate": 0.5, "depth": 2.5, "mix": 40},
			"delay":        {"time": 500, "feedback": 50, "tone": 50, "mix": 35},
			"reverb":       {"size": 90, "damping": 30, "mix": 40},
		}),
		// oitavador entrando no filtro de sintetizador em sequência (1/16 a 120 BPM)
		mk("Synth", cleanV10, "2x12 SM57", []string{"gate", "octave", "synth", "delay"}, map[string]set{
			"gate":         {"threshold": -55},
			"octave":       {"sub1": 70, "dry": 50},
			"synth":        {"mode": 2, "type": 0, "cutoff": 250, "res": 70, "depth": 3, "rate": 8, "glide": 20},
			engine.PowerID: {"volume": -14.4},
			"delay":        {"time": 375, "feedback": 30, "tone": 50, "mix": 18},
		}),
	}
}

// List devolve os de fábrica seguidos dos do usuário (em ordem alfabética).
// Arquivos inválidos são ignorados; o erro (se houver) vem junto, sem impedir o resto.
func List(dir string) ([]Preset, error) {
	out := Factory()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var user []Preset
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		p, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		user = append(user, p)
	}
	slices.SortFunc(user, func(a, b Preset) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return append(out, user...), errors.Join(errs...)
}

// Load lê um preset do usuário.
func Load(path string) (Preset, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Preset{}, err
	}
	var st engine.State
	if err := json.Unmarshal(b, &st); err != nil {
		return Preset{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	name := st.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return Preset{Name: name, Path: path, State: st}, nil
}

// FileName converte um nome em nome de arquivo seguro ("Meu Lead!" → "meu-lead.json").
func FileName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteRune('-')
			dash = true
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if s == "" {
		s = "preset"
	}
	return s + ".json"
}

// ErrFactoryName indica que o nome é de um preset de fábrica.
var ErrFactoryName = errors.New("esse nome é de um preset de fábrica; escolha outro")

// Save grava o estado como preset do usuário com o nome dado (substitui se já existir).
func Save(dir, name string, st engine.State) (Preset, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Preset{}, errors.New("o preset precisa de um nome")
	}
	for _, f := range Factory() {
		if strings.EqualFold(f.Name, name) {
			return Preset{}, ErrFactoryName
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Preset{}, err
	}
	st.Name = name
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return Preset{}, err
	}
	path := filepath.Join(dir, FileName(name))
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return Preset{}, err
	}
	return Preset{Name: name, Path: path, State: st}, nil
}

// Delete apaga um preset do usuário.
func Delete(p Preset) error {
	if p.Factory || p.Path == "" {
		return errors.New("presets de fábrica não podem ser apagados")
	}
	return os.Remove(p.Path)
}

// Find procura um preset pelo nome (sem diferenciar maiúsculas).
func Find(list []Preset, name string) (Preset, bool) {
	for _, p := range list {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Preset{}, false
}

// Equal diz se dois estados são o mesmo som: mesma ordem, mesmos módulos ligados e
// mesmos parâmetros (o arquivo de IR é ignorado). Usado para marcar "modificado".
func Equal(a, b engine.State) bool {
	if len(a.Modules) != len(b.Modules) {
		return false
	}
	for i := range a.Modules {
		ma, mb := a.Modules[i], b.Modules[i]
		if ma.ID != mb.ID || ma.Enabled != mb.Enabled || len(ma.Params) != len(mb.Params) {
			return false
		}
		for k, v := range ma.Params {
			w, ok := mb.Params[k]
			if !ok || math.Abs(v-w) > 1e-6 {
				return false
			}
		}
	}
	return true
}

// Resolve aceita um caminho de arquivo .json ou o nome de um preset (de fábrica ou do
// usuário) e devolve o estado.
func Resolve(nameOrPath string) (engine.State, error) {
	if _, err := os.Stat(nameOrPath); err == nil {
		p, err := Load(nameOrPath)
		return p.State, err
	}
	dir, _ := Dir()
	list, _ := List(dir)
	if p, ok := Find(list, nameOrPath); ok {
		return p.State, nil
	}
	return engine.State{}, fmt.Errorf("preset %q não encontrado (nem arquivo, nem nome)", nameOrPath)
}
