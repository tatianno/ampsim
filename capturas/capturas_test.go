package capturas

import (
	"math"
	"slices"
	"testing"
)

func TestTodasCarregam(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("nenhuma captura embutida")
	}
	for _, n := range names {
		m, err := Load(n)
		if err != nil {
			t.Fatal(err)
		}
		if m.SampleRate != 48000 {
			t.Errorf("%s: taxa %g", n, m.SampleRate)
		}
		if len(m.Submodels) != 2 || m.Submodels[0].Spec.Channels >= m.Submodels[1].Spec.Channels {
			t.Errorf("%s: esperava lite + completo", n)
		}
		net := m.NewNet(0, 256)
		x := make([]float32, 4800)
		for i := range x {
			x[i] = float32(0.3 * math.Sin(2*math.Pi*220*float64(i)/48000))
		}
		net.Process(x, x)
		peak := 0.0
		for _, v := range x {
			peak = max(peak, math.Abs(float64(v)))
		}
		if math.IsNaN(peak) || peak == 0 || peak > 4 {
			t.Errorf("%s: pico %g", n, peak)
		}
	}
	if _, err := Load("não existe"); err == nil {
		t.Error("esperava erro")
	}
	if _, err := Data("../go.mod"); err == nil {
		t.Error("Data não pode sair da pasta")
	}
}

func TestOrdemNatural(t *testing.T) {
	got := []string{"Clean V10", "Dist Sat3", "Clean V7", "Dist Sat0", "Dist Sat10"}
	slices.SortFunc(got, naturalCompare)
	want := []string{"Clean V7", "Clean V10", "Dist Sat0", "Dist Sat3", "Dist Sat10"}
	if !slices.Equal(got, want) {
		t.Errorf("%v", got)
	}
}
