package send

import (
	"net/http"
)

type writer struct {
	code      int
	data      []byte
	hdrToSend map[string][]string
}

func (w *writer) WriteHeader(code int) {
	w.code = code
}

func (w *writer) Write(b []byte) (int, error) {
	w.data = append(w.data, b...)
	return len(b), nil
}

func (w *writer) Header() http.Header {
	return w.hdrToSend
}
