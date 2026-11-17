package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Set ajusta um parâmetro a partir de "modulo.param=valor" (ex.: "pre.gain=8").
func (c *Chain) Set(expr string) error {
	k, v, ok := strings.Cut(strings.TrimSpace(expr), "=")
	mod, par, ok2 := strings.Cut(strings.TrimSpace(k), ".")
	if !ok || !ok2 {
		return fmt.Errorf("use modulo.param=valor, veio %q", expr)
	}
	m := c.Module(mod)
	if m == nil {
		return fmt.Errorf("módulo desconhecido %q", mod)
	}
	p := m.Param(par)
	if p == nil {
		return fmt.Errorf("%s não tem o parâmetro %q", mod, par)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return fmt.Errorf("%s: valor inválido %q", k, v)
	}
	p.Set(f)
	return nil
}

// SetList aplica vários "modulo.param=valor" separados por vírgula ou espaço.
func (c *Chain) SetList(list string) error {
	for _, kv := range SplitList(list) {
		if err := c.Set(kv); err != nil {
			return err
		}
	}
	return nil
}

// EnableList liga (on=true) ou desliga os módulos de uma lista separada por vírgula ou espaço.
func (c *Chain) EnableList(list string, on bool) error {
	for _, id := range SplitList(list) {
		if err := c.SetEnabled(id, on); err != nil {
			return err
		}
	}
	return nil
}

// SplitList separa uma lista por vírgulas e espaços, ignorando itens vazios.
func SplitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}
