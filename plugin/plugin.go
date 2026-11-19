// Pacote plugin é o núcleo do plugin AmpSim, independente do formato (LV2, CLAP): aplica um
// preset da app a uma faixa, com trim de entrada, volume de saída e liga/desliga. O som não
// é editado no plugin; a edição fica na app. As camadas de formato (plugin/lv2) só traduzem
// portas e estado.
//
// No LV2 cada preset é um plugin (o Audacity mostra no menu de efeitos, pelo nome): a
// instância nasce com o preset dela (New) e não troca de preset. Presets lista quais são.
//
// Threads: Process roda na thread de áudio do host e não aloca nem bloqueia. New e
// RestoreState montam a cadeia (leem arquivos, carregam capturas NAM) e rodam fora dela.
//
// Estado: SaveState guarda uma cópia do preset (o JSON completo). RestoreState usa essa
// cópia, não o preset atual da app: o projeto do host soa igual mesmo que o preset seja
// editado depois. (O Audacity 3.4 não usa o estado dos plugins; lá vale o preset atual.)
package plugin

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/tatianno/ampsim/dsp"
	"github.com/tatianno/ampsim/engine"
	"github.com/tatianno/ampsim/presets"
)

// MaxBlock é o bloco com que a cadeia é preparada; blocos maiores do host são fatiados.
const MaxBlock = 512

// maxLatency é o maior atraso do sinal seco (amostras) usado no liga/desliga.
const maxLatency = 1 << 14

// Tempos das rampas (ms).
const (
	gainFadeMs   = 20
	enableFadeMs = 20
)

// Kind é o tipo de um controle.
type Kind int

const (
	KindDB     Kind = iota // ganho em dB
	KindToggle             // liga/desliga (0 ou 1)
)

// Control descreve um controle exposto ao host. A ordem de Controls é a ordem das portas/IDs.
type Control struct {
	Symbol, Name      string
	Kind              Kind
	Min, Max, Default float64
}

// Índices dos controles em Controls.
const (
	CtlInput = iota
	CtlOutput
	CtlEnabled
	NumControls
)

// Controls são os controles do plugin, na ordem de CtlInput…CtlEnabled.
var Controls = [NumControls]Control{
	{Symbol: "entrada", Name: "Entrada", Kind: KindDB, Min: -24, Max: 24, Default: 0},
	{Symbol: "saida", Name: "Saída", Kind: KindDB, Min: -24, Max: 24, Default: 0},
	{Symbol: "ligado", Name: "Ligado", Kind: KindToggle, Min: 0, Max: 1, Default: 1},
}

// Preset é um preset oferecido pelo plugin.
type Preset struct {
	// ID é estável (vira a URI do plugin LV2): "fabrica-<nome>" ou "usuario-<arquivo>", com
	// o nome do arquivo do preset (presets.FileName) escapado para URI.
	ID      string
	Name    string
	Factory bool
	path    string       // preset do usuário: relido ao criar a instância
	state   engine.State // de fábrica
}

// State devolve o estado do preset (o do usuário é relido do arquivo, na versão atual).
func (p Preset) State() (engine.State, error) {
	if p.Factory {
		return p.state, nil
	}
	q, err := presets.Load(p.path)
	return q.State, err
}

// FromList converte a lista da app (presets.List: os de fábrica e os do usuário).
func FromList(list []presets.Preset) []Preset {
	out := make([]Preset, 0, len(list))
	for _, p := range list {
		slug := url.PathEscape(strings.TrimSuffix(presets.FileName(p.Name), ".json"))
		q := Preset{Name: p.Name, Factory: p.Factory, path: p.Path, state: p.State}
		if p.Factory {
			q.ID = "fabrica-" + slug
		} else {
			q.ID = "usuario-" + slug
		}
		out = append(out, q)
	}
	return out
}

// Presets devolve os presets do plugin: os de fábrica e os do usuário em dir (presets.Dir();
// "" = só os de fábrica). Presets do usuário ilegíveis ficam de fora (o erro diz quais).
func Presets(dir string) ([]Preset, error) {
	if dir == "" {
		return FromList(presets.Factory()), nil
	}
	list, err := presets.List(dir)
	return FromList(list), err
}

// Instance é uma instância do plugin.
type Instance struct {
	fs     float64
	stereo bool
	chain  *engine.Chain

	mu   sync.Mutex    // snap (SaveState pode rodar junto com Process)
	snap *engine.State // cópia do preset em uso (nil = sem preset: só o sinal seco)

	// pedal (NewPedal): os knobs do módulo, na ordem de Pedal.Params, e o último valor aplicado
	pedal  bool
	params []*engine.Param
	last   []float64

	// thread de áudio
	inG, outG  dsp.Smoother
	dryG       dsp.Smoother // 1 = só o sinal seco (desligado)
	mono, rght []float64
	dry        [2]*dsp.DelayLine
}

