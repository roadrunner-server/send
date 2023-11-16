module github.com/roadrunner-server/send/v4

go 1.21

toolchain go1.21.0

require (
	github.com/roadrunner-server/sdk/v4 v4.5.3
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.46.0
	go.opentelemetry.io/contrib/propagators/jaeger v1.21.1
	go.opentelemetry.io/otel v1.21.0
	go.opentelemetry.io/otel/trace v1.21.0
	go.uber.org/zap v1.26.0
)

require (
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.3.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/roadrunner-server/errors v1.3.0 // indirect
	github.com/roadrunner-server/tcplisten v1.4.0 // indirect
	go.opentelemetry.io/otel/metric v1.21.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
)
