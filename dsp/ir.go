package dsp

import "math"

// PrepareIR prepara uma resposta ao impulso de gabinete (porte de preparar_impulse_response):
//  1. corta o silêncio do início (a partir de 5% do pico, menos 2 amostras);
//  2. reamostra para fsTarget;
//  3. limita a maxMs milissegundos;
//  4. fade-out quadrático nos últimos 25 ms;
//  5. normaliza a energia (norma L2 = 1).
//
// Aloca memória: chamar fora da thread de áudio.
func PrepareIR(ir []float64, fsIR, fsTarget int, maxMs float64) []float64 {
	peak := 0.0
	for _, v := range ir {
		peak = math.Max(peak, math.Abs(v))
	}
	start := 0
	for i, v := range ir {
		if math.Abs(v) >= 0.05*peak {
			start = max(0, i-2)
			break
		}
	}
	out := append([]float64(nil), ir[start:]...)

	if fsIR != fsTarget {
		out = ResampleRate(out, fsIR, fsTarget)
	}

	if maxLen := int(maxMs / 1000 * float64(fsTarget)); len(out) > maxLen {
		out = out[:maxLen]
	}

	fade := min(len(out), int(0.025*float64(fsTarget)))
	for i := 0; i < fade; i++ {
		g := 1.0
		if fade > 1 {
			g = 1 - float64(i)/float64(fade-1)
		}
		out[len(out)-fade+i] *= g * g
	}

	energy := 0.0
	for _, v := range out {
		energy += v * v
	}
	if energy > 0 {
		inv := 1 / math.Sqrt(energy)
		for i := range out {
			out[i] *= inv
		}
	}
	return out
}

// GenericCabIR gera a resposta ao impulso de um gabinete genérico (sintética, sem licença
// de terceiros), usada quando nenhuma IR foi carregada. É só uma curva de EQ típica de
// gabinete de guitarra com alto-falante de 12": corte de graves, ressonância do gabinete,
// vale "caixa", presença do cone e queda forte acima de ~5 kHz. Fase mínima (filtros IIR).
func GenericCabIR(fs float64) []float64 {
	stages := [][]Coefs{
		ButterworthHighpass(2, fs, 75),
		{Peaking(fs, 110, 1.2, 3)},  // ressonância do gabinete
		{Peaking(fs, 450, 1.0, -4)}, // vale "caixa"
		{Peaking(fs, 2200, 1.4, 4)}, // presença do cone
		{Peaking(fs, 3800, 2.0, 2)},
		ButterworthLowpass(4, fs, 5200),
		ButterworthLowpass(2, fs, 8000),
	}
	var all []Coefs
	for _, s := range stages {
		all = append(all, s...)
	}
	c := NewCascade(all)
	ir := make([]float64, int(0.06*fs)) // 60 ms: os filtros já decaíram
	ir[0] = 1
	c.ProcessBlock(ir)
	return PrepareIR(ir, int(fs), int(fs), IRGenericMs)
}

// IRGenericMs é o comprimento da IR genérica.
const IRGenericMs = 60.0
