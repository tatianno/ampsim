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

// Factory devolve os presets de fábrica. Os volumes foram acertados para todos ficarem
// perto de −17 dBFS RMS com a DI de teste (trocar de preset não dá salto de volume).
func Factory() []Preset {
	type set map[string]float64
	mk := func(name string, on []string, params map[string]set) Preset {
		c := engine.NewDefaultChain()
		for _, m := range c.Modules() {
			c.SetEnabled(m.ID(), false)
		}
		for _, id := range append([]string{engine.PreID, engine.PowerID, engine.CabID}, on...) {
			c.SetEnabled(id, true)
		}
		for mod, ps := range params {
			for id, v := range ps {
				c.Module(mod).Param(id).Set(v)
			}
		}
		st := c.State()
		st.Name = name
		for i := range st.Modules {
			st.Modules[i].Extra = nil // sem IR: mantém o gabinete em uso
		}
		return Preset{Name: name, Factory: true, State: st}
	}
	return []Preset{
		mk("Clean", []string{"gate", "comp", "chorus", "reverb"}, map[string]set{
			"gate":         {"threshold": -55},
			"comp":         {"threshold": -28, "ratio": 3, "attack": 25, "release": 200, "level": 6},
			engine.PreID:   {"gain": 2, "bass": 5.5, "mid": 5, "treble": 6},
			engine.PowerID: {"presence": 5.5, "volume": -4},
			"chorus":       {"rate": 0.8, "depth": 1.5, "mix": 30},
			"reverb":       {"size": 60, "damping": 40, "mix": 20},
		}),
		mk("Crunch", []string{"gate", "reverb"}, map[string]set{
			"gate":         {"threshold": -45},
			engine.PreID:   {"gain": 6.5, "bass": 6, "mid": 6.5, "treble": 5.5},
			engine.PowerID: {"presence": 6, "volume": -14.5},
			"reverb":       {"size": 55, "mix": 15},
		}),
		mk("Lead", []string{"gate", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -42, "release": 150},
			engine.PreID:   {"gain": 9, "bass": 5.5, "mid": 7, "treble": 6},
			engine.PowerID: {"presence": 6.5, "volume": -15},
			"delay":        {"time": 420, "feedback": 30, "tone": 60, "mix": 25},
			"reverb":       {"size": 70, "mix": 18},
		}),
		mk("Ambiente", []string{"gate", "comp", "chorus", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -55},
			"comp":         {"threshold": -30, "ratio": 4, "attack": 30, "level": 7},
			engine.PreID:   {"gain": 3, "bass": 5, "mid": 4.5, "treble": 6.5},
			engine.PowerID: {"presence": 6, "volume": -3},
			"chorus":       {"rate": 0.5, "depth": 2.5, "mix": 40},
			"delay":        {"time": 500, "feedback": 50, "tone": 50, "mix": 35},
			"reverb":       {"size": 90, "damping": 30, "mix": 40},
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
