package ffmpeg

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

type blockingResponseWriter struct {
	mu           sync.Mutex
	header       http.Header
	statusCode   int
	body         bytes.Buffer
	writeStarted chan struct{}
	startOnce    sync.Once
	allowFinish  chan struct{}
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{
		header:       make(http.Header),
		writeStarted: make(chan struct{}),
		allowFinish:  make(chan struct{}),
	}
}

func (w *blockingResponseWriter) Header() http.Header {
	return w.header
}

func (w *blockingResponseWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
}

func (w *blockingResponseWriter) Write(p []byte) (int, error) {
	w.startOnce.Do(func() {
		close(w.writeStarted)
	})
	<-w.allowFinish
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.body.Write(p)
}

func TestLocalStreamServer_ConcurrentHandleStreamAndUnregister(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := NewLocalStreamServer(logger)
	if err != nil {
		t.Fatalf("failed to start LocalStreamServer: %v", err)
	}
	defer srv.Shutdown(context.Background())

	payload := []byte("concurrent-stream-spool-payload")
	stream := &nonConcurrentReaderAt{Reader: bytes.NewReader(payload)}
	id, rawURL, err := srv.Register(context.Background(), stream, time.Minute)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse session URL: %v", err)
	}

	srv.mu.RLock()
	session := srv.sessions[id]
	srv.mu.RUnlock()
	if session == nil || session.spoolPath == "" {
		t.Fatalf("expected non-empty spoolPath on registered session")
	}
	spoolPath := session.spoolPath

	bw := newBlockingResponseWriter()
	req := httptest.NewRequest(http.MethodGet, parsedURL.RequestURI(), nil)
	req.SetPathValue("id", id)

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		srv.handleStream(bw, req)
	}()

	select {
	case <-bw.writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handleStream to start writing")
	}

	// Unregister while handleStream is actively serving content.
	srv.Unregister(id)

	// The spool file must remain on disk while the active handler still holds a reference.
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("expected spool file %q to remain readable while handleStream is active, got err: %v", spoolPath, err)
	}

	// Subsequent requests for the unregistered session must be rejected.
	rejectRec := httptest.NewRecorder()
	rejectReq := httptest.NewRequest(http.MethodGet, parsedURL.RequestURI(), nil)
	rejectReq.SetPathValue("id", id)
	srv.handleStream(rejectRec, rejectReq)
	if rejectRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unregistered session, got %d", rejectRec.Code)
	}

	close(bw.allowFinish)
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handleStream to finish")
	}

	if bw.statusCode != http.StatusOK {
		t.Errorf("expected HTTP 200, got %d", bw.statusCode)
	}
	if !bytes.Equal(bw.body.Bytes(), payload) {
		t.Errorf("expected payload %q, got %q", string(payload), bw.body.String())
	}

	// Once handleStream returns, the last ref drops and the spool file must be deleted.
	if _, err := os.Stat(spoolPath); !os.IsNotExist(err) {
		t.Fatalf("expected spool file %q to be deleted after handleStream completes, stat err: %v", spoolPath, err)
	}
}

func TestLocalStreamServer_ShutdownRemovesActiveSpoolFiles(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := NewLocalStreamServer(logger)
	if err != nil {
		t.Fatalf("failed to start LocalStreamServer: %v", err)
	}

	var spoolPaths []string
	for i := 0; i < 2; i++ {
		stream := &nonConcurrentReaderAt{Reader: bytes.NewReader([]byte("spool-data"))}
		id, _, err := srv.Register(context.Background(), stream, time.Minute)
		if err != nil {
			t.Fatalf("Register %d failed: %v", i, err)
		}
		srv.mu.RLock()
		spoolPath := srv.sessions[id].spoolPath
		srv.mu.RUnlock()
		if spoolPath == "" {
			t.Fatalf("expected spoolPath for session %d", i)
		}
		if _, err := os.Stat(spoolPath); err != nil {
			t.Fatalf("expected spool file %q to exist before Shutdown: %v", spoolPath, err)
		}
		spoolPaths = append(spoolPaths, spoolPath)
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	for _, p := range spoolPaths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected spool file %q to be removed by Shutdown, stat err: %v", p, err)
		}
	}

	srv.mu.RLock()
	remaining := len(srv.sessions)
	srv.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("expected 0 sessions remaining after Shutdown, got %d", remaining)
	}

	// Registering after Shutdown must fail without leaving behind a spool file.
	if _, _, err := srv.Register(context.Background(), &nonConcurrentReaderAt{Reader: bytes.NewReader([]byte("after-shutdown"))}, time.Minute); err == nil {
		t.Fatal("expected Register after Shutdown to return an error")
	}
}
