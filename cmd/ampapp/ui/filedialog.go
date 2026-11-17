package ui

import (
	"errors"
	"os/exec"
	"strings"
)

// ErrNoDialog indica que não há programa de diálogo de arquivo instalado.
var ErrNoDialog = errors.New("seletor de arquivo indisponível (instale o zenity)")

// PickWAV abre o seletor de arquivo do sistema (zenity) e devolve o caminho escolhido.
// Devolve "" sem erro se o usuário cancelar. Bloqueia: chamar numa goroutine.
func PickWAV(title string) (string, error) {
	if _, err := exec.LookPath("zenity"); err != nil {
		return "", ErrNoDialog
	}
	cmd := exec.Command("zenity", "--file-selection", "--title="+title,
		"--file-filter=Áudio WAV | *.wav *.WAV", "--file-filter=Todos os arquivos | *")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 { // cancelado
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
