//go:build nogui

package main

import (
	"errors"

	"github.com/tatianno/ampsim/cmd/ampapp/audio"
)

func runGUI(*audio.Session, func(), string, string) error {
	return errors.New("este binário foi compilado sem GUI (-tags nogui); use -console")
}
