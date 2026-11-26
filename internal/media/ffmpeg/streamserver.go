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
	sessions map[string]*StreamSession
	mu       sync.RWMutex // Protects the sessions map, closed state, and session reference counts
	closed   bool
	logger   *slog.Logger
	baseURL  string // The dynamic base URL of the local server
	cancel   context.CancelFunc
}

type StreamSession struct {
	readerAt  io.ReaderAt // Replaces io.ReadSeeker to allow stateless concurrent reads
	size      int64       // Required for io.NewSectionReader
	token     string
	expiresAt time.Time
	spoolPath string // Disk spool backing readerAt when the stream was spooled; empty for pass-through readers
	refs      int    // Protected by LocalStreamServer.mu; initial 1 for registration + 1 per active handler
	closed    bool   // Protected by LocalStreamServer.mu; true once detached by Unregister, sweeper, or Shutdown
}

// NewLocalStreamServer initializes and starts the internal server.
func NewLocalStreamServer(logger *slog.Logger) (*LocalStreamServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind local stream server: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ls := &LocalStreamServer{
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

// Shutdown gracefully closes the internal server and removes any remaining spool files.
func (l *LocalStreamServer) Shutdown(ctx context.Context) error {
	l.cancel()
	err := l.server.Shutdown(ctx)

	var toRemove []*StreamSession
	l.mu.Lock()
	l.closed = true
	for id, session := range l.sessions {
		if l.closeSessionLocked(id, session) {
			toRemove = append(toRemove, session)
		}
	}
	l.mu.Unlock()

	for _, session := range toRemove {
		removeSpoolFile(session)
	}
	return err
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

// contextReader wraps an io.Reader and aborts reads when ctx is cancelled.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr contextReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.r.Read(p)
}

// Register securely mounts an in-memory stream and returns its unique ID and full FFmpeg-ready URL.
func (l *LocalStreamServer) Register(ctx context.Context, stream io.ReadSeeker, ttl time.Duration) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}

	id, err := generateRandomHex(16)
	if err != nil {
		return "", "", err
	}
	token, err := generateRandomHex(16)
	if err != nil {
		return "", "", err
	}

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
		written, err := io.Copy(spoolFile, contextReader{ctx: ctx, r: stream})
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
		readerAt:  readerAt,
		size:      size,
		token:     token,
		expiresAt: time.Now().Add(ttl),
		spoolPath: spoolPath,
		refs:      1,
	}

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		removeSpoolFile(session)
		return "", "", fmt.Errorf("local stream server is shut down")
	}
	l.sessions[id] = session
	l.mu.Unlock()

	fullURL := fmt.Sprintf("%s/stream/%s?token=%s", l.baseURL, id, token)
	return id, fullURL, nil
}

// closeSessionLocked detaches a session from the active map, marks it closed,
// and drops its registration reference. The caller must hold l.mu and remove the
// spool file outside l.mu if it returns true.
func (l *LocalStreamServer) closeSessionLocked(id string, session *StreamSession) bool {
	delete(l.sessions, id)
	session.closed = true
	session.refs--
	return session.refs == 0
}

func (l *LocalStreamServer) releaseSession(session *StreamSession) {
	l.mu.Lock()
	session.refs--
	shouldRemove := session.refs == 0 && session.closed
	l.mu.Unlock()

	if shouldRemove {
		removeSpoolFile(session)
	}
}

func (l *LocalStreamServer) Unregister(id string) {
	l.mu.Lock()
	session := l.sessions[id]
	var toRemove *StreamSession
	if session != nil && l.closeSessionLocked(id, session) {
		toRemove = session
	}
	l.mu.Unlock()

	if toRemove != nil {
		removeSpoolFile(toRemove)
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

	l.mu.Lock()
	session, exists := l.sessions[id]
	if !exists || session.closed || session.token != token {
		l.mu.Unlock()
		http.Error(w, "Unauthorized or Stream Not Found", http.StatusUnauthorized)
		return
	}
	if time.Now().After(session.expiresAt) {
		l.mu.Unlock()
		http.Error(w, "Stream Session Expired", http.StatusGone)
		return
	}
	session.refs++
	l.mu.Unlock()

	defer l.releaseSession(session)

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
			var toRemove []*StreamSession
			l.mu.Lock()
			now := time.Now()
			for id, session := range l.sessions {
				if now.After(session.expiresAt) {
					if l.closeSessionLocked(id, session) {
						toRemove = append(toRemove, session)
					}
					l.logger.Debug("Sweeper removed expired stream session", "id", id)
				}
			}
			l.mu.Unlock()

			for _, session := range toRemove {
				removeSpoolFile(session)
			}
		}
	}
}

func generateRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
