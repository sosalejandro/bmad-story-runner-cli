// Package prompts is the §12.3 template+render layer. CLI invocations
// load a named template, resolve its slots from caller-provided data,
// and emit the rendered prompt to stdout for the orchestrator to pass
// to a dispatched L3 agent.
//
// Templates live at infrastructure/prompts/templates/*.tmpl, embedded
// at build time so the renderer has no filesystem dependency at runtime.
//
// Slot contract: each template's first comment block declares its
// required slot names. The renderer runs in strict mode (missing fields
// surface as errors at Execute time, not silently empty strings).
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Renderer is the immutable registry of parsed templates.
type Renderer struct {
	tmpl  *template.Template
	known map[string]bool
}

// NewRenderer parses every embedded template at construction time. Returns
// an error if any template fails to parse (catch-at-build-time semantics).
func NewRenderer() (*Renderer, error) {
	root := template.New("bmad-prompts").Option("missingkey=error")

	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("read embedded templates dir: %w", err)
	}

	known := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tmpl") {
			continue
		}
		raw, err := fs.ReadFile(templatesFS, "templates/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read template %s: %w", e.Name(), err)
		}
		name := strings.TrimSuffix(e.Name(), ".tmpl")
		if _, err := root.New(name).Parse(string(raw)); err != nil {
			return nil, fmt.Errorf("parse template %s: %w", e.Name(), err)
		}
		known[name] = true
	}
	return &Renderer{tmpl: root, known: known}, nil
}

// Known returns the alphabetically-sorted list of registered template names.
func (r *Renderer) Known() []string {
	out := make([]string, 0, len(r.known))
	for n := range r.known {
		out = append(out, n)
	}
	return out
}

// Render executes the named template with data and returns the rendered string.
// data is typically a map[string]any or a typed struct; field names are
// case-sensitive and access happens via text/template's dot syntax.
func (r *Renderer) Render(name string, data any) (string, error) {
	if !r.known[name] {
		return "", fmt.Errorf("unknown template %q (known: %v)", name, r.Known())
	}
	var buf bytes.Buffer
	if err := r.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return buf.String(), nil
}
