package postprocess

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nwaples/rardecode/v2"

	"nzb-connect/internal/config"
	"nzb-connect/internal/nzb"
	"nzb-connect/internal/queue"
)

// RAR magic bytes:
//
//	RAR3/4: 52 61 72 21 1A 07 00
//	RAR5:   52 61 72 21 1A 07 01 00
var rar3Magic = []byte{0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0x00}
var rar5Magic = []byte{0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0x01, 0x00}

func rarVersion(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	buf := make([]byte, 8)
	if _, err := io.ReadFull(f, buf); err != nil {
		return 0
	}
	if len(buf) >= 8 {
		match := true
		for i, b := range rar5Magic {
			if buf[i] != b {
				match = false
				break
			}
		}
		if match {
			return 5
		}
	}
	match := true
	for i, b := range rar3Magic {
		if buf[i] != b {
			match = false
			break
		}
	}
	if match {
		return 3
	}
	return 0
}

// ProgressFunc is called during extraction with the current percentage (0–100) and the filename being extracted.
type ProgressFunc func(pct float64, file string)

// countingWriter wraps an io.Writer and calls onPct each time the written percentage advances.
type countingWriter struct {
	w       io.Writer
	total   int64
	written int64
	lastPct int64
	onPct   func(float64)
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 && cw.total > 0 && cw.onPct != nil {
		cw.written += int64(n)
		pct := cw.written * 100 / cw.total
		if pct > 100 {
			pct = 100
		}
		if pct != atomic.LoadInt64(&cw.lastPct) {
			atomic.StoreInt64(&cw.lastPct, pct)
			cw.onPct(float64(pct))
		}
	}
	return n, err
}

// parseUnrarLine parses a line from unrar stdout and returns (pct, filename).
// Returns (-1, "") if the line is not a progress line.
func parseUnrarLine(line string) (float64, string) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "Extracting") && !strings.HasPrefix(t, "...") {
		return -1, ""
	}
	fields := strings.Fields(t)
	if len(fields) < 2 {
		return -1, ""
	}
	last := fields[len(fields)-1]
	if !strings.HasSuffix(last, "%") {
		return -1, ""
	}
	var pct float64
	if _, err := fmt.Sscanf(strings.TrimSuffix(last, "%"), "%f", &pct); err != nil {
		return -1, ""
	}
	if len(fields) >= 3 {
		return pct, filepath.Base(strings.Join(fields[1:len(fields)-1], " "))
	}
	return pct, ""
}

// Processor handles post-processing of completed downloads.
type Processor struct {
	cfg      *config.Config
	queueMgr *queue.Manager
}

// NewProcessor creates a new post-processor.
func NewProcessor(cfg *config.Config, queueMgr *queue.Manager) *Processor {
	return &Processor{cfg: cfg, queueMgr: queueMgr}
}

