package send

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	rrcontext "github.com/roadrunner-server/context"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	jprop "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
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

type Logger interface {
	NamedLogger(name string) *zap.Logger
}

type Plugin struct {
	log         *zap.Logger
	writersPool sync.Pool
	prop        propagation.TextMapPropagator
}

func (p *Plugin) Init(log Logger) error {
	p.log = log.NamedLogger(PluginName)

	p.writersPool = sync.Pool{
		New: func() any {
			wr := new(writer)
			wr.code = http.StatusOK
			wr.data = make([]byte, 0, 10)
			wr.hdrToSend = make(map[string][]string, 2)
			return wr
		},
	}

	p.prop = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}, jprop.Jaeger{})
	return nil
}

// Middleware is an HTTP plugin middleware to serve headers
func (p *Plugin) Middleware(next http.Handler) http.Handler {
	// Define the http.HandlerFunc
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rrWriter := p.getWriter()
		defer func() {
			p.putWriter(rrWriter)
			_ = r.Body.Close()
		}()

		if val, ok := r.Context().Value(rrcontext.OtelTracerNameKey).(string); ok {
			tp := trace.SpanFromContext(r.Context()).TracerProvider()
			ctx, span := tp.Tracer(val, trace.WithSchemaURL(semconv.SchemaURL),
				trace.WithInstrumentationVersion(otelhttp.Version())).
				Start(r.Context(), PluginName, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			// inject
			p.prop.Inject(ctx, propagation.HeaderCarrier(r.Header))
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(rrWriter, r)

		// if there is no X-Sendfile header from the PHP worker, just return
		if path := rrWriter.Header().Get(xSendHeader); path == "" { //nolint:nestif
			for k := range rrWriter.hdrToSend {
				for kk := range rrWriter.hdrToSend[k] {
					// re-add all headers from the worker
					w.Header().Add(k, rrWriter.hdrToSend[k][kk])
				}
			}

			// write original
			w.WriteHeader(rrWriter.code)
			if len(rrWriter.data) > 0 {
				// write a body if exists
				_, err := w.Write(rrWriter.data)
				if err != nil {
					p.log.Error("failed to write data to the response", zap.Error(err))
				}
			}

			return
		}

		// we already checked that that header exists
		path := rrWriter.Header().Get(xSendHeader)
		// delete the original X-Sendfile header
		rrWriter.Header().Del(xSendHeader)

		// re-add original headers
		for k := range rrWriter.hdrToSend {
			for kk := range rrWriter.hdrToSend[k] {
				// re-add all headers from the worker
				w.Header().Add(k, rrWriter.hdrToSend[k][kk])
			}
		}

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
					if n > 0 {
						goto out
					}

					break
				}

				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

		out:
			buf = buf[:n]
			_, err = w.Write(buf)
			if err != nil {
				// we can't write response into the response writer
				p.log.Error("write response", zap.Error(err))
				return
			}

			// send data to the user
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			off += n
		}

		w.Header().Set(ContentTypeKey, ContentTypeVal)
	})
}

func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) getWriter() *writer {
	return p.writersPool.Get().(*writer)
}

func (p *Plugin) putWriter(w *writer) {
	w.code = http.StatusOK
	w.data = make([]byte, 0, 10)

	for k := range w.hdrToSend {
		delete(w.hdrToSend, k)
	}

	p.writersPool.Put(w)
}
