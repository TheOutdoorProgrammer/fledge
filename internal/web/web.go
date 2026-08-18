// Package web renders Fledge's browser-facing pages.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:generate go run ./assets/generate -out ./assets
//go:embed templates/*.html assets/*.png
var templateFS embed.FS

// pages holds one fully parsed template set per page. They cannot share a set:
// html/template keeps a single namespace, so every page defining "body" would
// overwrite the last one parsed rather than sit beside it.
var pages = parsePages()

func parsePages() map[string]*template.Template {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		panic("web: read templates: " + err.Error())
	}

	parsed := make(map[string]*template.Template, len(entries))
	for _, entry := range entries {
		if entry.Name() == "layout.html" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".html")
		parsed[name] = template.Must(
			template.New("layout.html").Funcs(helpers()).
				ParseFS(templateFS, "templates/layout.html", "templates/"+entry.Name()),
		)
	}

	return parsed
}

// Render writes a page, buffering first so a template error produces an error
// rather than a half-written 200.
func Render(w http.ResponseWriter, status int, name string, data any) {
	page, ok := pages[name]
	if !ok {
		http.Error(w, "template error: no page "+name, http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := page.ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// Asset returns one generated browser asset without exposing the embedded
// filesystem outside this package.
func Asset(name string) ([]byte, error) {
	if name == "" || path.Base(name) != name {
		return nil, fmt.Errorf("web: invalid asset name %q", name)
	}
	return templateFS.ReadFile("assets/" + name)
}

func helpers() template.FuncMap {
	return template.FuncMap{
		"bytes":    humanBytes,
		"since":    humanSince,
		"until":    humanUntil,
		"shortSHA": func(s string) string { return truncate(s, 12) },
	}
}

func humanBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGT"[exp])
}

func humanSince(t time.Time) string {
	return humanDuration(time.Since(t)) + " ago"
}

// humanUntil describes a future instant, and says so plainly when it is in fact
// already past.
func humanUntil(t time.Time) string {
	remaining := time.Until(t)
	if remaining <= 0 {
		return humanDuration(-remaining) + " ago"
	}
	return "in " + humanDuration(remaining)
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	default:
		return plural(int(d.Hours()/24/30), "month")
	}
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
