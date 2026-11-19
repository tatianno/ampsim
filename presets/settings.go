package presets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Settings são os ajustes do app que não fazem parte do som de um preset (dependem do
// equipamento: fones, uma caixa só...). Ficam em ajustes.json, ao lado da pasta de presets.
type Settings struct {
	Stereo *bool `json:"estereo,omitempty"` // saída estéreo (ausente = ligada)
}

// StereoOn informa se a saída estéreo está ligada (o padrão é ligada).
func (s Settings) StereoOn() bool { return s.Stereo == nil || *s.Stereo }

// SettingsPath devolve o ajustes.json ao lado da pasta de presets dir ("" sem pasta).
func SettingsPath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), "ajustes.json")
}

// LoadSettings lê os ajustes; sem o arquivo, devolve os padrões (sem erro).
func LoadSettings(path string) (Settings, error) {
	var s Settings
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Save grava os ajustes em path ("" = não grava).
func (s Settings) Save(path string) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
