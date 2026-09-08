package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOperationExportsPrivateCorrelatedFailure(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(privateExporter{exporter}))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	var logs bytes.Buffer
	oldLog := slog.Default()
	slog.SetDefault(Logger(&logs))
	defer slog.SetDefault(oldLog)
	want := errors.New("private request body")
	err := Operation(context.Background(), "sync.run", func(ctx context.Context) error {
		_, span := otel.Tracer("test").Start(ctx, "http.client")
		span.SetAttributes(attribute.String("url.full", "https://private/?password=private"))
		span.RecordError(want)
		span.End()
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("operation error = %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 || spans[1].Status.Code != codes.Error {
		t.Fatalf("missing error spans: %+v", spans)
	}
	if len(spans[0].Attributes) != 0 || len(spans[0].Events) != 0 || spans[1].Status.Description != "" {
		t.Fatal("private span fields were exported")
	}
	if !strings.Contains(logs.String(), `"trace_id"`) || strings.Contains(logs.String(), "private") {
		t.Fatalf("unsafe or uncorrelated logs: %s", logs.String())
	}
}

func TestLoggerDropsBoundAndNumericPrivateAttributes(t *testing.T) {
	var logs bytes.Buffer
	logger := Logger(&logs).With("url", "private", "service", "test").WithGroup("job")
	logger.Error("failed", "error", errors.New("private"), "vehicle_id", 12345, "count", 2)
	output := logs.String()
	if strings.Contains(output, "private") || strings.Contains(output, "12345") ||
		!strings.Contains(output, `"count":2`) || !strings.Contains(output, `"service":"test"`) {
		t.Fatalf("incorrect log filtering: %s", output)
	}
}

func TestDisabledTelemetryDoesNotReplaceProvider(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://invalid.example:4318")
	previous := otel.GetTracerProvider()
	shutdown, err := Start(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	shutdown()
	if otel.GetTracerProvider() != previous {
		t.Fatal("disabled telemetry replaced global provider")
	}
}

func TestHTTPPreservesHijacker(t *testing.T) {
	server := httptest.NewServer(HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Hijacker); !ok {
			t.Error("middleware removed http.Hijacker")
		}
	})))
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestHTTPPanicAndStreamingInterfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) { panic("private panic") })
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("middleware removed http.Flusher")
		}
		w.WriteHeader(http.StatusOK)
	})
	for route, status := range map[string]int{"/panic": 500, "/stream": 200} {
		w := httptest.NewRecorder()
		HTTPHandler(mux).ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code != status {
			t.Errorf("%s status = %d", route, w.Code)
		}
	}
}
