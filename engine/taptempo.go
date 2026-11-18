package engine

import (
	"fmt"
	"strings"
	"time"
)

// TapTempo calcula o andamento a partir de toques (botão TAP ou tecla T).
type TapTempo struct {
	taps []time.Time
}

// Regras do tap tempo.
const (
	TapResetAfter = 2 * time.Second // pausa maior que isso começa uma contagem nova
	TapAverage    = 4               // média dos últimos 4 intervalos
)

// Tap registra um toque. Do 2º toque em diante devolve o intervalo médio (ms).
func (t *TapTempo) Tap(now time.Time) (intervalMs float64, ok bool) {
	if n := len(t.taps); n > 0 && (now.Sub(t.taps[n-1]) > TapResetAfter || now.Before(t.taps[n-1])) {
		t.taps = t.taps[:0]
	}
	t.taps = append(t.taps, now)
	if len(t.taps) > TapAverage+1 {
		t.taps = t.taps[len(t.taps)-(TapAverage+1):]
	}
	if len(t.taps) < 2 {
		return 0, false
	}
	total := t.taps[len(t.taps)-1].Sub(t.taps[0])
	return float64(total) / float64(time.Millisecond) / float64(len(t.taps)-1), true
}

// Subdivision é uma divisão do tempo para o delay.
type Subdivision struct {
	Name   string
	Factor float64 // fração da semínima (pulso do tap)
}

// Subdivisions são as divisões oferecidas (a pontuada é a clássica de delay).
var Subdivisions = []Subdivision{
	{"1/4", 1},
	{"1/8.", 0.75},
	{"1/8", 0.5},
	{"1/16", 0.25},
}

// FindSubdivision busca uma subdivisão pelo nome ("1/8." etc.).
func FindSubdivision(name string) (Subdivision, error) {
	for _, s := range Subdivisions {
		if strings.EqualFold(s.Name, name) {
			return s, nil
		}
	}
	return Subdivision{}, fmt.Errorf("subdivisão %q desconhecida (use 1/4, 1/8., 1/8 ou 1/16)", name)
}

// DelayMs converte andamento (BPM) e subdivisão em tempo de delay.
func DelayMs(bpm float64, s Subdivision) float64 { return 60000 / bpm * s.Factor }

// BPM converte tempo de delay e subdivisão de volta em andamento.
func BPM(delayMs float64, s Subdivision) float64 { return 60000 * s.Factor / delayMs }
