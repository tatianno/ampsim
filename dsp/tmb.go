package dsp

import (
	"math"
	"math/cmplx"
)

// Tone stack "TMB" (treble, middle, bass) dos amps Fender e Marshall clássicos: um circuito
// passivo único, em que os controles interagem (ver Yeh e Smith, "Discretization of the
// '59 Fender Bassman Tone Stack", DAFx 2006). Topologia:
//
//	Vi ──C1──┬ (topo do pot de agudo R1)
//	         │ (1−t)·R1
//	         ├──── Vo (cursor do agudo)
//	         │ t·R1
//	Vi ─R4─ N ──C2── T2 (fim do pot de agudo)
//	         │        │ l·R2 (pot de grave, reostato)
//	         └──C3──  Q
//	                  │ m·R3 (pot de médio, reostato)
//	                 GND
//
// Em vez de fórmulas fechadas, a função de transferência é obtida por análise nodal (MNA)
// com polinômios em s: cada condutância é constante e cada capacitor contribui s·C; a regra
// de Cramer dá H(s) = det(A_out)/det(A), com grau 3 (um por capacitor).

// TMBModel são os componentes de um tone stack TMB.
type TMBModel struct {
	Name           string
	R1, R2, R3, R4 float64 // agudo, grave, médio, "slope"
	C1, C2, C3     float64 // agudo, grave, médio
}

// Modelos clássicos (valores dos esquemas originais).
var (
	TMBFender = TMBModel{Name: "Fender", // '59 Bassman 5F6-A
		R1: 250e3, R2: 1e6, R3: 25e3, R4: 56e3, C1: 250e-12, C2: 20e-9, C3: 20e-9}
	TMBMarshall = TMBModel{Name: "Marshall", // JTM45 / 1959
		R1: 220e3, R2: 1e6, R3: 22e3, R4: 33e3, C1: 470e-12, C2: 22e-9, C3: 22e-9}
	// TMBHighGain é o tone stack da voz High Gain: a topologia e os pots do Marshall, com o
	// "slope" e os capacitores ajustados à curva medida de um amp moderno de alto ganho
	// (vale nos médios e presença alta; veja dsp.HighGain).
	TMBHighGain = TMBModel{Name: "HighGain",
		R1: 220e3, R2: 1e6, R3: 22e3, R4: 66e3, C1: 332e-12, C2: 47e-9, C3: 31e-9}
)

// p6 é um polinômio em s (coeficientes em ordem crescente) com grau ≤ 5: o determinante
// de uma matriz 5×5 com entradas de grau ≤ 1. Tamanho fixo: nada é alocado (o cálculo roda
// na thread de áudio quando um knob gira).
type p6 [6]float64

func (p p6) mul(q p6) (r p6) {
	for i, a := range p {
		if a == 0 {
			continue
		}
		for j := 0; i+j < len(r); j++ {
			r[i+j] += a * q[j]
		}
	}
	return r
}

// pmat é uma matriz de polinômios; só o canto n×n é usado em detN.
type pmat [nNodes][nNodes]p6

// detN calcula o determinante do canto n×n por expansão de Laplace (por valor: fica na pilha).
func detN(m pmat, n int) p6 {
	if n == 1 {
		return m[0][0]
	}
	var r p6
	for c := 0; c < n; c++ {
		if m[0][c] == (p6{}) {
			continue
		}
		var sub pmat
		for i := 1; i < n; i++ {
			k := 0
			for j := 0; j < n; j++ {
				if j != c {
					sub[i-1][k] = m[i][j]
					k++
				}
			}
		}
		d := m[0][c].mul(detN(sub, n-1))
		if c%2 == 1 {
			for k := range d {
				d[k] = -d[k]
			}
		}
		for k := range r {
			r[k] += d[k]
		}
	}
	return r
}

// nós desconhecidos do circuito
const (
	nT1 = iota // topo do pot de agudo
	nO         // saída (cursor do agudo)
	nT2        // fim do pot de agudo / topo do grave
	nN         // junção de R4, C2 e C3
	nQ         // fim do grave / topo do médio
	nNodes
)

const (
	nodeVi  = -1
	nodeGnd = -2
)

// tmbElement é um elemento do circuito: entre os nós a e b, admitância g + s·c.
type tmbElement struct {
	a, b int
	g, c float64
}