// Process runs post-processing on a completed download.
func (p *Processor) Process(dl *queue.Download) {
	log.Printf("Post-processing: %s", dl.Name)

	srcDir := dl.Path
	if srcDir == "" {
		srcDir = filepath.Join(p.cfg.Paths.Incomplete, dl.Name)
	}

	destDir := filepath.Join(p.cfg.Paths.Complete, dl.Name)
	if dl.Category != "" {
		destDir = filepath.Join(p.cfg.Paths.Complete, dl.Category, dl.Name)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Printf("Error creating dest dir: %v", err)
		p.queueMgr.SetError(dl.ID, fmt.Sprintf("mkdir dest: %v", err))
		return
	}

	// Extract password from NZB metadata (e.g. <meta type="password">)
	var archivePassword string
	if len(dl.NZBData) > 0 {
		if parsed, err := nzb.ParseBytes(dl.NZBData); err == nil {
			archivePassword = parsed.Password()
			if archivePassword != "" {
				log.Printf("NZB metadata contains a password for %s", dl.Name)
			}
		}
	}

	// Find and extract archives
	archives, err := findArchives(srcDir)
	if err != nil {
		log.Printf("Error finding archives: %v", err)
	}

	extractStart := time.Now()
	onProgress := ProgressFunc(func(pct float64, file string) {
		p.queueMgr.SetExtractProgress(dl.ID, pct, file)
	})

	extractOK := true
	if len(archives) > 0 {
		for _, archive := range archives {
			if err := p.extractArchive(archive, destDir, archivePassword, onProgress); err != nil {
				log.Printf("Extraction failed for %s: %v", filepath.Base(archive), err)
				extractOK = false
				break
			}
		}

		if extractOK {
			elapsed := time.Since(extractStart).Seconds()
			approxMB := float64(dl.TotalBytes) / 1024 / 1024
			if elapsed > 0 {
				log.Printf("Extraction complete: ~%.0f MB in %.1fs (~%.1f MB/s) for %s",
					approxMB, elapsed, approxMB/elapsed, dl.Name)
			}
		}
		p.queueMgr.ClearExtractProgress(dl.ID)

		if extractOK {
			// Delete archives only after successful extraction
			if p.cfg.PostProcess.DeleteArchives {
				for _, archive := range archives {
					os.Remove(archive)
				}
				removeRelatedFiles(srcDir)
			}
			moveNonArchiveFiles(srcDir, destDir)
		} else {
			// Extraction failed — move everything (including .rar files) to the
			// complete directory so Sonarr/Radarr and the user can find the files.
			// We do NOT delete the archives since they weren't extracted.
			log.Printf("Moving raw files to complete dir after extraction failure: %s", destDir)
			if err := moveAllFiles(srcDir, destDir); err != nil {
				log.Printf("Error moving files to complete: %v", err)
			}
		}
	} else {
		// No archives — move everything as-is
		if err := moveAllFiles(srcDir, destDir); err != nil {
			log.Printf("Error moving files: %v", err)
			p.queueMgr.SetError(dl.ID, fmt.Sprintf("move error: %v", err))
			return
		}
	}

	// Clean up the (now empty or abandoned) incomplete directory
	os.RemoveAll(srcDir)

	// Ensure the destination directory and all its contents are owned by the
	// real user (not root) so they can manage files without sudo.
	config.ChownToRealUser(destDir)

	// Always update the path so history shows the complete directory
	p.queueMgr.UpdatePath(dl.ID, destDir)

	if !extractOK {
		// Mark as failed so the ARR stack knows extraction didn't complete,
		// but the path is already updated to the complete dir for inspection.
		p.queueMgr.SetError(dl.ID, "extraction failed — raw archives moved to complete dir")
		log.Printf("Post-processing partial: %s -> %s (extraction failed, raw files moved)", dl.Name, destDir)
		return
	}

	p.queueMgr.UpdateStatus(dl.ID, queue.StatusCompleted)
	log.Printf("Post-processing complete: %s -> %s", dl.Name, destDir)
}

func (p *Processor) extractArchive(archivePath, destDir, password string, onProgress ProgressFunc) error {
	ext := strings.ToLower(filepath.Ext(archivePath))
	switch ext {
	case ".rar":
		return p.extractRar(archivePath, destDir, password, onProgress)
	case ".zip", ".7z":
		return p.extract7z(archivePath, destDir)
	default:
		return fmt.Errorf("unsupported archive format: %s", ext)
	}
}

// extractRarUnrar runs unrar with live stdout progress parsing.
func (p *Processor) extractRarUnrar(unrar, archivePath, destDir, password string, onProgress ProgressFunc) error {
	passFlag := "-p-"
	if password != "" {
		passFlag = "-p" + password
	}
	cmd := exec.Command(unrar, "x", "-o+", "-y", passFlag, archivePath, destDir+"/")
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("unrar stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting unrar: %w", err)
	}
	var lastFile string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		log.Printf("[unrar] %s", line)
		if onProgress != nil {
			pct, file := parseUnrarLine(line)
			if file != "" {
				lastFile = file
			}
			if pct >= 0 {
				onProgress(pct, lastFile)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 11 {
			return fmt.Errorf("unrar exit 11 (BADPWD) for %s", filepath.Base(archivePath))
		}
		return fmt.Errorf("unrar: %w", err)
	}
	if onProgress != nil && lastFile != "" {
		onProgress(100, lastFile)
	}
	return nil
}

// extractRar extracts a RAR archive (single or multi-volume).
// Strategy:
//  1. External unrar — preferred; 5–10× faster than pure-Go for large RAR5.
//  2. Pure Go (rardecode/v2) — fallback, no binary dep needed.
//  3. External 7z / 7zz — last resort.
func (p *Processor) extractRar(archivePath, destDir, password string, onProgress ProgressFunc) error {
	log.Printf("Extracting RAR: %s -> %s", archivePath, destDir)

	ver := rarVersion(archivePath)
	log.Printf("Detected RAR%d format: %s", ver, filepath.Base(archivePath))

	// 1. External unrar — preferred; 5–10× faster than pure-Go for large RAR5.
	if unrar := resolveUnrar(p.cfg.PostProcess.Unrar); unrar != "" {
		if err := p.extractRarUnrar(unrar, archivePath, destDir, password, onProgress); err == nil {
			return nil
		} else {
			log.Printf("unrar failed (%v), falling back to pure-Go rardecode/v2", err)
		}
	}

	// 2. Pure-Go rardecode/v2 — fallback; no binary dep needed.
	if err := extractRarGo(archivePath, destDir, password, onProgress); err == nil {
		return nil
	} else {
		log.Printf("Pure-Go RAR extraction failed (%v), trying 7z", err)
	}

	// 3. 7z last resort.
	if sevenzip := resolve7z(p.cfg.PostProcess.SevenZip); sevenzip != "" {
		cmd := exec.Command(sevenzip, "x", archivePath, "-o"+destDir, "-y")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			return nil
		} else {
			log.Printf("7z failed: %v", err)
		}
	}

	return fmt.Errorf("RAR extraction failed: tried unrar, pure-Go rardecode/v2, and 7z")
}

