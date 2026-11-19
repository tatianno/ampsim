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

// FactoryBankSize é quantos presets de fábrica há em cada banco (os footswitches da pedaleira).
const FactoryBankSize = 6

// FactoryBanks são os temas dos bancos de fábrica, na ordem de Factory.
var FactoryBanks = []string{"Limpos", "Drive e rock", "Efeitos e texturas", "Amps clássicos"}

// Factory devolve os presets de fábrica. Cada um escolhe o amp (uma captura NAM incluída
// ou o amp simulado), o gabinete (uma IR incluída) e os pedais. Os volumes (power.volume)
// foram acertados com a DI de teste para −18 dBFS RMS, sem passar de −1 dBFS de pico: trocar
// de preset não dá salto de volume. Os limpos e dinâmicos (Clean, Funk, Country) ficam ~2 dB
// abaixo em RMS, limitados pelo pico (veja TestFabricaNoMesmoVolume).
//
// Os EQs (Worship, Country, Solo Hard Rock, Metal) vêm do teste com guitarra ao vivo (19/11):
// menos agudos acima de ~5 kHz (as capturas são só do pré e soavam brilhantes) e mais corpo
// entre 150 e 800 Hz, com o level do EQ compensando o volume.
//
// A ordem é a da pedaleira padrão (FactoryBankSize por banco): um tema por banco (FactoryBanks),
// com o ganho subindo dentro do banco.
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
		// capturas de pedal (drive NAM); drive/tone/modo dos presets ficam com o ajuste do
		// modelado equivalente, para comparar trocando o Tipo
		sd1     = "SD-1 Oliver SD-10 D10 T6 L10 (Cranked)"
		tsLight = "TS7 TS9 D3 T7 L5 (Light Drive, Bright)"
		tsBoost = "TS7 TS9 D0 T0 L5 (Clean Boost)"
	)
	// mk monta o preset: amp = nome de uma captura NAM (ou simulado), ir = IR incluída e, se
	// houver, pedal = captura de pedal do drive (liga o tipo NAM do drive).
	mk := func(name, amp, ir string, on []string, params map[string]set, pedal ...string) Preset {
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
		if len(pedal) > 0 {
			c.Drive().Param("type").Set(engine.DriveNAM)
		}
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
			case "drive":
				if len(pedal) > 0 {
					st.Modules[i].Extra = map[string]string{"nam": capturas.Prefix + pedal[0]}
				}
			}
		}
		return Preset{Name: name, Factory: true, State: st}
	}
	return []Preset{
		// ——— Banco 1: limpos ———
		mk("Clean", cleanV10, "2x12 SM57", []string{"gate", "comp", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -30, "ratio": 4, "attack": 5, "release": 200, "level": 4},
			engine.PowerID: {"volume": -10.4},
			"reverb":       {"size": 55, "damping": 40, "mix": 18},
		}),
		mk("Country", cleanV10, "2x12 SM57", []string{"gate", "comp", "eq", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -34, "ratio": 8, "attack": 1, "release": 150, "level": 7},
			"eq":           {"g1": -2, "f2": 140, "g2": 3, "q2": 0.5, "f3": 570, "g3": 3, "q3": 0.6, "f5": 5500, "g5": -8, "level": -3},
			engine.PowerID: {"volume": -6.2},
			"delay":        {"time": 110, "feedback": 0, "tone": 70, "mix": 30}, // slapback
			"reverb":       {"size": 35, "damping": 40, "mix": 12},
		}),
		mk("Funk", cleanV7G, "2x12 SM57", []string{"gate", "comp", "wah", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -34, "ratio": 8, "attack": 1, "release": 120, "level": 8},
			"wah":          {"mode": 0, "sens": 70, "range": 55, "q": 4, "response": 120},
			engine.PowerID: {"volume": -11.1},
			"reverb":       {"size": 30, "damping": 50, "mix": 12},
		}),
		mk("Surf", simulado, "2x12 SM57", []string{"gate", "tremolo", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			engine.PreID:   {"gain": 3, "bass": 8.7, "mid": 4, "treble": 7, "stack": engine.StackAmerican},
			engine.PowerID: {"presence": 6, "volume": -4.9},
			"tremolo":      {"rate": 6, "depth": 45, "shape": 0},
			"reverb":       {"size": 85, "damping": 20, "mix": 45},
		}),
		// estilo Andy Summers (The Police): Tele num Marshall quase limpo, chorus largo e profundo e
		// eco de fita em semínima (~150 BPM), arpejos com as notas soltas soando juntas
		mk("New Wave", cleanV10G, "4x12 SM7", []string{"gate", "comp", "chorus", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -26, "ratio": 3, "attack": 20, "release": 200, "level": 4},
			"chorus":       {"rate": 0.7, "depth": 3.5, "delay": 14, "mix": 50},
			engine.PowerID: {"volume": -5.8},
			"delay":        {"time": 400, "feedback": 35, "tone": 50, "mix": 25},
			"reverb":       {"size": 40, "damping": 45, "mix": 12},
		}),
		// estilo pop/rock sueco dos anos 80 (Roxette): limpo comprimido e brilhante, chorus médio,
		// delay curto e reverb de sala grande
		mk("Pop Anos 80", cleanV10, "2x12 SM57", []string{"gate", "comp", "chorus", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"comp":         {"threshold": -32, "ratio": 5, "attack": 8, "release": 180, "level": 6},
			"chorus":       {"rate": 1.1, "depth": 2.2, "delay": 20, "mix": 40},
			engine.PowerID: {"volume": -6.0},
			"delay":        {"time": 300, "feedback": 25, "tone": 60, "mix": 18},
			"reverb":       {"size": 75, "damping": 35, "mix": 26},
		}),
		// ——— Banco 2: drive e rock, do menos para o mais saturado ———
		mk("Blues", cleanV10G, "1x10 SM58", []string{"gate", "drive", "reverb"}, map[string]set{
			"gate":         {"threshold": -60},
			"drive":        {"mode": 1, "drive": 4, "tone": 6, "level": 0},
			engine.PowerID: {"volume": -15.8},
			"reverb":       {"size": 55, "damping": 35, "mix": 22},
		}, tsLight),
		mk("Crunch", sat3, "2x12 SM57", []string{"gate", "reverb"}, map[string]set{
			"gate":         {"threshold": -50},
			engine.PowerID: {"volume": -12.0},
			"reverb":       {"size": 50, "damping": 40, "mix": 14},
		}),
		// overdrive estilo SD-1 todo aberto (captura NAM do Oliver SD-10) empurrando o amp com crunch
		mk("Rock Clássico", sat3MD, "4x12 SM7", []string{"gate", "drive", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -50},
			"drive":        {"level": 0},
			engine.PowerID: {"volume": -10.7},
			"delay":        {"time": 320, "feedback": 15, "tone": 60, "mix": 12},
			"reverb":       {"size": 45, "damping": 40, "mix": 12},
		}, sd1),
		// estilo Brian May (Queen, solo de Bohemian Rhapsody): treble booster (Boost com tone alto)
		// empurrando um AC30 2x12; médios "cantados" da Red Special, delay e sala
		mk("Treble Booster", sat3MD, "2x12 SM57", []string{"gate", "drive", "eq", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -48, "release": 120},
			"drive":        {"mode": 0, "drive": 9, "tone": 8}, // Boost: +18 dB
			"eq":           {"f1": 150, "g1": -4, "f3": 1200, "g3": 3, "q3": 0.8, "f5": 5500, "g5": -6},
			engine.PowerID: {"volume": -14.1},
			"delay":        {"time": 350, "feedback": 25, "tone": 55, "mix": 18},
			"reverb":       {"size": 55, "damping": 40, "mix": 16},
		}),
		// estilo Slash: Les Paul num Marshall modificado, médios na frente, delay no solo
		mk("Solo Hard Rock", sat7MD, "4x12 SM7", []string{"gate", "drive", "eq", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -45, "release": 120},
			"drive":        {"mode": 1, "drive": 2, "tone": 6, "level": -3.5},
			"eq":           {"g1": -1, "f2": 240, "g2": 4.5, "q2": 0.6, "f3": 820, "g3": 5, "q3": 0.5, "f4": 2400, "g4": -2, "q4": 0.5, "f5": 5500, "g5": -10, "level": -2.5},
			engine.PowerID: {"volume": -7.0},
			"delay":        {"time": 430, "feedback": 30, "tone": 60, "mix": 22},
			"reverb":       {"size": 60, "damping": 40, "mix": 15},
		}, tsBoost),
		// o clássico do metal: Tube Screamer de boost (pouco drive, muito level) num amp moderno de
		// alto ganho (a voz High Gain do amp simulado, ajustada medindo um 5150III). O boost fica no
		// modelado de propósito: ele aperta o grave (−9 dB em 80 Hz) e realça 2–3 kHz antes do amp;
		// as capturas do TS7 com pouco drive deixam o grave passar
		mk("Metal", simulado, "4x12 SM7", []string{"gate", "drive", "eq", "reverb"}, map[string]set{
			"gate":         {"threshold": -42, "release": 40},
			engine.PreID:   {"stack": engine.StackHighGain, "gain": 8, "bass": 9.3, "mid": 5, "treble": 5},
			"drive":        {"mode": 1, "drive": 0.5, "tone": 7, "level": 6},
			"eq":           {"f1": 90, "g1": 2, "f2": 120, "g2": 1.5, "q2": 0.6, "f3": 680, "g3": -6, "q3": 0.8, "f5": 5500, "g5": -4, "level": 1.4},
			engine.PowerID: {"volume": -7.7},
			"reverb":       {"size": 40, "damping": 50, "mix": 10},
		}),
		// ——— Banco 3: efeitos e texturas ———
		mk("Psicodélico", cleanV10G, "4x12 SM7", []string{"gate", "drive", "vibe", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -55},
			"drive":        {"mode": 3, "drive": 7, "level": 0}, // Fuzz Face
			"vibe":         {"rate": 1.2, "intensity": 85, "mode": 0},
			engine.PowerID: {"volume": -12.5},
			"delay":        {"time": 400, "feedback": 35, "tone": 45, "mix": 22},
			"reverb":       {"size": 60, "damping": 40, "mix": 20},
		}),
		// estilo Jimi Hendrix (solo de Purple Haze): Octavia = oitava acima entrando no fuzz, num
		// Marshall aberto; cru, só um reverb curto (o Psicodélico é o lado Uni-Vibe)
		mk("Fuzz Oitavado", sat3MD, "4x12 SM7", []string{"gate", "octave", "drive", "reverb"}, map[string]set{
			"gate":         {"threshold": -50, "release": 100},
			"octave":       {"sub1": 0, "sub2": 0, "up": 60, "dry": 100},
			"drive":        {"mode": 3, "drive": 7, "level": 0}, // Fuzz Face
			engine.PowerID: {"volume": -11.4},
			"reverb":       {"size": 35, "damping": 45, "mix": 12},
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
			"drive":        {"mode": 1, "drive": 3, "tone": 6, "level": -3},
			"eq":           {"f1": 100, "g1": -2, "f2": 180, "g2": 5, "q2": 0.6, "f3": 700, "g3": 4, "q3": 0.6, "f5": 4500, "g5": -10, "level": -3.7},
			"pitch":        {"voice1": 12, "level2": 0, "mix": 12},
			engine.PowerID: {"volume": -15.0},
			"delay":        {"time": 375, "feedback": 45, "tone": 55, "mix": 30},
			"reverb":       {"size": 92, "damping": 30, "mix": 40},
		}, tsLight),
		mk("Ambiente", simulado, "2x12 SM57", []string{"gate", "comp", "chorus", "delay", "reverb"}, map[string]set{
			"gate":         {"threshold": -55},
			"comp":         {"threshold": -30, "ratio": 4, "attack": 30, "level": 7},
			engine.PreID:   {"gain": 3, "bass": 8.7, "mid": 4.5, "treble": 6.5, "stack": engine.StackAmerican},
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
		// ——— Banco 4: amps clássicos ———
		// só o amp simulado e o gabinete (e o gate nos de ganho alto): a base para montar o resto
		// com os pedais (no plugin, os "AmpSim Pedal"). O grave fica no ponto neutro de cada tone
		// stack (o antigo 5, veja TestFabricaGraveTMBComoAntes); o caráter vem do médio, do agudo,
		// do ganho e do gabinete.
		//
		// o limpo cristalino dos combos americanos dos anos 60 (blackface): médios cavados
		mk("Americano Limpo", simulado, "2x12 SM57", nil, map[string]set{
			engine.PreID:   {"stack": engine.StackAmerican, "gain": 2.5, "bass": 8.72, "mid": 3, "treble": 7},
			engine.PowerID: {"presence": 6, "volume": -5.1},
		}),
		// combo pequeno no talo (tweed): satura quando a palheta pesa, limpa no volume da guitarra
		mk("Americano Quebrando", simulado, "1x10 SM58", nil, map[string]set{
			engine.PreID:   {"stack": engine.StackAmerican, "gain": 10, "bass": 8.72, "mid": 6, "treble": 6},
			engine.PowerID: {"presence": 5, "volume": -11.2},
		}),
		// o crunch dos cabeçotes britânicos dos anos 60 e 70 (plexi): médios na frente
		mk("Britânico Crunch", simulado, "4x12 SM7", nil, map[string]set{
			engine.PreID:   {"stack": engine.StackBritish, "gain": 10, "bass": 9.01, "mid": 6.5, "treble": 6},
			engine.PowerID: {"presence": 6, "volume": -12.0},
		}),
		// o britânico dos anos 80, mais quente (hard rock): a voz Britânico Mod (um britânico com
		// um estágio de ganho a mais, como os cabeçotes modificados). As vozes Americano e
		// Britânico saturam suave (THD de ~7% no ganho 10 com uma senoide no nível da DI); a Mod
		// chega a ~19% no ganho 8, perto do Solo Hard Rock
		mk("Britânico Saturado", simulado, "4x12 SM7", []string{"gate"}, map[string]set{
			"gate":         {"threshold": -48, "release": 100},
			engine.PreID:   {"stack": engine.StackBritishMod, "gain": 8, "bass": 9.01, "mid": 7, "treble": 6.5},
			engine.PowerID: {"presence": 7, "volume": -10.2},
		}),
		// alto ganho moderno, apertado (anos 90 em diante): a voz do Metal, sem o boost e o EQ
		mk("High Gain Moderno", simulado, "4x12 SM7", []string{"gate"}, map[string]set{
			"gate":         {"threshold": -42, "release": 40},
			engine.PreID:   {"stack": engine.StackHighGain, "gain": 7.5, "bass": 9.3, "mid": 4.5, "treble": 5.5},
			engine.PowerID: {"presence": 6, "volume": -9.6},
		}),
		// limpo neutro e encorpado (o tone stack Moderno, de três bandas independentes): jazz
		mk("Moderno Limpo", simulado, "2x12 SM57", nil, map[string]set{
			engine.PreID:   {"stack": engine.StackModern, "gain": 2, "bass": 5.5, "mid": 5, "treble": 4.5},
			engine.PowerID: {"presence": 4, "volume": -8.7},
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
