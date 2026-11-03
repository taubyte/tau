package instance

import (
	"reflect"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

// otelGlobal is the package otel's default provider lives in. Until a process
// installs a provider, otel.Tracer answers through that one, which takes one
// mutex for the whole process on every call.
const otelGlobal = "go.opentelemetry.io/otel/internal/global"

// installTracerProvider gives the process a no-op tracer provider unless it
// already has a provider of its own. The blockservice under every store
// starts a span on each block it gets, adds or deletes, so with otel's
// default every block operation in the process queues on that mutex.
func installTracerProvider() {
	if isOtelDefault(otel.GetTracerProvider()) {
		otel.SetTracerProvider(noop.NewTracerProvider())
	}
}

func isOtelDefault(tp any) bool {
	t := reflect.TypeOf(tp)
	return t.Kind() == reflect.Pointer && t.Elem().PkgPath() == otelGlobal
}