// extractRarGo extracts a RAR archive using the pure-Go rardecode/v2 library.
// Supports RAR 2/3/4/5 including multi-volume archives.
func extractRarGo(archivePath, destDir, password string, onProgress ProgressFunc) error {
	var opts []rardecode.Option
	if password != "" {
		opts = append(opts, rardecode.Password(password))
	}
	r, err := rardecode.OpenReader(archivePath, opts...)
	if err != nil {
		return fmt.Errorf("open rar: %w", err)
	}
	defer r.Close()

	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading rar entry: %w", err)
		}

		destPath := filepath.Join(destDir, header.Name)

		if header.IsDir {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("create %s: %w", destPath, err)
		}

		var dest io.Writer = f
		if onProgress != nil && header.UnPackedSize > 0 {
			cw := &countingWriter{
				w:     f,
				total: header.UnPackedSize,
				onPct: func(pct float64) { onProgress(pct, filepath.Base(header.Name)) },
			}
			dest = cw
		}
		_, copyErr := io.Copy(dest, r)
		f.Close()
		if copyErr != nil {
			return fmt.Errorf("write %s: %w", destPath, copyErr)
		}
		if onProgress != nil {
			onProgress(100, filepath.Base(header.Name))
		}
	}
	return nil
}

func (p *Processor) extract7z(archivePath, destDir string) error {
	sevenzip := resolve7z(p.cfg.PostProcess.SevenZip)
	if sevenzip == "" {
		return fmt.Errorf("7z not found; install p7zip or 7zip")
	}

	cmd := exec.Command(sevenzip, "x", archivePath, "-o"+destDir, "-y")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Extracting with 7z: %s -> %s", archivePath, destDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("7z failed: %w", err)
	}
	return nil
}

// resolveUnrar finds the unrar binary from the config path, PATH, or common locations.
func resolveUnrar(configured string) string {
	candidates := []string{configured, "unrar"}
	for _, dir := range []string{"/usr/bin", "/usr/local/bin", "/usr/sbin", "/bin"} {
		candidates = append(candidates, filepath.Join(dir, "unrar"))
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return ""
}

// resolve7z finds 7z / 7zz / 7za from config, PATH, or common locations.
func resolve7z(configured string) string {
	// Prefer the configured path, then try common names in order
	candidates := []string{configured, "7z", "7zz", "7za"}
	for _, dir := range []string{"/usr/bin", "/usr/local/bin", "/bin"} {
		for _, name := range []string{"7z", "7zz", "7za"} {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return ""
}

func findArchives(dir string) ([]string, error) {
	var archives []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	seenRar := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		ext := filepath.Ext(name)

		switch ext {
		case ".rar":
			// Only add the first .rar — unrar/rardecode handles subsequent volumes automatically
			if !seenRar {
				archives = append(archives, filepath.Join(dir, entry.Name()))
				seenRar = true
			}
		case ".zip", ".7z":
			archives = append(archives, filepath.Join(dir, entry.Name()))
		}
	}
	return archives, nil
}

func removeRelatedFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		ext := filepath.Ext(name)
		if ext == ".rar" || ext == ".zip" || ext == ".7z" ||
			(len(ext) == 4 && ext[0] == '.' && ext[1] == 'r' && ext[2] >= '0' && ext[2] <= '9') {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func moveAllFiles(srcDir, destDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(destDir, entry.Name())
		if err := moveFile(src, dst); err != nil {
			return fmt.Errorf("moving %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func moveNonArchiveFiles(srcDir, destDir string) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		ext := filepath.Ext(name)
		if ext == ".rar" || ext == ".zip" || ext == ".7z" ||
			(len(ext) == 4 && ext[0] == '.' && ext[1] == 'r' && ext[2] >= '0' && ext[2] <= '9') {
			continue
		}
		moveFile(filepath.Join(srcDir, entry.Name()), filepath.Join(destDir, entry.Name()))
	}
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, info.Mode()); err != nil {
		return err
	}
	return os.Remove(src)
}
