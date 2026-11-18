package audio

import (
	"slices"
	"testing"
)

func TestAgruparPortasJack(t *testing.T) {
	portas := []string{
		"alsa_input.usb-PreSonus_Audio_AudioBox_USB-01.analog-stereo:capture_FL",
		"alsa_input.usb-PreSonus_Audio_AudioBox_USB-01.analog-stereo:capture_FR",
		"alsa_input.pci-0000_00_1f.3.analog-stereo:capture_FL",
		"alsa_output.usb-PreSonus_Audio_AudioBox_USB-01.analog-stereo:monitor_FL", // monitor: fora
		"alsa_input.pci-0000_00_1f.3.analog-stereo:capture_FR",
		"semdoispontos",
	}
	devs := groupJackPorts(portas)
	if len(devs) != 2 {
		t.Fatalf("%d placas: %+v", len(devs), devs)
	}
	if devs[0].Name != "alsa_input.usb-PreSonus_Audio_AudioBox_USB-01.analog-stereo" || len(devs[0].Ports) != 2 {
		t.Errorf("placa 0: %+v", devs[0])
	}
	if !slices.Equal(devs[1].Ports, []string{portas[2], portas[4]}) {
		t.Errorf("placa 1 deveria manter a ordem das portas: %v", devs[1].Ports)
	}
	if got := jackDisplayName(devs[0].Name); got != "PreSonus Audio AudioBox USB-01 analog-stereo" {
		t.Errorf("nome legível: %q", got)
	}
}
