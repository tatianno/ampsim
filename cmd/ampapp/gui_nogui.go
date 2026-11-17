//go:build nogui

package main

import (
	"errors"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
	"github.com/tatianno/ampsim/cmd/ampapp/source"
	"github.com/tatianno/ampsim/engine"
)

func runGUI(*engine.Chain, *audio.Output, *source.FilePlayer, *audio.Monitor, func()) error {
	return errors.New("este binário foi compilado sem GUI (-tags nogui); use -console")
}
