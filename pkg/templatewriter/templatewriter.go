package templatewriter

import (
	"fmt"
	sprig "github.com/go-task/slim-sprig/v3"
	"io"
	"text/template"
)

type TemplateWriter struct {
	Name     string
	Template string
	Data     any
	Path     string
}

func (p *TemplateWriter) WriteToBuffer(w io.Writer) error {
	t, err := template.New(p.Name).Funcs(sprig.TxtFuncMap()).Parse(p.Template)
	if err != nil {
		return fmt.Errorf("failed to parse template for %s: %w", p.Name, err)
	}
	err = t.Execute(w, p.Data)
	if err != nil {
		return fmt.Errorf("failed to execute template for %s: %w", p.Name, err)
	}

	return nil
}
