package ffmpeg

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// LocalStreamServer acts as an internal loopback bridge, allowing FFmpeg to securely
// access in-memory data streams via HTTP Range requests.
type LocalStreamServer struct {
	server   *http.Server
	listener net.Listener
	sessions map[string]*StreamSession
	mu       sync.RWMutex // Protects the sessions map from concurrent map writes/reads
	logger   *slog.Logger
	baseURL  string // The dynamic base URL of the local server
	cancel   context.CancelFunc
}

type StreamSession struct {
	id        string
	readerAt  io.ReaderAt // Replaces io.ReadSeeker to allow stateless concurrent reads
	size      int64       // Required for io.NewSectionReader
	token     string
	expiresAt time.Time
	spoolPath string // Disk spool backing readerAt when the stream was spooled; empty for pass-through readers
}

// NewLocalStreamServer initializes and starts the internal server.
func NewLocalStreamServer(logger *slog.Logger) (*LocalStreamServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind local stream server: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ls := &LocalStreamServer{
		listener: listener,
		sessions: make(map[string]*StreamSession),
		logger:   logger,
		baseURL:  fmt.Sprintf("http://%s", listener.Addr().String()),
		cancel:   cancel,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /stream/{id}", ls.handleStream)

	ls.server = &http.Server{Handler: mux}

	go func() {
		if err := ls.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("Local stream server crashed", "error", err)
		}
	}()

	go ls.startSweeper(ctx)

	logger.Info("Internal loopback server started", "url", ls.baseURL)
	return ls, nil
}

// Shutdown gracefully closes the internal server.
func (l *LocalStreamServer) Shutdown(ctx context.Context) error {
	l.cancel()
	return l.server.Shutdown(ctx)
}

// isConcurrentSafeReaderAt checks whether the stream is a known concurrent-safe io.ReaderAt
// implementation. Network-backed streams such as *minio.Object implement io.ReaderAt by mutating
// internal offset/HTTP reader state without synchronization, making concurrent HTTP Range requests
// from FFmpeg unsafe unless buffered.
func isConcurrentSafeReaderAt(stream io.ReadSeeker) (io.ReaderAt, bool) {
	switch r := stream.(type) {
	case *bytes.Reader:
		return r, true
	case *strings.Reader:
		return r, true
	case *io.SectionReader:
		return r, true
	case *os.File:
		return r, true
	default:
		return nil, false
	}
}

// Register securely mounts an in-memory stream and returns its unique ID and full FFmpeg-ready URL.
func (l *LocalStreamServer) Register(stream io.ReadSeeker, ttl time.Duration) (string, string, error) {
	id := generateRandomHex(16)
	token := generateRandomHex(16)

	var readerAt io.ReaderAt
	var size int64
	var spoolPath string

	if safeReaderAt, ok := isConcurrentSafeReaderAt(stream); ok {
		endPos, err := stream.Seek(0, io.SeekEnd)
		if err != nil {
			return "", "", fmt.Errorf("failed to determine stream size: %w", err)
		}
		if _, err := stream.Seek(0, io.SeekStart); err != nil {
			return "", "", fmt.Errorf("failed to rewind stream: %w", err)
		}
		readerAt = safeReaderAt
		size = endPos
	} else {
		// Fallback: For non-ReaderAt or non-concurrent-safe streams (e.g., *minio.Object),
		// rewind if possible and spool to a temporary file on disk so it becomes a
		// concurrent-safe *os.File. This avoids holding the whole file in RAM for the
		// lifetime of the session while still supporting concurrent Range reads via ReadAt.
		_, _ = stream.Seek(0, io.SeekStart)
		l.logger.Debug("Spooling stream to disk for concurrent-safe ReaderAt access")
		spoolFile, err := os.CreateTemp("", "mh-streamspool-*")
		if err != nil {
			return "", "", fmt.Errorf("failed to create stream spool file: %w", err)
		}
		spoolPath = spoolFile.Name()
		written, err := io.Copy(spoolFile, stream)
		if err != nil {
			spoolFile.Close()
			os.Remove(spoolPath)
			return "", "", fmt.Errorf("failed to spool stream to disk: %w", err)
		}
		if _, err := spoolFile.Seek(0, io.SeekStart); err != nil {
			spoolFile.Close()
			os.Remove(spoolPath)
			return "", "", fmt.Errorf("failed to rewind stream spool file: %w", err)
		}
		readerAt = spoolFile
		size = written
	}

	session := &StreamSession{
		id:        id,
		readerAt:  readerAt,
		size:      size,
		token:     token,
		expiresAt: time.Now().Add(ttl),
		spoolPath: spoolPath,
	}

	l.mu.Lock()
	l.sessions[id] = session
	l.mu.Unlock()

	fullURL := fmt.Sprintf("%s/stream/%s?token=%s", l.baseURL, id, token)
	return id, fullURL, nil
}

func (l *LocalStreamServer) Unregister(id string) {
	l.mu.Lock()
	session := l.sessions[id]
	delete(l.sessions, id)
	l.mu.Unlock()

	if session != nil {
		removeSpoolFile(session)
	}
}

// removeSpoolFile closes and deletes the temporary spool file backing a session,
// if any. Sessions whose readerAt is a pass-through (owned by the caller) are left untouched.
func removeSpoolFile(session *StreamSession) {
	if session.spoolPath == "" {
		return
	}
	if spoolFile, ok := session.readerAt.(*os.File); ok {
		spoolFile.Close()
	}
	os.Remove(session.spoolPath)
}

// handleStream is the internal endpoint that FFmpeg requests data from.
func (l *LocalStreamServer) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := r.URL.Query().Get("token")

	l.mu.RLock()
	session, exists := l.sessions[id]
	l.mu.RUnlock()

	if !exists || session.token != token {
		http.Error(w, "Unauthorized or Stream Not Found", http.StatusUnauthorized)
		return
	}

	if time.Now().After(session.expiresAt) {
		http.Error(w, "Stream Session Expired", http.StatusGone)
		return
	}

	// Give THIS specific HTTP request its own independent cursor!
	// This prevents the race condition when FFmpeg opens multiple concurrent threads.
	independentStream := io.NewSectionReader(session.readerAt, 0, session.size)

	http.ServeContent(w, r, "stream.bin", time.Time{}, independentStream)
}

func (l *LocalStreamServer) startSweeper(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			for id, session := range l.sessions {
				if now.After(session.expiresAt) {
					delete(l.sessions, id)
					removeSpoolFile(session)
					l.logger.Debug("Sweeper removed expired stream session", "id", id)
				}
			}
			l.mu.Unlock()
		}
	}
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
