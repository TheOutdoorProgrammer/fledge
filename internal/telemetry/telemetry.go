package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func Start(ctx context.Context, service string) (func(), error) {
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") ||
		(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "") {
		return func() {}, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx, resource.WithTelemetrySDK(),
		resource.WithAttributes(attribute.String("service.name", service)), resource.WithFromEnv())
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(privateExporter{exporter}))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("telemetry export failed", "error_type", ErrorType(err))
	}))
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			slog.Error("telemetry shutdown failed", "error_type", ErrorType(err))
		}
	}, nil
}

func Logger(output io.Writer) *slog.Logger {
	return slog.New(LogHandler(slog.NewJSONHandler(output, nil)))
}

func LogHandler(next slog.Handler) slog.Handler { return traceHandler{next} }

type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	filtered := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		if err, ok := attr.Value.Any().(error); ok {
			filtered.AddAttrs(slog.String("error_type", ErrorType(err)))
			return true
		}
		switch attr.Value.Kind() {
		case slog.KindBool, slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindDuration:
			switch attr.Key {
			case "http_status", "duration_ms", "attempt", "max_attempts", "status", "count", "signals",
				"destinations", "signature_validation", "dry_run", "removed", "kept", "repos",
				"created", "updated", "skipped", "failed", "errors", "processed", "total", "calls":
				filtered.AddAttrs(attr)
			}
		case slog.KindString:
			switch attr.Key {
			case "operation", "error_type", "http_route", "version", "service":
				filtered.AddAttrs(attr)
			}
		}
		return true
	})
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		filtered.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	}
	return h.Handler.Handle(ctx, filtered)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return boundHandler{traceHandler: h, attrs: attrs}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

type boundHandler struct {
	traceHandler
	attrs []slog.Attr
}

func (h boundHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(h.attrs...)
	return h.traceHandler.Handle(ctx, record)
}

func (h boundHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return boundHandler{traceHandler: h.traceHandler, attrs: combined}
}

func (h boundHandler) WithGroup(name string) slog.Handler {
	return boundHandler{traceHandler: traceHandler{h.Handler.WithGroup(name)}, attrs: h.attrs}
}

func ErrorType(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "timeout"
	}
	return fmt.Sprintf("%T", err)
}

func RecordError(ctx context.Context, operation string, err error) {
	if err == nil {
		return
	}
	kind := ErrorType(err)
	span := trace.SpanFromContext(ctx)
	span.SetStatus(codes.Error, "")
	span.SetAttributes(attribute.String("error.type", kind))
	slog.ErrorContext(ctx, "operation failed", "operation", operation, "error_type", kind)
}

func Operation(ctx context.Context, name string, run func(context.Context) error) (err error) {
	ctx, span := otel.Tracer("application").Start(ctx, name)
	defer span.End()
	defer func() {
		if recover() != nil {
			err = errors.New("operation panicked")
		}
		if err != nil {
			RecordError(ctx, name, err)
		} else {
			slog.InfoContext(ctx, "operation completed", "operation", name)
		}
	}()
	return run(ctx)
}

func HTTPHandler(next http.Handler) http.Handler {
	return otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics := httpsnoop.CaptureMetrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recover() != nil {
					RecordError(r.Context(), "http.panic", errors.New("handler panicked"))
					http.Error(w, "internal error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		}), w, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("http.route", route))
		level := slog.LevelInfo
		if metrics.Code >= 500 {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "http request completed", "http_route", route,
			"http_status", metrics.Code, "duration_ms", metrics.Duration.Milliseconds())
	}), "http.server", otelhttp.WithFilter(func(r *http.Request) bool {
		return r.URL.Path != "/health" && r.URL.Path != "/healthz"
	}))
}

func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: timeout}
}

type privateExporter struct{ sdktrace.SpanExporter }

func (e privateExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	filtered := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		filtered[i] = privateSpan{span}
	}
	return e.SpanExporter.ExportSpans(ctx, filtered)
}

type privateSpan struct{ sdktrace.ReadOnlySpan }

func (s privateSpan) SpanContext() trace.SpanContext {
	return s.ReadOnlySpan.SpanContext().WithTraceState(trace.TraceState{})
}

func (s privateSpan) Parent() trace.SpanContext {
	return s.ReadOnlySpan.Parent().WithTraceState(trace.TraceState{})
}

// HTTP instrumentation includes private URLs and caller-controlled attributes.
func (s privateSpan) Attributes() []attribute.KeyValue {
	var safe []attribute.KeyValue
	for _, attr := range s.ReadOnlySpan.Attributes() {
		switch attr.Key {
		case "http.route", "http.request.method", "http.method", "http.response.status_code", "http.status_code",
			"http.request.body.size", "http.response.body.size", "network.protocol.version", "error.type":
			safe = append(safe, attr)
		}
	}
	return safe
}

func (s privateSpan) Status() sdktrace.Status {
	return sdktrace.Status{Code: s.ReadOnlySpan.Status().Code}
}

func (s privateSpan) Events() []sdktrace.Event { return nil }
func (s privateSpan) Links() []sdktrace.Link   { return nil }
