package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestTelemetryTemplateIsOptInAndEscapesEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", `https://collector.example/collect/test?x="<script>`} {
		t.Setenv("FARO_COLLECTOR_URL", endpoint)
		var output bytes.Buffer
		page := pages["message"]
		if page == nil {
			t.Fatal("message template missing")
		}
		if err := page.ExecuteTemplate(&output, "layout", map[string]any{}); err != nil {
			t.Fatal(err)
		}
		body := output.String()
		if strings.Contains(body, `src="/assets/telemetry.js"`) != (endpoint != "") {
			t.Fatal("telemetry opt-in missing")
		}
		if strings.Contains(body, endpoint) && endpoint != "" {
			t.Fatal("collector URL was not HTML escaped")
		}
	}
}
