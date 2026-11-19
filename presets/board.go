package presets

import "path/filepath"

// BoardFile é o pedaleira.json: a configuração da pedaleira de presets do app.
type BoardFile struct {
	Slots []string `json:"slots"`           // nome do preset de cada footswitch ("" = vazio), banco a banco
	Banks []string `json:"banks,omitempty"` // nome de cada banco ("" = sem nome)
	// Spillover: as caudas de delay e reverb continuam na troca de preset (ausente = ligado)
	Spillover *bool `json:"spillover,omitempty"`
}

// BoardPath devolve o pedaleira.json ao lado da pasta de presets dir ("" sem pasta).
func BoardPath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), "pedaleira.json")
}
