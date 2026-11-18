// Pacote wav lê e grava arquivos WAV (PCM 8/16/24/32 bits e float 32/64 bits).
//
// As amostras são sempre convertidas para float64 na faixa [-1, 1). A normalização
// acontece por canal, ANTES de qualquer mistura de canais: foi exatamente a falta
// disso que causou o ruído no fim do áudio no protótipo Python (np.mean convertia
// int16 em float64 e a normalização era pulada).
package wav

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// Audio é um sinal multicanal já normalizado.
type Audio struct {
	SampleRate int
	Channels   [][]float64 // Channels[c][i]
}

// Len devolve o número de amostras por canal.
func (a *Audio) Len() int {
	if len(a.Channels) == 0 {
		return 0
	}
	return len(a.Channels[0])
}

// Mono devolve a média dos canais (os canais já estão normalizados).
func (a *Audio) Mono() []float64 {
	n := a.Len()
	out := make([]float64, n)
	if len(a.Channels) == 0 {
		return out
	}
	for _, ch := range a.Channels {
		for i, v := range ch {
			out[i] += v
		}
	}
	inv := 1.0 / float64(len(a.Channels))
	for i := range out {
		out[i] *= inv
	}
	return out
}

const (
	formatPCM        = 1
	formatFloat      = 3
	formatExtensible = 0xFFFE
)

// ReadFile lê um arquivo WAV.
func ReadFile(path string) (*Audio, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a, err := Read(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

// ReadMonoFile lê um WAV e devolve a taxa de amostragem e o sinal mono.
func ReadMonoFile(path string) (int, []float64, error) {
	a, err := ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	return a.SampleRate, a.Mono(), nil
}

// Read lê um WAV de um io.Reader. Chunks desconhecidos (LIST, fact, cue, ...) são ignorados.
func Read(r io.Reader) (*Audio, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("cabeçalho RIFF: %w", err)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("não é um arquivo RIFF/WAVE")
	}

	var (
		format, channels, bits uint16
		sampleRate             uint32
		haveFmt                bool
	)
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, errors.New("chunk 'data' não encontrado")
			}
			return nil, err
		}
		id := string(ch[0:4])
		size := binary.LittleEndian.Uint32(ch[4:8])

		switch id {
		case "fmt ":
			buf := make([]byte, size)
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, fmt.Errorf("chunk fmt: %w", err)
			}
			if size < 16 {
				return nil, errors.New("chunk fmt curto demais")
			}
			format = binary.LittleEndian.Uint16(buf[0:2])
			channels = binary.LittleEndian.Uint16(buf[2:4])
			sampleRate = binary.LittleEndian.Uint32(buf[4:8])
			bits = binary.LittleEndian.Uint16(buf[14:16])
			if format == formatExtensible {
				if size < 26 {
					return nil, errors.New("WAVE_FORMAT_EXTENSIBLE sem subformato")
				}
				format = binary.LittleEndian.Uint16(buf[24:26])
			}
			haveFmt = true
			if size%2 == 1 {
				io.CopyN(io.Discard, r, 1)
			}

		case "data":
			if !haveFmt {
				return nil, errors.New("chunk data antes do chunk fmt")
			}
			return readData(r, size, format, channels, bits, sampleRate)

		default:
			skip := int64(size) + int64(size%2) // chunks têm tamanho par
			if _, err := io.CopyN(io.Discard, r, skip); err != nil {
				return nil, fmt.Errorf("pulando chunk %q: %w", id, err)
			}
		}
	}
}

