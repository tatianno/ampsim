package presets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/tatianno/ampsim/engine"
)

// Settings são os ajustes do app que não fazem parte do som de um preset (dependem do
// equipamento: fones, uma caixa só...). Ficam em ajustes.json, ao lado da pasta de presets.
type Settings struct {
	Stereo  *bool `json:"estereo,omitempty"`   // saída estéreo (ausente = ligada)
	Limiter *bool `json:"limitador,omitempty"` // limitador de segurança na saída (ausente = ligado)
	// Calibração de entrada dos modelos NAM: ligada ou não (ausente = desligada) e o nível da
	// placa em dBu para 0 dBFS (ausente = engine.DefaultInputLevelDBu).
	InputCal      *bool    `json:"calibrar_entrada,omitempty"`
	InputLevelDBu *float64 `json:"nivel_entrada_dbu,omitempty"`
	// Metrônomo: andamento, tempos por compasso (1 = sem acento) e volume do clique (dBFS).
	// Ausentes = os padrões do app; ele sempre abre desligado.
	MetroBPM    *float64 `json:"metronomo_bpm,omitempty"`
	MetroBeats  *float64 `json:"metronomo_compasso,omitempty"`
	MetroVolume *float64 `json:"metronomo_volume_db,omitempty"`
}

// StereoOn informa se a saída estéreo está ligada (o padrão é ligada).
func (s Settings) StereoOn() bool { return s.Stereo == nil || *s.Stereo }

// LimiterOn informa se o limitador de saída está ligado (o padrão é ligado).
func (s Settings) LimiterOn() bool { return s.Limiter == nil || *s.Limiter }

// InputCalOn informa se a calibração de entrada está ligada (o padrão é desligada).
func (s Settings) InputCalOn() bool { return s.InputCal != nil && *s.InputCal }

// InputLevel devolve o nível da placa em dBu para 0 dBFS (padrão: o do plugin, 12 dBu).
func (s Settings) InputLevel() float64 {
	if s.InputLevelDBu == nil {
		return engine.DefaultInputLevelDBu
	}
	return *s.InputLevelDBu
}

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
