package engine

import (
	"math"
	"sync/atomic"
)

// Calibração de entrada dos modelos NAM, como no plugin oficial: o .nam pode trazer em
// "input_level_dbu" o nível em dBu que dava 0 dBFS na placa usada na captura. Sabendo o
// nível da placa de quem toca (dBu em 0 dBFS, o "max input level" do manual), o mesmo sinal
// elétrico da guitarra chega ao modelo com o nível que ele viu no treino:
//
//	ganho (dB) = nível da placa − input_level_dbu do modelo
//
// Ex.: placa com 0 dBFS = +12 dBu e captura feita a +13 dBu: o modelo recebe −1 dB. Modelos
// sem o metadado (como as capturas incluídas) não mudam. É ajuste do equipamento, não do som:
// fica fora do preset (no app, em ajustes.json).

// DefaultInputLevelDBu é o nível da placa até o usuário informar o dele (padrão do plugin).
const DefaultInputLevelDBu = 12.0

// Limites do nível da placa aceitos (dBu em 0 dBFS).
const (
	MinInputLevelDBu = -20.0
	MaxInputLevelDBu = 40.0
)

// CalibrationFadeMs é a rampa do ganho de calibração (ligar, mudar o nível, trocar de modelo).
const CalibrationFadeMs = 20.0

// inputCal é a calibração da cadeia, lida pelos blocos NAM na thread de áudio.
type inputCal struct {
	on  atomic.Bool
	dbu atomic.Uint64 // float64
}

func (c *inputCal) set(on bool, dbu float64) {
	if math.IsNaN(dbu) {
		dbu = DefaultInputLevelDBu
	}
	dbu = math.Max(MinInputLevelDBu, math.Min(MaxInputLevelDBu, dbu))
	c.dbu.Store(math.Float64bits(dbu))
	c.on.Store(on)
}

func (c *inputCal) level() float64 { return math.Float64frombits(c.dbu.Load()) }

// offsetDB devolve o ajuste (dB) para um modelo com nível de entrada model (nil = sem metadado).
func (c *inputCal) offsetDB(model *float64) float64 {
	if c == nil || model == nil || !c.on.Load() {
		return 0
	}
	return c.level() - *model
}

// calUser é implementado pelos módulos que rodam modelos NAM (pré-amp e Drive).
type calUser interface {
	useCal(*inputCal)
}

// SetInputCalibration liga ou desliga a calibração de entrada dos modelos NAM, com o nível da
// placa em dBu para 0 dBFS (limitado a MinInputLevelDBu..MaxInputLevelDBu). Seguro com o áudio
// rodando (rampa de CalibrationFadeMs).
func (c *Chain) SetInputCalibration(on bool, dbu float64) { c.cal.set(on, dbu) }

// InputCalibration informa se a calibração está ligada e o nível da placa (dBu).
func (c *Chain) InputCalibration() (on bool, dbu float64) { return c.cal.on.Load(), c.cal.level() }

func (m *Pre) useCal(c *inputCal)   { m.nam.cal = c }
func (m *Drive) useCal(c *inputCal) { m.nam.cal = c }

// NAMInputOffset devolve o ajuste de calibração (dB) do modelo NAM do pré-amp e se ele vale
// (o modelo tem input_level_dbu e a calibração está ligada).
func (m *Pre) NAMInputOffset() (db float64, ok bool) { return m.nam.calOffset() }

// NAMInputOffset devolve o ajuste de calibração (dB) da captura do Drive (veja Pre.NAMInputOffset).
func (m *Drive) NAMInputOffset() (db float64, ok bool) { return m.nam.calOffset() }
