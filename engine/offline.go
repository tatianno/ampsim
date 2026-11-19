package engine

import "fmt"

// RenderOffline processa x (mono, taxa fs) com o som descrito em st, numa cadeia nova:
// a cadeia ao vivo não é tocada (dá para exportar enquanto toca). Acrescenta tailS segundos
// de silêncio no fim (caudas de delay e reverb) e compensa a latência da cadeia, para a
// saída ficar alinhada com a entrada. progress (opcional) recebe a fração concluída.
func RenderOffline(st State, x []float64, fs, tailS float64, block int, progress func(float64)) ([]float64, error) {
	ch, err := RenderOfflineChannels(st, x, fs, tailS, block, false, progress)
	if err != nil {
		return nil, err
	}
	return ch[0], nil
}

// RenderOfflineChannels é o RenderOffline com a saída em estéreo (stereo = true: devolve L e
// R) ou mono (devolve um canal).
func RenderOfflineChannels(st State, x []float64, fs, tailS float64, block int, stereo bool, progress func(float64)) ([][]float64, error) {
	c := NewDefaultChain()
	if err := c.ApplyState(st); err != nil {
		return nil, fmt.Errorf("aplicando o som atual: %w", err)
	}
	if block <= 0 {
		block = 512
	}
	c.SetStereo(stereo)
	c.Prepare(fs, block)
	lat := c.Latency()
	n := len(x) + int(tailS*fs)
	buf := make([]float64, n+lat)
	copy(buf, x)
	var right []float64
	if stereo {
		right = make([]float64, len(buf))
	}
	every := max(1, len(buf)/block/100) // ~100 atualizações de progresso
	for i, k := 0, 0; i < len(buf); i, k = i+block, k+1 {
		j := min(i+block, len(buf))
		if stereo {
			c.ProcessStereo(buf[i:j], right[i:j])
		} else {
			c.ProcessMono(buf[i:j])
		}
		if progress != nil && k%every == 0 {
			progress(float64(i) / float64(len(buf)))
		}
	}
	if progress != nil {
		progress(1)
	}
	if stereo {
		return [][]float64{buf[lat:], right[lat:]}, nil
	}
	return [][]float64{buf[lat:]}, nil
}
