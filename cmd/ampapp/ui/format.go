package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/tatianno/ampsim/engine"
)

// FormatValue formata o valor de um parâmetro para exibição ("+3.0 dB", "1.20 Hz", "380 ms", "45%").
func FormatValue(in engine.ParamInfo, v float64) string {
	var s string
	switch in.Unit {
	case "dB":
		s = fmt.Sprintf("%+.1f dB", v)
		if math.Abs(v) < 0.05 {
			s = "0.0 dB"
		}
	case "Hz":
		switch {
		case v >= 1000:
			s = fmt.Sprintf("%.1f kHz", v/1000)
		case v >= 100:
			s = fmt.Sprintf("%.0f Hz", v)
		case v >= 10:
			s = fmt.Sprintf("%.1f Hz", v)
		default:
			s = fmt.Sprintf("%.2f Hz", v)
		}
	case "ms":
		if v < 10 {
			s = fmt.Sprintf("%.2f ms", v)
		} else {
			s = fmt.Sprintf("%.0f ms", v)
		}
	case "%":
		s = fmt.Sprintf("%.0f%%", v)
	case ":1": // razão de compressão
		s = fmt.Sprintf("%.1f:1", v)
	case "oct":
		s = fmt.Sprintf("%.1f oct", v)
	case "":
		if in.Step >= 1 {
			s = fmt.Sprintf("%.0f", v)
		} else {
			s = fmt.Sprintf("%.1f", v)
		}
	default:
		s = fmt.Sprintf("%.1f %s", v, in.Unit)
	}
	return strings.Replace(s, "-", "−", 1) // sinal de menos tipográfico
}