func readData(r io.Reader, size uint32, format, channels, bits uint16, sampleRate uint32) (*Audio, error) {
	if channels == 0 {
		return nil, errors.New("número de canais inválido")
	}
	bytesPer := int(bits) / 8
	switch {
	case format == formatPCM && (bits == 8 || bits == 16 || bits == 24 || bits == 32):
	case format == formatFloat && (bits == 32 || bits == 64):
	default:
		return nil, fmt.Errorf("formato não suportado (tag %d, %d bits)", format, bits)
	}

	frameBytes := bytesPer * int(channels)
	raw, err := io.ReadAll(io.LimitReader(r, int64(size)))
	if err != nil {
		return nil, err
	}
	n := len(raw) / frameBytes

	a := &Audio{SampleRate: int(sampleRate), Channels: make([][]float64, channels)}
	for c := range a.Channels {
		a.Channels[c] = make([]float64, n)
	}
	le := binary.LittleEndian
	for i := 0; i < n; i++ {
		for c := 0; c < int(channels); c++ {
			p := raw[(i*int(channels)+c)*bytesPer:]
			var v float64
			switch {
			case format == formatFloat && bits == 32:
				v = float64(math.Float32frombits(le.Uint32(p)))
			case format == formatFloat && bits == 64:
				v = math.Float64frombits(le.Uint64(p))
			case bits == 8: // PCM 8 bits é sem sinal
				v = (float64(p[0]) - 128) / 128
			case bits == 16:
				v = float64(int16(le.Uint16(p))) / 32768
			case bits == 24:
				s := int32(uint32(p[0])<<8|uint32(p[1])<<16|uint32(p[2])<<24) >> 8
				v = float64(s) / 8388608
			case bits == 32:
				v = float64(int32(le.Uint32(p))) / 2147483648
			}
			a.Channels[c][i] = v
		}
	}
	return a, nil
}

// Formato de gravação.
type Format int

const (
	PCM16   Format = iota // inteiro 16 bits, com clip em ±1
	Float32               // IEEE float 32 bits, sem clip
	PCM24                 // inteiro 24 bits, com clip em ±1 (padrão de estúdio)
)

// bits e tag do formato
func (f Format) layout() (bits, tag uint16, err error) {
	switch f {
	case PCM16:
		return 16, formatPCM, nil
	case PCM24:
		return 24, formatPCM, nil
	case Float32:
		return 32, formatFloat, nil
	}
	return 0, 0, errors.New("formato inválido")
}

// header monta o cabeçalho de 44 bytes.
func header(sampleRate, nc int, f Format, dataSize int) ([]byte, error) {
	bits, tag, err := f.layout()
	if err != nil {
		return nil, err
	}
	bytesPer := int(bits) / 8
	le := binary.LittleEndian
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	le.PutUint32(hdr[4:], uint32(36+dataSize))
	copy(hdr[8:], "WAVE")
	copy(hdr[12:], "fmt ")
	le.PutUint32(hdr[16:], 16)
	le.PutUint16(hdr[20:], tag)
	le.PutUint16(hdr[22:], uint16(nc))
	le.PutUint32(hdr[24:], uint32(sampleRate))
	le.PutUint32(hdr[28:], uint32(sampleRate*nc*bytesPer))
	le.PutUint16(hdr[32:], uint16(nc*bytesPer))
	le.PutUint16(hdr[34:], bits)
	copy(hdr[36:], "data")
	le.PutUint32(hdr[40:], uint32(dataSize))
	return hdr, nil
}

// putSample codifica uma amostra no formato.
func putSample(p []byte, v float64, f Format) {
	le := binary.LittleEndian
	switch f {
	case PCM16:
		s := math.Max(-32768, math.Min(32767, math.Round(v*32767)))
		le.PutUint16(p, uint16(int16(s)))
	case PCM24:
		s := int32(math.Max(-8388608, math.Min(8388607, math.Round(v*8388607))))
		p[0], p[1], p[2] = byte(s), byte(s>>8), byte(s>>16)
	case Float32:
		le.PutUint32(p, math.Float32bits(float32(v)))
	}
}