// elements devolve os 8 elementos para as posições dos pots (array fixo: sem alocar).
func (m TMBModel) elements(t, mid, l float64) [8]tmbElement {
	return [8]tmbElement{
		{nodeVi, nT1, 0, m.C1},
		{nT1, nO, 1 / ((1 - t) * m.R1), 0},
		{nO, nT2, 1 / (t * m.R1), 0},
		{nodeVi, nN, 1 / m.R4, 0},
		{nN, nT2, 0, m.C2},
		{nN, nQ, 0, m.C3},
		{nT2, nQ, 1 / (l * m.R2), 0},
		{nQ, nodeGnd, 1 / (mid * m.R3), 0},
	}
}

// clampPot evita resistência zero (nós em curto deixam o sistema singular), como num pot real.
func clampPot(x float64) float64 { return math.Max(1e-4, math.Min(1-1e-4, x)) }

// Analog devolve H(s) = (b0 + b1·s + b2·s² + b3·s³)/(a0 + … + a3·s³) para as posições dos
// pots t (agudo), mid (médio) e l (grave), cada uma de 0 a 1. Não aloca.
func (m TMBModel) Analog(t, mid, l float64) (b, a [4]float64) {
	t, mid, l = clampPot(t), clampPot(mid), clampPot(l)
	var A pmat
	var rhs [nNodes]p6
	for _, e := range m.elements(t, mid, l) {
		y := p6{e.g, e.c}
		for _, p := range [2][2]int{{e.a, e.b}, {e.b, e.a}} {
			i, k := p[0], p[1]
			if i < 0 {
				continue
			}
			for q := range y {
				A[i][i][q] += y[q] // corrente saindo do nó i pelo elemento
				switch {
				case k >= 0:
					A[i][k][q] -= y[q]
				case k == nodeVi: // Vi conhecido (= 1) vai para o lado direito
					rhs[i][q] += y[q]
				}
			}
		}
	}
	den := detN(A, nNodes)
	for i := range A { // Cramer: troca a coluna da saída pelo lado direito
		A[i][nO] = rhs[i]
	}
	num := detN(A, nNodes)
	k := den[0]
	for i := 0; i < 4; i++ {
		b[i], a[i] = num[i]/k, den[i]/k
	}
	return b, a
}

// AnalogResponse avalia H(jω) direto pelo circuito (resolvendo o sistema complexo), sem
// passar pelos polinômios: serve para conferir Analog.
func (m TMBModel) AnalogResponse(t, mid, l, f float64) complex128 {
	t, mid, l = clampPot(t), clampPot(mid), clampPot(l)
	s := complex(0, 2*math.Pi*f)
	n := nNodes
	A := make([][]complex128, n)
	for i := range A {
		A[i] = make([]complex128, n+1) // última coluna = lado direito
	}
	for _, e := range m.elements(t, mid, l) {
		y := complex(e.g, 0) + s*complex(e.c, 0)
		for _, p := range [2][2]int{{e.a, e.b}, {e.b, e.a}} {
			i, k := p[0], p[1]
			if i < 0 {
				continue
			}
			A[i][i] += y
			if k >= 0 {
				A[i][k] -= y
			} else if k == nodeVi {
				A[i][n] += y
			}
		}
	}
	// eliminação de Gauss com pivotamento parcial
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if cmplx.Abs(A[r][c]) > cmplx.Abs(A[p][c]) {
				p = r
			}
		}
		A[c], A[p] = A[p], A[c]
		for r := c + 1; r < n; r++ {
			f := A[r][c] / A[c][c]
			for k := c; k <= n; k++ {
				A[r][k] -= f * A[c][k]
			}
		}
	}
	x := make([]complex128, n)
	for r := n - 1; r >= 0; r-- {
		v := A[r][n]
		for k := r + 1; k < n; k++ {
			v -= A[r][k] * x[k]
		}
		x[r] = v / A[r][r]
	}
	return x[nO]
}

