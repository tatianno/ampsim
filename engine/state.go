package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
)

// StateVersion é a versão do formato de preset.
//
//	1: um módulo "amp" único (Fase 1).
//	2: amp dividido em pre/power/cab e a lista de módulos na ordem da cadeia (todos).
//	3: a lista tem só os módulos que estão na cadeia (os outros ficam de fora).
const StateVersion = 3

// State é o estado completo da cadeia: o mesmo JSON serve de preset no app e de state no plugin.
// Na versão 2, Modules vem na ordem da cadeia; um preset com todos os módulos define a ordem.
type State struct {
	Name    string        `json:"name,omitempty"` // nome do preset (opcional)
	Version int           `json:"version"`
	Modules []ModuleState `json:"modules"`
}

// ModuleState é o estado de um módulo.
type ModuleState struct {
	ID      string             `json:"id"`
	Enabled bool               `json:"enabled"`
	Params  map[string]float64 `json:"params"`
	Extra   map[string]string  `json:"extra,omitempty"`
}

// State captura o estado atual.
func (c *Chain) State() State {
	st := State{Version: StateVersion}
	for _, s := range *c.want.Load() {
		ms := ModuleState{ID: s.m.ID(), Enabled: s.enabled.Load(), Params: map[string]float64{}}
		for _, p := range s.m.Params() {
			ms.Params[p.Info().ID] = p.Get()
		}
		if ex, ok := s.m.(ExtraState); ok {
			ms.Extra = ex.ExtraState()
		}
		st.Modules = append(st.Modules, ms)
	}
	return st
}

// ApplyState aplica um estado. Parâmetros ausentes ficam como estão; módulos ou
// parâmetros desconhecidos geram erro (mas o resto é aplicado).
//
// A cadeia passa a ter os módulos do estado, na ordem dele (e as partes do amp, se faltarem).
// Num estado da versão 2, que listava todos os módulos, ficam na cadeia só os ligados: os
// desligados não mudavam o som.
func (c *Chain) ApplyState(st State) error {
	var errs []error
	if st.Version > StateVersion {
		errs = append(errs, fmt.Errorf("preset versão %d é mais nova que a suportada (%d)", st.Version, StateVersion))
	}
	v1 := st.Version < 2
	if v1 {
		st = migrateV1(st)
	}
	var ids []string
	for _, ms := range st.Modules {
		s := c.byID[ms.ID]
		if s == nil {
			errs = append(errs, fmt.Errorf("módulo desconhecido: %q", ms.ID))
			continue
		}
		s.enabled.Store(ms.Enabled)
		for id, v := range ms.Params {
			p := s.m.Param(id)
			if p == nil {
				errs = append(errs, fmt.Errorf("%s: parâmetro desconhecido %q", ms.ID, id))
				continue
			}
			p.Set(v)
		}
		if ex, ok := s.m.(ExtraState); ok && ms.Extra != nil {
			if err := ex.ApplyExtraState(ms.Extra); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", ms.ID, err))
			}
		}
		if (st.Version >= 3 || ms.Enabled || Required(ms.ID)) && !slices.Contains(ids, ms.ID) {
			ids = append(ids, ms.ID)
		}
	}
	if v1 { // a versão 1 não tinha ordem: cada um na posição padrão
		all := c.allIDs()
		slices.SortFunc(ids, func(a, b string) int { return slices.Index(all, a) - slices.Index(all, b) })
	}
	if len(st.Modules) > 0 {
		for _, r := range []string{PreID, PowerID, CabID} {
			if c.byID[r] != nil && !slices.Contains(ids, r) {
				ids = insertDefault(ids, r, c.allIDs())
			}
		}
		if err := c.SetOrder(ids); err != nil {
			errs = append(errs, fmt.Errorf("ordem do preset: %w", err))
		}
	}
	return errors.Join(errs...)
}

// migrateV1 converte o módulo "amp" único da versão 1 em pre + power + cab.
func migrateV1(st State) State {
	var out []ModuleState
	for _, ms := range st.Modules {
		if ms.ID != "amp" {
			out = append(out, ms)
			continue
		}
		pre := ModuleState{ID: PreID, Enabled: ms.Enabled, Params: map[string]float64{}}
		pow := ModuleState{ID: PowerID, Enabled: ms.Enabled, Params: map[string]float64{}}
		cab := ModuleState{ID: CabID, Enabled: ms.Enabled, Params: map[string]float64{}, Extra: ms.Extra}
		for k, v := range ms.Params {
			switch k {
			case "presence", "volume":
				pow.Params[k] = v
			default:
				pre.Params[k] = v
			}
		}
		out = append(out, pre, pow, cab)
	}
	st.Modules = out
	st.Version = 2
	return st
}

// SaveState grava o estado num arquivo JSON.
func (c *Chain) SaveState(path string) error {
	b, err := json.MarshalIndent(c.State(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// LoadState lê e aplica um preset JSON.
func (c *Chain) LoadState(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return c.ApplyState(st)
}