// WriteFile grava os canais num arquivo WAV.
func WriteFile(path string, sampleRate int, channels [][]float64, f Format) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(file)
	if err := Write(w, sampleRate, channels, f); err != nil {
		file.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// WriteMonoFile grava um sinal mono.
func WriteMonoFile(path string, sampleRate int, mono []float64, f Format) error {
	return WriteFile(path, sampleRate, [][]float64{mono}, f)
}

// Write grava um WAV num io.Writer.
func Write(w io.Writer, sampleRate int, channels [][]float64, f Format) error {
	if len(channels) == 0 {
		return errors.New("nenhum canal")
	}
	n := len(channels[0])
	for _, ch := range channels {
		if len(ch) != n {
			return errors.New("canais com tamanhos diferentes")
		}
	}
	nc := len(channels)
	bits, _, err := f.layout()
	if err != nil {
		return err
	}
	bytesPer := int(bits) / 8
	hdr, err := header(sampleRate, nc, f, n*nc*bytesPer)
	if err != nil {
		return err
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	buf := make([]byte, nc*bytesPer)
	for i := 0; i < n; i++ {
		for c := 0; c < nc; c++ {
			putSample(buf[c*bytesPer:], channels[c][i], f)
		}
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

// StreamWriter grava um WAV mono aos poucos (gravação ao vivo): o cabeçalho é escrito com
// tamanho zero e corrigido no Close. Não é para a thread de áudio (faz I/O).
type StreamWriter struct {
	f          *os.File
	w          *bufio.Writer
	format     Format
	sampleRate int
	bytesPer   int
	samples    int64
	buf        []byte
}

// Create começa a gravar um WAV mono em path.
func Create(path string, sampleRate int, f Format) (*StreamWriter, error) {
	bits, _, err := f.layout()
	if err != nil {
		return nil, err
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	hdr, _ := header(sampleRate, 1, f, 0)
	sw := &StreamWriter{f: file, w: bufio.NewWriterSize(file, 1<<16), format: f, sampleRate: sampleRate, bytesPer: int(bits) / 8}
	if _, err := sw.w.Write(hdr); err != nil {
		file.Close()
		return nil, err
	}
	return sw, nil
}

// Write acrescenta amostras.
func (s *StreamWriter) Write(x []float64) error {
	need := len(x) * s.bytesPer
	if cap(s.buf) < need {
		s.buf = make([]byte, need)
	}
	b := s.buf[:need]
	for i, v := range x {
		putSample(b[i*s.bytesPer:], v, s.format)
	}
	s.samples += int64(len(x))
	_, err := s.w.Write(b)
	return err
}

// Samples devolve quantas amostras já foram escritas.
func (s *StreamWriter) Samples() int64 { return s.samples }

// Close corrige o cabeçalho com o tamanho final e fecha o arquivo.
func (s *StreamWriter) Close() error {
	if err := s.w.Flush(); err != nil {
		s.f.Close()
		return err
	}
	hdr, _ := header(s.sampleRate, 1, s.format, int(s.samples)*s.bytesPer)
	if _, err := s.f.WriteAt(hdr, 0); err != nil {
		s.f.Close()
		return err
	}
	return s.f.Close()
}

// Info descreve um WAV sem ler as amostras.
type Info struct {
	SampleRate int
	Channels   int
	Bits       int
	Frames     int64
}

// Duration devolve a duração em segundos.
func (i Info) Duration() float64 {
	if i.SampleRate == 0 {
		return 0
	}
	return float64(i.Frames) / float64(i.SampleRate)
}

// Stat lê só o cabeçalho de um WAV (rápido mesmo para gravações longas).
func Stat(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Info{}, err
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return Info{}, errors.New("não é um arquivo RIFF/WAVE")
	}
	var info Info
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return Info{}, errors.New("chunk 'data' não encontrado")
		}
		id, size := string(ch[0:4]), binary.LittleEndian.Uint32(ch[4:8])
		switch id {
		case "fmt ":
			buf := make([]byte, size)
			if _, err := io.ReadFull(r, buf); err != nil || size < 16 {
				return Info{}, errors.New("chunk fmt inválido")
			}
			info.Channels = int(binary.LittleEndian.Uint16(buf[2:4]))
			info.SampleRate = int(binary.LittleEndian.Uint32(buf[4:8]))
			info.Bits = int(binary.LittleEndian.Uint16(buf[14:16]))
			if size%2 == 1 {
				r.Discard(1)
			}
		case "data":
			if info.Channels == 0 || info.Bits == 0 {
				return Info{}, errors.New("chunk data antes do fmt")
			}
			info.Frames = int64(size) / int64(info.Channels*info.Bits/8)
			return info, nil
		default:
			if _, err := r.Discard(int(size) + int(size%2)); err != nil {
				return Info{}, err
			}
		}
	}
}
