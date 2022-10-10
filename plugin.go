package send

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/roadrunner-server/sdk/v3/utils"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	jprop "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	PluginName     string = "sendfile"
	ContentTypeKey string = "Content-Type"
	ContentTypeVal string = "application/octet-stream"
	xSendHeader    string = "X-Sendfile"
	bufSize        int    = 10 * 1024 * 1024 // 10MB chunks
)

type Plugin struct {
	log  *zap.Logger
	prop propagation.TextMapPropagator
}

func (p *Plugin) Init(log *zap.Logger) error {
	p.log = new(zap.Logger)
	*p.log = *log

	p.prop = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}, jprop.Jaeger{})
	return nil
}

// Middleware is HTTP plugin middleware to serve headers
func (p *Plugin) Middleware(next http.Handler) http.Handler { //nolint:gocognit
	// Define the http.HandlerFunc
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if val, ok := r.Context().Value(utils.OtelTracerNameKey).(string); ok {
			tp := trace.SpanFromContext(r.Context()).TracerProvider()
			ctx, span := tp.Tracer(val, trace.WithSchemaURL(semconv.SchemaURL),
				trace.WithInstrumentationVersion(otelhttp.SemVersion())).
				Start(r.Context(), PluginName, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			// inject
			p.prop.Inject(ctx, propagation.HeaderCarrier(r.Header))
			r = r.WithContext(ctx)
		}

		if path := r.Header.Get(xSendHeader); path != "" { //nolint:nestif
			defer func() {
				_ = r.Body.Close()
			}()

			// do not allow paths like ../../resource, security
			// only specified folder and resources in it
			// see: https://lgtm.com/rules/1510366186013/
			if strings.Contains(path, "..") {
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// check if the file exists
			fs, err := os.Stat(path)
			if err != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}

			f, err := os.OpenFile(path, os.O_RDONLY, 0)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer func() {
				_ = f.Close()
			}()

			size := fs.Size()
			var buf []byte
			// do not allocate large buffer for the small files
			if size < int64(bufSize) {
				// allocate exact size
				buf = make([]byte, size)
			} else {
				// allocate default 10mb buf
				buf = make([]byte, bufSize)
			}

			off := 0
			for {
				n, err := f.ReadAt(buf, int64(off))
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}

					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				buf = buf[:n]
				_, err = w.Write(buf)
				if err != nil {
					// we can't write response into the response writer
					p.log.Error("write response", zap.Error(err))
					return
				}

				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				off += n
			}

			r.Header.Set(ContentTypeKey, ContentTypeVal)
			r.Header.Del(xSendHeader)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) Name() string {
	return PluginName
}
