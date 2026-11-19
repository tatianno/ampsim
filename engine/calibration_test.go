package engine

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tatianno/ampsim/capturas"
)

// namComNivel grava uma cópia de uma captura incluída com input_level_dbu = dbu.
func namComNivel(t *testing.T, name string, dbu float64) string {
	t.Helper()
	data, err := capturas.Data(name)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	meta, _ := f["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["input_level_dbu"] = dbu
	f["metadata"] = meta
	out, _ := json.Marshal(f)
	p := filepath.Join(t.TempDir(), "calibrada.nam")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func cadeiaNAM(t *testing.T, ref string, input float64) *Chain {
	t.Helper()
	c := NewChain(NewPre())
	c.Pre().Param("amp").Set(AmpNAM)
	c.Pre().Param("nam_input").Set(input)
	if err := c.Pre().LoadNAM(ref); err != nil {
		t.Fatal(err)
	}
	return c
}

func diffDB(a, b []float64) float64 {
	var e, r float64
	for i := range a {
		e += (a[i] - b[i]) * (a[i] - b[i])
		r += b[i] * b[i]
	}
	return 10 * math.Log10(e/r)
}

// Calibração ligada com a placa a +12 dBu e a captura a +13 dBu: o mesmo que −1 dB no knob
// Entrada. Desligada, ou num modelo sem o metadado, nada muda.
func TestCalibracaoEntradaNAM(t *testing.T) {
	di := lerDI48(t)[:24000]
	name := "Dist Sat5 V4 B5 M5 T5 P5"
	arq := namComNivel(t, name, 13)
	render := func(c *Chain) []float64 {
		c.Prepare(48000, 256)
		return renderCadeia(c, di, 256)
	}

	ref := render(cadeiaNAM(t, arq, -1)) // −1 dB à mão, sem calibração
	c := cadeiaNAM(t, arq, 0)
	c.SetInputCalibration(true, 12)
	if db, ok := c.Pre().NAMInputOffset(); !ok || db != -1 {
		t.Errorf("ajuste %v %v, esperado −1 dB", db, ok)
	}
	if d := diffDB(render(c), ref); d > -100 {
		t.Errorf("calibrada × −1 dB à mão: %.1f dB", d)
	}
	if lvl := c.Pre().NAMInfo().InputLevelDBu; lvl == nil || *lvl != 13 {
		t.Errorf("NAMInfo.InputLevelDBu = %v", lvl)
	}

	plain := render(cadeiaNAM(t, arq, 0))
	off := cadeiaNAM(t, arq, 0)
	off.SetInputCalibration(false, 12)
	if d := diffDB(render(off), plain); d > -200 {
		t.Errorf("calibração desligada mudou o som: %.1f dB", d)
	}
	if _, ok := off.Pre().NAMInputOffset(); ok {
		t.Error("desligada não deveria informar ajuste")
	}

	// modelo sem input_level_dbu: igual com ou sem calibração
	semNivel := capturas.Prefix + name
	a := render(cadeiaNAM(t, semNivel, 0))
	b := cadeiaNAM(t, semNivel, 0)
	b.SetInputCalibration(true, 3)
	if d := diffDB(render(b), a); d > -200 {
		t.Errorf("modelo sem metadado mudou com a calibração: %.1f dB", d)
	}
	if _, ok := b.Pre().NAMInputOffset(); ok {
		t.Error("sem metadado não deveria informar ajuste")
	}

	// a exportação offline leva a calibração junto
	cc := cadeiaNAM(t, arq, 0)
	cc.SetInputCalibration(true, 12)
	o := OutputOf(cc)
	if !o.InputCal || o.InputLevelDBu != 12 {
		t.Errorf("OutputOf: %+v", o)
	}
}

// O Drive NAM também é calibrado; limites; ligar e desligar com o áudio rodando não aloca e
// não salta (rampa).
func TestCalibracaoDriveERampa(t *testing.T) {
	arq := namComNivel(t, capturas.PedalNames()[0], 6)
	c := NewDefaultChain()
	d := c.Drive()
	d.Param("type").Set(DriveNAM)
	if err := d.LoadNAM(arq); err != nil {
		t.Fatal(err)
	}
	c.SetEnabled("drive", true)
	c.SetInputCalibration(true, 12)
	if db, ok := d.NAMInputOffset(); !ok || db != 6 {
		t.Errorf("drive: ajuste %v %v, esperado +6 dB", db, ok)
	}
	c.SetInputCalibration(true, 1000)
	if _, dbu := c.InputCalibration(); dbu != MaxInputLevelDBu {
		t.Errorf("nível não limitado: %v", dbu)
	}
	c.SetInputCalibration(false, math.NaN())
	if _, dbu := c.InputCalibration(); dbu != DefaultInputLevelDBu {
		t.Errorf("NaN deveria voltar ao padrão: %v", dbu)
	}

	// rampa: um seno constante; ligar a calibração (+6 dB) não pode dar salto na entrada do modelo
	c.Prepare(48000, 256)
	buf := make([]float64, 256)
	k := 0
	if a := testing.AllocsPerRun(60, func() {
		k++
		c.SetInputCalibration(k/10%2 == 0, 12)
		for i := range buf {
			buf[i] = 0.1 * math.Sin(float64(k*256+i)*0.05)
		}
		c.ProcessMono(buf)
		for _, v := range buf {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatal("NaN/Inf")
			}
		}
	}); a != 0 {
		t.Errorf("aloca %.0f vezes por bloco", a)
	}

	n := &namBlock{latency: 0}
	n.cal = &inputCal{}
	n.cal.set(false, 12)
	if err := n.Load(arq); err != nil {
		t.Fatal(err)
	}
	n.prepare(48000, 256)
	n.process(make([]float64, 256), 1, NAMLite) // ganho já em 1 (sem rampa no começo)
	n.cal.set(true, 12)
	n.calG.SetTarget(math.Pow(10, 6.0/20))
	prev := n.calG.Next()
	if prev >= 1.01 {
		t.Errorf("a rampa começou no alvo: %v", prev)
	}
	for range int(48000 * CalibrationFadeMs / 1000 * 8) {
		prev = n.calG.Next()
	}
	if math.Abs(prev-math.Pow(10, 6.0/20)) > 1e-3 {
		t.Errorf("a rampa não chegou ao alvo em 8 constantes de tempo: %v", prev)
	}
}
