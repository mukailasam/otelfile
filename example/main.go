package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/mukailasam/otelfile" // Your local package path
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// initTracer initializes the OpenTelemetry pipeline using otelfile as the local exporter
func initTracer(serviceName string, filePath string) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	if serviceName == "" {
		serviceName = "unknown_service"
	}

	// Create our file-based exporter (keeps a rolling log of 50 traces)
	exporter := otelfile.NewFileExporter(filePath, 0)

	// Build the resource matching semconv patterns and SchemaURL correctly
	res, err := resource.New(context.Background(),
		resource.WithSchemaURL(semconv.SchemaURL), // Correctly passes the schema URL
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	// Register the exporter with the correct SimpleSpanProcessor setup
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)), // Correct method signature
		sdktrace.WithResource(res),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Establish Text Map Propagators (critical for context parsing across requests)
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	// Return the provider and a safe shutdown function
	shutdownFunc := func(ctx context.Context) error {
		if tp != nil {
			return tp.Shutdown(ctx)
		}
		return nil
	}

	return tp, shutdownFunc, nil
}

func main() {
	// Initialize otelfile tracing pipeline on payment-api service
	_, shutdown, err := initTracer("payment-api", "trace.html")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = shutdown(context.Background())
	}()

	// Grab our tracer
	tracer := otel.Tracer("http-server")

	// Setup local web server for endpoint manual testing
	http.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		// Extract incoming trace context from headers (like Postman / browser client headers)
		ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// Start the root span using the extracted context
		ctx, span := tracer.Start(ctx, "HTTP POST /checkout")
		defer span.End()

		span.SetAttributes(
			attribute.String("http.method", "POST"),
			attribute.String("checkout.cart_id", "cart_abc123"),
		)

		// Simulate Authorization child span
		func() {
			_, childSpan := tracer.Start(ctx, "AuthService.Authorize")
			defer childSpan.End()
			time.Sleep(time.Duration(30+rand.Intn(40)) * time.Millisecond)
		}()

		// Simulate Database child span
		func() {
			_, childSpan := tracer.Start(ctx, "DB SELECT cart_items")
			defer childSpan.End()
			childSpan.SetAttributes(
				attribute.String("db.system", "postgresql"),
				attribute.String("db.statement", "SELECT * FROM carts WHERE id = $1"),
			)
			time.Sleep(time.Duration(10+rand.Intn(50)) * time.Millisecond)
		}()

		// Simulate Payment gateway child span
		func() {
			_, childSpan := tracer.Start(ctx, "POST https://api.stripe.com/v3/charges")
			defer childSpan.End()
			time.Sleep(time.Duration(120+rand.Intn(100)) * time.Millisecond)
		}()

		w.Write([]byte("Order placed! Open trace.html in your browser."))
	})

	fmt.Println("Server running on http://localhost:8080")
	fmt.Println("Send requests to http://localhost:8080/checkout to generate trace.html")
	_ = http.ListenAndServe(":8080", nil)
}
