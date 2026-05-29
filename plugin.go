package send

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	rrcontext "github.com/roadrunner-server/context"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	jprop "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	PluginName     string = "sendfile"
	ContentTypeKey string = "Content-Type"
	ContentTypeVal string = "application/octet-stream"
	xSendHeader    string = "X-Sendfile"
	bufSize        int    = 10 * 1024 * 1024 // 10MB chunks
)

type Logger interface {
	NamedLogger(name string) *slog.Logger
}

type Plugin struct {
	log         *slog.Logger
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

		var otelVal string
		var tp trace.TracerProvider

		if val, ok := r.Context().Value(rrcontext.OtelTracerNameKey).(string); ok {
			otelVal = val
			tp = trace.SpanFromContext(r.Context()).TracerProvider()
			var ctx context.Context
			ctx, span := tp.Tracer(val, trace.WithSchemaURL(semconv.SchemaURL),
				trace.WithInstrumentationVersion(otelhttp.Version)).
				Start(r.Context(), PluginName, trace.WithSpanKind(trace.SpanKindInternal))

			// inject
			p.prop.Inject(ctx, propagation.HeaderCarrier(r.Header))
			r = r.WithContext(ctx)
			span.End()
		}

		next.ServeHTTP(rrWriter, r)

		// start a second span for the post-processing (X-Sendfile handling)
		var postSpan trace.Span
		if otelVal != "" {
			_, postSpan = tp.Tracer(otelVal, trace.WithSchemaURL(semconv.SchemaURL),
				trace.WithInstrumentationVersion(otelhttp.Version)).
				Start(r.Context(), PluginName+":post", trace.WithSpanKind(trace.SpanKindInternal))
		}
		defer func() {
			if postSpan != nil {
				postSpan.End()
			}
		}()

		// capture the X-Sendfile path once
		path := rrWriter.Header().Get(xSendHeader)

		// if there is no X-Sendfile header from the PHP worker, just return
		if path == "" {
			// re-add all headers from the worker
			addHeaders(w.Header(), rrWriter.hdrToSend)

			// write original
			w.WriteHeader(rrWriter.code)
			if len(rrWriter.data) > 0 {
				// write a body if exists
				_, err := w.Write(rrWriter.data)
				if err != nil {
					p.log.Error("failed to write data to the response", "error", err)
				}
			}

			return
		}

		// delete the original X-Sendfile header
		rrWriter.Header().Del(xSendHeader)

		// re-add original headers
		addHeaders(w.Header(), rrWriter.hdrToSend)

		// do not allow paths like ../../resource, security
		// only specified folder and resources in it
		// see: https://lgtm.com/rules/1510366186013/
		if strings.Contains(filepath.Clean(path), "..") {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		// check if the file exists
		fs, err := os.Stat(path)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		f, err := os.Open(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		// set content-type before writing any data
		w.Header().Set(ContentTypeKey, ContentTypeVal)

		size := fs.Size()
		buf := make([]byte, min(size, int64(bufSize)))

		off := 0
		for {
			buf = buf[:cap(buf)]
			n, err := f.ReadAt(buf, int64(off))
			if err != nil {
				if errors.Is(err, io.EOF) {
					if n > 0 {
						_, werr := w.Write(buf[:n])
						if werr != nil {
							p.log.Error("write response", "error", werr)
						}
					}
					break
				}

				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			_, werr := w.Write(buf[:n])
			if werr != nil {
				// we can't write response into the response writer
				p.log.Error("write response", "error", werr)
				return
			}

			// send data to the user
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			off += n
		}
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
	w.data = w.data[:0]
	clear(w.hdrToSend)
	p.writersPool.Put(w)
}

// addHeaders copies every value of the worker-supplied headers into dst.
func addHeaders(dst http.Header, src map[string][]string) {
	for k, vals := range src {
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}
