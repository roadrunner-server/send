module github.com/roadrunner-server/send/v4

go 1.20

require (
	github.com/roadrunner-server/sdk/v4 v4.2.4
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.41.1
	go.opentelemetry.io/contrib/propagators/jaeger v1.16.1
	go.opentelemetry.io/otel v1.15.1
	go.opentelemetry.io/otel/trace v1.15.1
	go.uber.org/zap v1.24.0
)

require (
	github.com/felixge/httpsnoop v1.0.3 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/roadrunner-server/errors v1.2.0 // indirect
	github.com/roadrunner-server/tcplisten v1.3.0 // indirect
	go.opentelemetry.io/otel/metric v0.38.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
)