// Bilinear3 discretiza H(s) de 3ª ordem com s = K·(1−z⁻¹)/(1+z⁻¹), K = 2·fs.
// Devolve B(z⁻¹) e A(z⁻¹) normalizados (A[0] = 1).
func Bilinear3(b, a [4]float64, fs float64) (B, A [4]float64) {
	K := 2 * fs
	// (1−z⁻¹)^i · (1+z⁻¹)^(3−i), em potências de z⁻¹
	terms := [4][4]float64{
		{1, 3, 3, 1},   // i=0: (1+z)^3
		{1, 1, -1, -1}, // i=1: (1−z)(1+z)^2
		{1, -1, -1, 1}, // i=2: (1−z)^2(1+z)
		{1, -3, 3, -1}, // i=3: (1−z)^3
	}
	k := 1.0
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			B[j] += b[i] * k * terms[i][j]
			A[j] += a[i] * k * terms[i][j]
		}
		k *= K
	}
	a0 := A[0]
	for j := range A {
		A[j] /= a0
		B[j] /= a0
	}
	return B, A
}

// TMBStack é o tone stack modelado rodando em tempo real: H(z) de 3ª ordem na forma direta
// II transposta, com os knobs (0..10) suavizados e os coeficientes recalculados a cada 32
// amostras enquanto algum knob se move.
type TMBStack struct {
	model  TMBModel
	fs     float64
	b, a   [4]float64 // H(z), a[0] = 1
	z      [3]float64
	knobs  [3]Smoother // grave, médio, agudo
	cur    [3]float64  // valores aplicados
	count  int
	makeup float64 // compensa a perda do circuito passivo
}

// Taper do pot de grave (logarítmico, "audio taper"), como em Yeh e Smith.
func bassTaper(x float64) float64 { return math.Exp((x - 1) * 3.4) }

// NewTMBStack cria o tone stack do modelo para a taxa fs (aloca; fora da thread de áudio).
func NewTMBStack(fs float64, m TMBModel) *TMBStack {
	s := &TMBStack{model: m, fs: fs}
	for i := range s.knobs {
		s.knobs[i] = NewSmoother(fs, 30)
		s.knobs[i].Snap(5)
	}
	// ganho de compensação: média (em dB) da resposta com tudo em 5, de 100 Hz a 5 kHz
	b, a := m.Analog(0.5, 0.5, bassTaper(0.5))
	sum, n := 0.0, 0
	for f := 100.0; f <= 5000; f *= 1.1 {
		h := 0.0
		w := 2 * math.Pi * f
		var num, den complex128
		p := complex(1, 0)
		for i := 0; i < 4; i++ {
			num += complex(b[i], 0) * p
			den += complex(a[i], 0) * p
			p *= complex(0, w)
		}
		h = 20 * math.Log10(cmplxAbs(num/den))
		sum += h
		n++
	}
	s.makeup = math.Pow(10, -sum/float64(n)/20)
	s.cur = [3]float64{-1, -1, -1}
	s.update()
	return s
}

func cmplxAbs(c complex128) float64 { return math.Hypot(real(c), imag(c)) }

// Model devolve o modelo.
func (s *TMBStack) Model() TMBModel { return s.model }

// Set ajusta os knobs (0..10).
func (s *TMBStack) Set(bass, mid, treble float64) {
	s.knobs[0].SetTarget(bass)
	s.knobs[1].SetTarget(mid)
	s.knobs[2].SetTarget(treble)
}

// Reset zera o estado do filtro.
func (s *TMBStack) Reset() { s.z = [3]float64{} }

func (s *TMBStack) update() {
	v := [3]float64{s.knobs[0].Value(), s.knobs[1].Value(), s.knobs[2].Value()}
	if v == s.cur {
		return
	}
	s.cur = v
	ba, aa := s.model.Analog(v[2]/10, v[1]/10, bassTaper(v[0]/10))
	s.b, s.a = Bilinear3(ba, aa, s.fs)
	for i := range s.b {
		s.b[i] *= s.makeup
	}
}

// ProcessBlock filtra in-place.
func (s *TMBStack) ProcessBlock(buf []float64) {
	b, a := &s.b, &s.a
	for i, x := range buf {
		for k := range s.knobs {
			s.knobs[k].Next()
		}
		if s.count == 0 {
			s.update()
			b, a = &s.b, &s.a
		}
		s.count = (s.count + 1) & 31
		y := b[0]*x + s.z[0]
		s.z[0] = b[1]*x - a[1]*y + s.z[1]
		s.z[1] = b[2]*x - a[2]*y + s.z[2]
		s.z[2] = b[3]*x - a[3]*y
		buf[i] = y
	}
}
