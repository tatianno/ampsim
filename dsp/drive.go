package dsp

import "math"

// Modos do pedal de drive.
const (
	DriveBoost = iota
	DriveOverdrive
	DriveDistortion
	DriveFuzz // Fuzz Face: assimétrico, sem controle de tom
	DriveMuff // Big Muff: dois estágios simétricos e o vale nos médios
)

// driveVoice são as escolhas de cada modo.
type driveVoice struct {
	preHP    float64 // passa-alta antes do ceifamento (Hz)
	gainMin  float64 // ganho com o knob Drive em 0
	gainMax  float64 // ganho com o knob Drive em 10
	clean    float64 // quanto do sinal limpo soma na saída (Tube Screamer: o limpo passa por fora)
	postLP   float64 // passa-baixa depois do ceifamento (Hz)
	norm     float64 // normalização: com Drive 5 e Level 0 dB, sai no nível da entrada (−12 dBFS)
	midScoop bool    // vale nos médios (Big Muff)
}

var driveVoices = [5]driveVoice{
	DriveBoost:      {preHP: 25, gainMin: 1, gainMax: 10, postLP: 18000, norm: 1},
	DriveOverdrive:  {preHP: 720, gainMin: 2, gainMax: 120, clean: 1, postLP: 5500, norm: 0.254},
	DriveDistortion: {preHP: 80, gainMin: 4, gainMax: 600, postLP: 6000, norm: 0.184},
	DriveFuzz:       {preHP: 40, gainMin: 3, gainMax: 400, postLP: 5000, norm: 0.199},
	DriveMuff:       {preHP: 60, gainMin: 2, gainMax: 1000, postLP: 4500, norm: 0.222, midScoop: true},
}

// Drive é o pedal de drive: boost limpo, overdrive (Tube Screamer: passa-alta antes do
// ceifamento suave e o limpo somado), distorção (RAT: muito ganho, ceifamento duro), fuzz
// (Fuzz Face: ceifamento assimétrico pesado, graves cheios) e muff (Big Muff: dois estágios
// simétricos e vale nos médios). O ceifamento roda a 4x.
//
// Tone é um "tilt" em volta de 1 kHz (0 = escuro, 5 = plano, 10 = brilhante).
type Drive struct {
	fs    float64
	mode  int
	os    *Oversampler4x
	up    [OversampleFactor]float64
	pre   Biquad // passa-alta do modo
	post  Biquad // passa-baixa do modo
	scoop Biquad // vale nos médios (fuzz)
	tiltL Biquad // tilt: shelf de graves
	tiltH Biquad // tilt: shelf de agudos
	dc    Biquad
	gain  Smoother
	level Smoother
	tone  float64
	clean *DelayLine // o sinal limpo, alinhado com a latência do oversampling
}

// NewDrive cria o drive (aloca).
func NewDrive(fs float64) *Drive {
	d := &Drive{fs: fs, os: NewOversampler4x(), tone: -1, clean: NewDelayLine(osLatency + 1)}
	d.gain.SetTime(fs, 20)
	d.level.SetTime(fs, 20)
	d.dc.SetCoefs(OnePoleHighpass(fs, 15))
	d.SetMode(DriveOverdrive)
	return d
}

// DriveLatency é a latência do drive (oversampling), igual em todos os modos.
const DriveLatency = osLatency

// SetMode troca o modo (o chamador cuida do crossfade).
func (d *Drive) SetMode(mode int) {
	if mode < DriveBoost || mode > DriveMuff {
		mode = DriveOverdrive
	}
	d.mode = mode
	v := driveVoices[mode]
	d.pre.SetCoefs(OnePoleHighpass(d.fs, v.preHP))
	d.post.SetCoefs(Lowpass2(d.fs, math.Min(v.postLP, 0.45*d.fs), 0.707))
	d.scoop.SetCoefs(Peaking(d.fs, 900, 0.8, -9))
}

// Set ajusta drive (0..10), tone (0..10) e o nível de saída (dB).
func (d *Drive) Set(drive, tone, levelDB float64) {
	v := driveVoices[d.mode]
	t := drive / 10
	d.gain.SetTarget(v.gainMin * math.Pow(v.gainMax/v.gainMin, t)) // ganho em escala log
	d.level.SetTarget(math.Pow(10, levelDB/20) * v.norm)
	if tone != d.tone {
		d.tone = tone
		tilt := (tone - 5) / 5 * 6 // ±6 dB
		d.tiltL.SetCoefs(LowShelf(d.fs, 1000, 0.707, -tilt))
		d.tiltH.SetCoefs(HighShelf(d.fs, 1000, 0.707, tilt))
	}
}

// Reset zera o estado.
func (d *Drive) Reset() {
	d.os.Reset()
	d.clean.Reset()
	for _, f := range []*Biquad{&d.pre, &d.post, &d.scoop, &d.tiltL, &d.tiltH, &d.dc} {
		f.Reset()
	}
}

// clip é a não linearidade de cada modo.
func (d *Drive) clip(u float64) float64 {
	switch d.mode {
	case DriveBoost:
		return u
	case DriveOverdrive: // diodos na realimentação: suave e simétrico
		return math.Tanh(u)
	case DriveDistortion: // diodos para o terra: joelho mais duro
		return u / math.Pow(1+math.Pow(math.Abs(u), 2.5), 1/2.5)
	}
	if d.mode == DriveMuff { // dois estágios de diodos em cascata: simétrico e muito comprimido
		return math.Tanh(2 * math.Tanh(u))
	}
	// fuzz: assimétrico (transistor polarizado), quase quadrado
	if u > 0 {
		return math.Tanh(1.5 * u)
	}
	return math.Tanh(0.8*u) * 0.85
}

// ProcessBlock processa in-place.
func (d *Drive) ProcessBlock(buf []float64) {
	v := driveVoices[d.mode]
	for i, x := range buf {
		g := d.gain.Next()
		h := d.pre.Process(x)
		d.os.Up(h, &d.up)
		for k, u := range d.up {
			d.up[k] = d.clip(u * g)
		}
		y := d.os.Down(&d.up)
		c := d.clean.ReadInt(osLatency) // x atrasado como o ramo saturado
		d.clean.Write(x)
		y += v.clean * c // Tube Screamer: o limpo passa por fora do ceifamento
		if v.midScoop {
			y = d.scoop.Process(y)
		}
		y = d.post.Process(y)
		y = d.tiltH.Process(d.tiltL.Process(y))
		buf[i] = d.dc.Process(y) * d.level.Next()
	}
}
