package send

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type testLogger struct{ log *slog.Logger }

func (l testLogger) NamedLogger(string) *slog.Logger { return l.log }

type chunkWriter struct {
	*httptest.ResponseRecorder
	writes  []int
	flushes int
	failOn  int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, len(p))
	if len(w.writes) == w.failOn {
		return 0, errors.New("response write failed")
	}
	return w.ResponseRecorder.Write(p)
}

func (w *chunkWriter) Flush() {
	w.flushes++
	w.ResponseRecorder.Flush()
}

func TestSendfileChunks(t *testing.T) {
	const chunk = 10 * 1024 * 1024
	for _, tc := range []struct {
		name        string
		size        int
		failOn      int
		noFlusher   bool
		wantWrites  []int
		wantBytes   int
		wantFlushes int
	}{
		{name: "empty file"},
		{name: "small file", size: 17, wantWrites: []int{17}, wantBytes: 17, wantFlushes: 1},
		{name: "exact chunk", size: chunk, wantWrites: []int{chunk}, wantBytes: chunk, wantFlushes: 1},
		{name: "partial last chunk", size: chunk + 17, wantWrites: []int{chunk, 17}, wantBytes: chunk + 17, wantFlushes: 2},
		{name: "two full chunks", size: 2 * chunk, wantWrites: []int{chunk, chunk}, wantBytes: 2 * chunk, wantFlushes: 2},
		{name: "full chunk write error", size: chunk + 17, failOn: 1, wantWrites: []int{chunk}},
		{name: "partial chunk write error", size: chunk + 17, failOn: 2, wantWrites: []int{chunk, 17}, wantBytes: chunk, wantFlushes: 1},
		{name: "writer without flush", size: chunk + 17, noFlusher: true, wantWrites: []int{chunk, 17}, wantBytes: chunk + 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pattern := []byte("0123456789abcdef\n")
			body := bytes.Repeat(pattern, tc.size/len(pattern)+1)[:tc.size]
			name := filepath.Join(t.TempDir(), "file.bin")
			if err := os.WriteFile(name, body, 0o600); err != nil {
				t.Fatal(err)
			}

			var logs bytes.Buffer
			p := &Plugin{}
			if err := p.Init(testLogger{slog.New(slog.NewTextHandler(&logs, nil))}); err != nil {
				t.Fatal(err)
			}
			h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Sendfile", name)
				w.Header().Add("X-Worker", "first")
				w.Header().Add("X-Worker", "second")
			}))

			rec := &chunkWriter{ResponseRecorder: httptest.NewRecorder(), failOn: tc.failOn}
			var w http.ResponseWriter = rec
			if tc.noFlusher {
				w = struct{ http.ResponseWriter }{rec}
			}
			h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

			if !slices.Equal(rec.writes, tc.wantWrites) {
				t.Errorf("write sizes: got %v, want %v", rec.writes, tc.wantWrites)
			}
			if rec.flushes != tc.wantFlushes {
				t.Errorf("flush count: got %d, want %d", rec.flushes, tc.wantFlushes)
			}
			if !bytes.Equal(rec.Body.Bytes(), body[:tc.wantBytes]) {
				t.Errorf("response body differs: got %d bytes, want %d", rec.Body.Len(), tc.wantBytes)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
			}
			if rec.Header().Get("Content-Type") != "application/octet-stream" {
				t.Errorf("content type: got %q", rec.Header().Get("Content-Type"))
			}
			if rec.Header().Get("X-Sendfile") != "" {
				t.Error("response contains X-Sendfile")
			}
			if !slices.Equal(rec.Header().Values("X-Worker"), []string{"first", "second"}) {
				t.Errorf("worker headers: got %v", rec.Header().Values("X-Worker"))
			}
			if got := bytes.Count(logs.Bytes(), []byte("write response")); got != min(tc.failOn, 1) {
				t.Errorf("write error logs: got %d, want %d", got, min(tc.failOn, 1))
			}
		})
	}
}
