package preview

import (
	"embed"
	"fmt"
	"html/template"
	"io"
)

//go:embed preview.html
var files embed.FS

func HTML(w io.Writer, v View) error {
	t, err := template.New("preview.html").Funcs(template.FuncMap{
		"css": func(s Sample) template.CSS { return template.CSS(s.CSS) },
		"dim": func(s Sample) template.CSS { return template.CSS(s.DimCSS) },
		"rgb": func(s Sample) template.CSS { return template.CSS(fmt.Sprintf("rgb(%d %d %d)", s.R, s.G, s.B)) },
	}).ParseFS(files, "preview.html")
	if err != nil {
		return err
	}
	return t.Execute(w, v)
}
