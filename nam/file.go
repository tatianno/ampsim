// Pacote nam lê e executa modelos do Neural Amp Modeler (arquivos .nam) em Go puro.
//
// O arquivo é um JSON com a arquitetura, a configuração, o vetor de pesos, a taxa de
// amostragem e metadados. Hoje são suportados:
//
//   - WaveNet, no formato clássico (A1: "standard", "lite", "feather", "nano") e no novo (A2:
//     kernel por camada, bottleneck, layer1x1/head1x1, cabeça com kernel > 1, LeakyReLU etc.);
//   - SlimmableContainer: vários WaveNets do mesmo som, em tamanhos diferentes (ex.: um
//     "lite" de 3 canais e um completo de 8), escolhidos por um valor 0..1.
//
// Ainda não: LSTM, ConvNet, Linear, FiLM, condition_dsp e WaveNet "slimmable" por fatia de canais.
//
// A semântica segue a NeuralAmpModelerCore (C++), a inferência usada pelo plugin oficial.
package nam

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
)

// Metadata são os metadados do .nam (todos opcionais).
type Metadata struct {
	Name      string `json:"name"`
	ModeledBy string `json:"modeled_by"`
	GearType  string `json:"gear_type"`
	GearMake  string `json:"gear_make"`
	GearModel string `json:"gear_model"`
	ToneType  string `json:"tone_type"`
	Trainer   string `json:"trainer"`
	// Loudness é o volume médio da saída do modelo (dB) num sinal de referência; o plugin
	// oficial usa para normalizar a saída.
	Loudness *float64 `json:"loudness"`
	// Gain é uma estimativa (0..1) do quanto o som é saturado.
	Gain *float64 `json:"gain"`
	// InputLevelDBu é o nível em dBu que corresponde a 0 dBFS na entrada da captura.
	InputLevelDBu *float64 `json:"input_level_dbu"`
	// OutputLevelDBu é o nível em dBu que corresponde a 0 dBFS na saída da captura.
	OutputLevelDBu *float64 `json:"output_level_dbu"`
}

// fileJSON é o .nam como vem do disco (também cada submodelo de um container).
type fileJSON struct {
	Version      string          `json:"version"`
	Architecture string          `json:"architecture"`
	Config       json.RawMessage `json:"config"`
	Weights      []float32       `json:"weights"`
	SampleRate   *float64        `json:"sample_rate"`
	Metadata     Metadata        `json:"metadata"`
}

// Submodel é uma das redes de um modelo. MaxValue é o limite (exclusivo) do valor de
// tamanho 0..1 que escolhe esta rede (a última cobre o resto).
type Submodel struct {
	MaxValue float64
	Spec     *Spec
}

// Model é um arquivo .nam carregado e validado (ainda sem estado de áudio).
type Model struct {
	Version      string
	Architecture string
	// SampleRate é a taxa em que o modelo foi treinado (0 = desconhecida; na prática 48 kHz).
	SampleRate float64
	Metadata   Metadata
	// Submodels vem em ordem crescente de tamanho; um WaveNet simples tem um só.
	Submodels []Submodel
}

// Load lê e valida um arquivo .nam.
func Load(path string) (*Model, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Parse lê e valida o conteúdo de um .nam.
func Parse(data []byte) (*Model, error) {
	var f fileJSON
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("JSON inválido: %w", err)
	}
	m := &Model{Version: f.Version, Architecture: f.Architecture, Metadata: f.Metadata}
	if f.SampleRate != nil {
		m.SampleRate = *f.SampleRate
	}
	switch f.Architecture {
	case "WaveNet":
		spec, err := parseWaveNet(f.Config, f.Weights)
		if err != nil {
			return nil, err
		}
		m.Submodels = []Submodel{{MaxValue: 1, Spec: spec}}
	case "SlimmableContainer":
		subs, err := parseContainer(f.Config, m.SampleRate)
		if err != nil {
			return nil, err
		}
		m.Submodels = subs
	case "":
		return nil, errors.New("arquivo sem 'architecture': não parece um .nam")
	case "LSTM", "ConvNet", "Linear":
		return nil, fmt.Errorf("arquitetura %s ainda não suportada (só WaveNet)", f.Architecture)
	default:
		return nil, fmt.Errorf("arquitetura desconhecida: %q", f.Architecture)
	}
	return m, nil
}

func parseContainer(raw json.RawMessage, fs float64) ([]Submodel, error) {
	var cfg struct {
		Submodels []struct {
			MaxValue *float64 `json:"max_value"`
			Model    fileJSON `json:"model"`
		} `json:"submodels"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config do container: %w", err)
	}
	if len(cfg.Submodels) == 0 {
		return nil, errors.New("SlimmableContainer sem submodelos")
	}
	var subs []Submodel
	for i, s := range cfg.Submodels {
		if s.MaxValue == nil {
			return nil, fmt.Errorf("submodelo %d sem max_value", i)
		}
		if s.Model.Architecture != "WaveNet" {
			return nil, fmt.Errorf("submodelo %d: arquitetura %q não suportada (só WaveNet)", i, s.Model.Architecture)
		}
		if sr := s.Model.SampleRate; sr != nil && fs > 0 && *sr != fs {
			return nil, fmt.Errorf("submodelo %d: taxa %g diferente da do container (%g)", i, *sr, fs)
		}
		spec, err := parseWaveNet(s.Model.Config, s.Model.Weights)
		if err != nil {
			return nil, fmt.Errorf("submodelo %d: %w", i, err)
		}
		if i > 0 && *s.MaxValue <= subs[i-1].MaxValue {
			return nil, errors.New("submodelos fora de ordem (max_value precisa crescer)")
		}
		subs = append(subs, Submodel{MaxValue: *s.MaxValue, Spec: spec})
	}
	if subs[len(subs)-1].MaxValue < 1 {
		return nil, errors.New("o último submodelo precisa ter max_value >= 1")
	}
	return subs, nil
}

// SubmodelFor devolve o índice do submodelo escolhido pelo tamanho size (0..1), como no
// plugin oficial: o primeiro com size < MaxValue; o último se nenhum.
func (m *Model) SubmodelFor(size float64) int {
	i := slices.IndexFunc(m.Submodels, func(s Submodel) bool { return size < s.MaxValue })
	if i < 0 {
		return len(m.Submodels) - 1
	}
	return i
}

// Name devolve o nome do modelo (metadata.name), ou "" se não houver.
func (m *Model) Name() string { return m.Metadata.Name }

// NewNet cria uma instância do submodelo escolhido por size (0..1), já aquecida (Prewarm).
func (m *Model) NewNet(size float64, maxBlock int) *Net {
	n := NewNet(m.Submodels[m.SubmodelFor(size)].Spec, maxBlock)
	n.Prewarm()
	return n
}
