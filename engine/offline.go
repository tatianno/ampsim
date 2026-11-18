package engine

import "fmt"

// RenderOffline processa x (mono, taxa fs) com o som descrito em st, numa cadeia nova:
// a cadeia ao vivo não é tocada (dá para exportar enquanto toca). Acrescenta tailS segundos
// de silêncio no fim (caudas de delay e reverb) e compensa a latência da cadeia, para a
// saída ficar alinhada com a entrada. progress (opcional) recebe a fração concluída.
func RenderOffline(st State, x []float64, fs, tailS float64, block int, progress func(float64)) ([]float64, error) {
	c := NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		return nil, fmt.Errorf("aplicando o som atual: %w", err)
	}
	if block <= 0 {
		block = 512
	}
	c.Prepare(fs, block)
	lat := c.Latency()
	n := len(x) + int(tailS*fs)
	buf := make([]float64, n+lat)
	copy(buf, x)
	every := max(1, len(buf)/block/100) // ~100 atualizações de progresso
	for i, k := 0, 0; i < len(buf); i, k = i+block, k+1 {
		c.ProcessMono(buf[i:min(i+block, len(buf))])
		if progress != nil && k%every == 0 {
			progress(float64(i) / float64(len(buf)))
		}
	}
	if progress != nil {
		progress(1)
	}
	return buf[lat:], nil
}
