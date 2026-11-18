package dsp

import "math"

// Octaver é um oitavador monofônico no estilo do OC-2 (analógico):
//
//   - o sinal passa por um passa-baixa (fica a fundamental);
//   - um flip-flop troca de estado a cada ciclo (cruzamento de zero subindo, com histerese);
//     multiplicar o sinal filtrado por ±1 dele dá uma oitava abaixo, com a dinâmica da
//     palhetada;
//   - o mesmo processo aplicado à oitava abaixo (já filtrada) dá a segunda oitava abaixo;
//   - a oitava acima vem da retificação de onda completa (como no Octavia), sem a média.
//
// Como todo oitavador analógico, funciona com uma nota de cada vez.
type Octaver struct {
	fs                  float64
	in1, in2            Biquad // passa-baixa da entrada (isola a fundamental)
	out1, out2          Biquad // suavizam as oitavas abaixo
	upHP                Biquad // tira a média da retificação
	envK                float64
	d1, d2              divider // as duas divisões por 2, em cascata
	sub1, sub2, up, dry Smoother
}

// divider é um divisor de frequência por 2: um flip-flop que troca a cada ciclo do sinal.
type divider struct {
	env   float64
	armed bool // histerese: precisa descer abaixo de −h antes de contar outra subida
	ff    float64
}

func (d *divider) next(x, envK float64) float64 {
	d.env += envK * (math.Abs(x) - d.env)
	h := 0.3 * d.env // histerese proporcional ao nível
	if x < -h {
		d.armed = true
	} else if d.armed && x > h { // subida: um ciclo completo
		d.armed = false
		d.ff = -d.ff
	}
	return x * d.ff
}

// NewOctaver cria o oitavador.
func NewOctaver(fs float64) *Octaver {
	o := &Octaver{fs: fs, envK: 1 - math.Exp(-1/(0.02*fs)), d1: divider{ff: 1}, d2: divider{ff: 1}}
	o.in1.SetCoefs(Lowpass2(fs, 700, 0.707))
	o.in2.SetCoefs(Lowpass2(fs, 700, 0.707))
	o.out1.SetCoefs(Lowpass2(fs, 900, 0.707))
	o.out2.SetCoefs(Lowpass2(fs, 600, 0.707))
	o.upHP.SetCoefs(Highpass2(fs, 80, 0.707))
	for _, s := range []*Smoother{&o.sub1, &o.sub2, &o.up, &o.dry} {
		s.SetTime(fs, 20)
	}
	return o
}

// Set ajusta os níveis (0..1): uma oitava abaixo, duas abaixo, uma acima e o sinal seco.
func (o *Octaver) Set(sub1, sub2, up, dry float64) {
	o.sub1.SetTarget(sub1)
	o.sub2.SetTarget(sub2)
	o.up.SetTarget(up)
	o.dry.SetTarget(dry)
}

// Reset zera o estado.
func (o *Octaver) Reset() {
	for _, f := range []*Biquad{&o.in1, &o.in2, &o.out1, &o.out2, &o.upHP} {
		f.Reset()
	}
	o.d1, o.d2 = divider{ff: 1}, divider{ff: 1}
}

// ProcessBlock processa in-place.
func (o *Octaver) ProcessBlock(buf []float64) {
	for i, x := range buf {
		f := o.in2.Process(o.in1.Process(x))
		s1 := o.out1.Process(o.d1.next(f, o.envK))
		s2 := o.out2.Process(o.d2.next(s1, o.envK))
		u := o.upHP.Process(math.Abs(x) * 2)
		buf[i] = o.dry.Next()*x + o.sub1.Next()*s1*1.6 + o.sub2.Next()*s2*2.5 + o.up.Next()*u
	}
}
