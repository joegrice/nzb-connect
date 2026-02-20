package downloader

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joe/nzb-connect/internal/nzb"
	"github.com/joe/nzb-connect/internal/queue"
)

// Engine orchestrates the download of NZB files.
type Engine struct {
	poolMgr      *PoolManager
	queueMgr     *queue.Manager
	incompletDir string
	tempDir      string
	workers      int

	mu              sync.Mutex
	currentSpeed    atomic.Int64 // bytes per second
	cancel          context.CancelFunc
	ctx             context.Context
	wakeUp          chan struct{}
	onComplete      func(dl *queue.Download)
	activeDownloads map[string]context.CancelFunc // id → cancel, protected by mu
}

// NewEngine creates a new download engine.
func NewEngine(poolMgr *PoolManager, queueMgr *queue.Manager, incompleteDir, tempDir string) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		poolMgr:         poolMgr,
		queueMgr:        queueMgr,
		incompletDir:    incompleteDir,
		tempDir:         tempDir,
		workers:         8,
		ctx:             ctx,
		cancel:          cancel,
		wakeUp:          make(chan struct{}, 1),
		activeDownloads: make(map[string]context.CancelFunc),
	}
}

// CancelDownload stops a queued or in-progress download and marks it failed.
func (e *Engine) CancelDownload(id string) {
	// Mark as failed immediately so it won't be picked up by the process loop
	_ = e.queueMgr.SetError(id, "cancelled by user")

	// If the download is actively running, cancel its context to stop goroutines
	e.mu.Lock()
	if cancel, ok := e.activeDownloads[id]; ok {
		cancel()
	}
	e.mu.Unlock()
}

// OnComplete sets a callback for when a download finishes successfully.
func (e *Engine) OnComplete(fn func(dl *queue.Download)) {
	e.onComplete = fn
}

// Start begins the download processing loop.
func (e *Engine) Start() {
	go e.processLoop()
}

// Stop stops the engine.
func (e *Engine) Stop() {
	e.cancel()
}

// Notify wakes up the processing loop to check for new work.
func (e *Engine) Notify() {
	select {
	case e.wakeUp <- struct{}{}:
	default:
	}
}

// CurrentSpeed returns the current download speed in bytes/sec.
func (e *Engine) CurrentSpeed() int64 {
	return e.currentSpeed.Load()
}

func (e *Engine) processLoop() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.wakeUp:
		case <-time.After(5 * time.Second):
		}

		if e.queueMgr.IsPaused() {
			continue
		}

		dl, err := e.queueMgr.GetNextQueued()
		if err != nil {
			log.Printf("Error getting next queued: %v", err)
			continue
		}
		if dl == nil {
			continue
		}

		e.processDownload(dl)
	}
}

