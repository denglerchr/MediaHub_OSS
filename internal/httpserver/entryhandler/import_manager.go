package entryhandler

import (
	"context"
	"sync"
)

const defaultMaxConcurrentImports = 2

var importManagerInitMu sync.Mutex

// ImportManager bounds concurrent background entry imports and coordinates graceful shutdown.
type ImportManager struct {
	mu            sync.Mutex
	maxConcurrent int
	active        int
	shuttingDown  bool
	baseCtx       context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

// NewImportManager creates an ImportManager with the given concurrency limit (defaults to 2 if <= 0).
func NewImportManager(maxConcurrent int) *ImportManager {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentImports
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &ImportManager{
		maxConcurrent: maxConcurrent,
		baseCtx:       ctx,
		cancel:        cancel,
	}
}

// TryStart attempts to acquire a concurrent import slot.
// Returns (jobCtx, release, true) on success, or (nil, nil, false) when busy or shutting down.
func (m *ImportManager) TryStart() (context.Context, func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shuttingDown || m.active >= m.maxConcurrent {
		return nil, nil, false
	}

	m.active++
	m.wg.Add(1)
	jobCtx, jobCancel := context.WithCancel(m.baseCtx)

	var once sync.Once
	release := func() {
		once.Do(func() {
			jobCancel()
			m.mu.Lock()
			m.active--
			m.mu.Unlock()
			m.wg.Done()
		})
	}

	return jobCtx, release, true
}

// Shutdown stops accepting new imports, cancels active import contexts, and waits
// for running imports to exit or until ctx expires.
func (m *ImportManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.shuttingDown = true
	m.cancel()
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *EntryHandler) importManager() *ImportManager {
	importManagerInitMu.Lock()
	defer importManagerInitMu.Unlock()
	if h.Imports == nil {
		h.Imports = NewImportManager(defaultMaxConcurrentImports)
	}
	return h.Imports
}