// New cria uma instância à taxa fs com o preset st (nil = sem preset: só o sinal seco);
// stereo = saída em dois canais (senão, mono). Monta a cadeia: não chamar na thread de áudio.
func New(fs float64, stereo bool, st *engine.State) *Instance {
	in := newInstance(fs, stereo)
	in.use(st)
	return in
}

// newInstance cria a instância sem cadeia.
func newInstance(fs float64, stereo bool) *Instance {
	in := &Instance{
		fs: fs, stereo: stereo,
		inG:  dsp.NewSmoother(fs, gainFadeMs),
		outG: dsp.NewSmoother(fs, gainFadeMs),
		dryG: dsp.NewSmoother(fs, enableFadeMs),
		mono: make([]float64, MaxBlock), rght: make([]float64, MaxBlock),
	}
	for i := range in.dry {
		in.dry[i] = dsp.NewDelayLine(maxLatency + 1)
	}
	return in
}

// use passa a tocar st (nil = só o sinal seco), numa cadeia nova.
func (in *Instance) use(st *engine.State) {
	c := engine.NewDefaultChain()
	c.SetStereo(in.stereo)
	if st != nil {
		if err := c.ApplyState(*st); err != nil {
			logf("preset %q: %v", st.Name, err)
		}
	}
	c.Prepare(in.fs, MaxBlock)
	in.chain = c
	in.mu.Lock()
	in.snap = st
	in.mu.Unlock()
}

// Latency devolve a latência em amostras (a da cadeia do preset).
func (in *Instance) Latency() int { return min(in.chain.Latency(), maxLatency) }

// Settings são os valores dos controles num bloco.
type Settings struct {
	InputDB  float64
	OutputDB float64
	Enabled  bool
	Params   []float64 // pedal: os knobs, na ordem de Pedal.Params (valores fora da faixa são limitados)
}

// Process processa n amostras: src tem 1 ou mais canais (misturados em mono na entrada da
// cadeia); dst tem 1 canal (mono) ou 2 (estéreo). Thread de áudio: não aloca nem bloqueia.
func (in *Instance) Process(src, dst [][]float32, n int, s Settings) {
	for i, v := range s.Params[:min(len(s.Params), len(in.params))] {
		if v != in.last[i] {
			in.params[i].Set(v)
			in.last[i] = v
		}
	}
	in.inG.SetTarget(dbToLin(s.InputDB))
	in.outG.SetTarget(dbToLin(s.OutputDB))
	dry := 0.0
	if !s.Enabled || (!in.pedal && in.snap == nil) {
		dry = 1
	}
	in.dryG.SetTarget(dry)

	c := in.chain
	lat := min(c.Latency(), maxLatency)
	for off := 0; off < n; off += MaxBlock {
		k := min(MaxBlock, n-off)
		m, r := in.mono[:k], in.rght[:k]
		inv := 0.0
		if len(src) > 0 {
			inv = 1 / float64(len(src))
		}
		for i := range m {
			x := 0.0
			for _, ch := range src {
				x += float64(ch[off+i])
			}
			m[i] = x * inv * in.inG.Next()
		}
		if len(dst) >= 2 {
			c.ProcessStereo(m, r)
		} else {
			c.ProcessMono(m)
			r = m
		}
		for i := range m {
			g, d := in.outG.Next(), in.dryG.Next()
			for j, ch := range dst {
				wet := m[i]
				if j == 1 {
					wet = r[i]
				}
				var x float64 // sinal seco do canal j, com o atraso da cadeia
				if len(src) > 0 {
					x = float64(src[min(j, len(src)-1)][off+i])
				}
				dl := in.dry[min(j, 1)]
				dl.Write(x)
				ch[off+i] = float32(wet*g*(1-d) + dl.ReadInt(lat+1)*d)
			}
		}
	}
}

// savedState é o estado salvo no projeto do host.
type savedState struct {
	Version int           `json:"versao"`
	Preset  *engine.State `json:"preset,omitempty"` // nil = sem preset
}

const savedVersion = 1

// SaveState devolve o estado da instância (JSON com a cópia do preset; nil num pedal, que
// não tem estado além dos knobs, salvos pelo host). Pode ser chamado de qualquer thread, com
// o áudio rodando.
func (in *Instance) SaveState() ([]byte, error) {
	if in.pedal {
		return nil, nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return json.Marshal(savedState{Version: savedVersion, Preset: in.snap})
}

// RestoreState volta ao estado salvo por SaveState, com a cópia do preset. Monta a cadeia:
// não pode rodar junto com Process (no LV2, o host garante isso).
func (in *Instance) RestoreState(data []byte) error {
	if in.pedal {
		return nil
	}
	var s savedState
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("estado do plugin: %w", err)
	}
	if s.Version > savedVersion {
		return fmt.Errorf("estado do plugin versão %d é mais nova que a suportada (%d)", s.Version, savedVersion)
	}
	in.use(s.Preset)
	return nil
}

func dbToLin(db float64) float64 { return math.Pow(10, db/20) }

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ampsim: "+format+"\n", args...)
}