func (e *Engine) processDownload(dl *queue.Download) {
	log.Printf("Starting download: %s", dl.Name)

	// Per-download context so individual downloads can be cancelled without
	// stopping the whole engine.
	dlCtx, dlCancel := context.WithCancel(e.ctx)
	defer dlCancel()

	e.mu.Lock()
	e.activeDownloads[dl.ID] = dlCancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.activeDownloads, dl.ID)
		e.mu.Unlock()
	}()

	if err := e.queueMgr.UpdateStatus(dl.ID, queue.StatusDownloading); err != nil {
		log.Printf("Error updating status: %v", err)
		return
	}

	// Parse the NZB data
	nzbFile, err := nzb.ParseBytes(dl.NZBData)
	if err != nil {
		log.Printf("Error parsing NZB for %s: %v", dl.Name, err)
		e.queueMgr.SetError(dl.ID, fmt.Sprintf("NZB parse error: %v", err))
		return
	}

	// Create download directory
	dlDir := filepath.Join(e.incompletDir, dl.Name)
	if err := os.MkdirAll(dlDir, 0755); err != nil {
		log.Printf("Error creating directory %s: %v", dlDir, err)
		e.queueMgr.SetError(dl.ID, fmt.Sprintf("mkdir error: %v", err))
		return
	}

	if err := e.queueMgr.UpdatePath(dl.ID, dlDir); err != nil {
		log.Printf("Error updating path: %v", err)
	}

	// Download all files in the NZB
	var totalDone atomic.Int32
	var totalBytes atomic.Int64
	startTime := time.Now()

	// Speed tracking goroutine
	speedCtx, speedCancel := context.WithCancel(dlCtx)
	defer speedCancel()
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		var lastBytes int64
		for {
			select {
			case <-speedCtx.Done():
				e.currentSpeed.Store(0)
				return
			case <-ticker.C:
				current := totalBytes.Load()
				speed := current - lastBytes
				lastBytes = current
				e.currentSpeed.Store(speed)
			}
		}
	}()

	var downloadErr error
	for _, file := range nzbFile.Files {
		if dlCtx.Err() != nil || e.queueMgr.IsPaused() {
			break
		}

		err := e.downloadFile(dlCtx, file, dlDir, &totalDone, &totalBytes, dl)
		if err != nil {
			downloadErr = err
			log.Printf("Error downloading file %s: %v", file.Filename(), err)
			break
		}
	}

	speedCancel()

	elapsed := time.Since(startTime)
	log.Printf("Download %s finished in %s (%.2f MB/s)",
		dl.Name, elapsed.Round(time.Second),
		float64(totalBytes.Load())/elapsed.Seconds()/1024/1024)

	if downloadErr != nil {
		e.queueMgr.SetError(dl.ID, fmt.Sprintf("download error: %v", downloadErr))
		return
	}

	// Mark as processing (post-processing will pick it up)
	if err := e.queueMgr.UpdateStatus(dl.ID, queue.StatusProcessing); err != nil {
		log.Printf("Error updating status: %v", err)
	}

	// Trigger post-processing callback
	updatedDL, err := e.queueMgr.Get(dl.ID)
	if err != nil {
		log.Printf("Error getting updated download: %v", err)
		return
	}
	if e.onComplete != nil {
		e.onComplete(updatedDL)
	}
}

func (e *Engine) downloadFile(ctx context.Context, file nzb.File, dlDir string, totalDone *atomic.Int32, totalBytes *atomic.Int64, dl *queue.Download) error {
	filename := file.Filename()
	segments := file.SortedSegments()

	// Pre-allocate result slice for ordered assembly
	results := make([][]byte, len(segments))
	var resultsMu sync.Mutex
	var downloadErr error
	var errOnce sync.Once

	// Worker pool for segments
	sem := make(chan struct{}, e.workers)
	var wg sync.WaitGroup

	for i, seg := range segments {
		if ctx.Err() != nil || e.queueMgr.IsPaused() {
			break
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(idx int, segment nzb.Segment) {
			defer wg.Done()
			defer func() { <-sem }()

			data, err := e.poolMgr.FetchSegment(ctx, segment.MessageID)
			if err != nil {
				errOnce.Do(func() {
					downloadErr = fmt.Errorf("segment %d (%s): %w", segment.Number, segment.MessageID, err)
				})
				return
			}

			// Decode yEnc
			decoded, err := DecodeYEnc(data)
			if err != nil {
				errOnce.Do(func() {
					downloadErr = fmt.Errorf("yenc decode segment %d: %w", segment.Number, err)
				})
				return
			}

			resultsMu.Lock()
			results[idx] = decoded.Data
			resultsMu.Unlock()

			totalBytes.Add(int64(len(decoded.Data)))
			done := int(totalDone.Add(1))

			// Update progress periodically
			if done%10 == 0 || done == dl.TotalSegments {
				e.queueMgr.UpdateProgress(dl.ID, totalBytes.Load(), done)
			}
		}(i, seg)
	}

	wg.Wait()

	if downloadErr != nil {
		return downloadErr
	}

	// Assemble file from segments
	filePath := filepath.Join(dlDir, filename)
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", filePath, err)
	}
	defer f.Close()

	for i, data := range results {
		if data == nil {
			return fmt.Errorf("missing segment %d for %s", i+1, filename)
		}
		if _, err := bytes.NewReader(data).WriteTo(f); err != nil {
			return fmt.Errorf("writing segment %d: %w", i+1, err)
		}
	}

	log.Printf("Assembled file: %s", filename)
	return nil
}
