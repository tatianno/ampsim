package audio

import "strings"

// jackDevice é uma placa vista pelo JACK: as portas físicas agrupadas pelo nome do
// cliente (a parte antes de ":"), ex.: "alsa_input.usb-PreSonus…:capture_FL".
type jackDevice struct {
	Name  string
	Ports []string
}

// groupJackPorts agrupa portas físicas por placa, na ordem em que aparecem,
// ignorando as portas de monitor (cópia do que sai numa placa).
func groupJackPorts(ports []string) []jackDevice {
	var devs []jackDevice
	idx := map[string]int{}
	for _, p := range ports {
		client, port, ok := strings.Cut(p, ":")
		if !ok || strings.Contains(strings.ToLower(port), "monitor") {
			continue
		}
		i, seen := idx[client]
		if !seen {
			i = len(devs)
			idx[client] = i
			devs = append(devs, jackDevice{Name: client})
		}
		devs[i].Ports = append(devs[i].Ports, p)
	}
	return devs
}

// jackDisplayName deixa o nome da placa mais legível
// ("alsa_input.usb-PreSonus_Audio_AudioBox_USB-01.analog-stereo" → "PreSonus Audio AudioBox USB-01 analog-stereo").
func jackDisplayName(client string) string {
	s := client
	for _, pre := range []string{"alsa_input.", "alsa_output.", "usb-", "pci-"} {
		s = strings.TrimPrefix(s, pre)
	}
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.Replace(s, ".", " ", 1)
	return s
}
