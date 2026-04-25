package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

func initTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	endpoint := getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector.monitoring.svc:4317")

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(getenv("SERVICE_NAME", "otel-demo")),
			semconv.ServiceVersion("1.0.0"),
		)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp, nil
}

func main() {
	ctx := context.Background()

	tp, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("failed to init tracer: %v", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(shutCtx); err != nil {
			log.Printf("tracer shutdown error: %v", err)
		}
	}()

	tracer := otel.Tracer("otel-demo")

	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "handle-ping",
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.path", r.URL.Path),
			),
		)
		defer span.End()

		traceID := span.SpanContext().TraceID().String()
		// Log format matches Loki derivedFields regex: traceID=(\w+)
		log.Printf("traceID=%s level=info msg=\"handled ping\" method=%s path=%s", traceID, r.Method, r.URL.Path)

		fmt.Fprintln(w, "pong")
	})

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	port := getenv("PORT", "8080")
	log.Printf("traceID=0000000000000000 level=info msg=\"otel-demo starting\" port=%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}