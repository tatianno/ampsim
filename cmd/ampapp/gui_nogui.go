//go:build nogui

package main

import (
	"errors"

	"ampsim/cmd/ampapp/audio"
	"ampsim/cmd/ampapp/source"
	"ampsim/engine"
)

func runGUI(*engine.Chain, *audio.Output, *source.FilePlayer, *audio.Monitor, func()) error {
	return errors.New("este binário foi compilado sem GUI (-tags nogui); use -console")
}
