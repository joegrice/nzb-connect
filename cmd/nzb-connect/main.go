package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joe/nzb-connect/internal/api"
	"github.com/joe/nzb-connect/internal/config"
	"github.com/joe/nzb-connect/internal/downloader"
	"github.com/joe/nzb-connect/internal/postprocess"
	"github.com/joe/nzb-connect/internal/queue"
	"github.com/joe/nzb-connect/internal/vpn"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("NZB Connect starting...")

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	log.Printf("Config loaded from %s", *configPath)

	// Ensure directories exist
	if err := cfg.EnsureDirectories(); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}

	// Initialize queue manager (SQLite)
	dbPath := filepath.Join(cfg.Paths.Temp, "nzb-connect.db")
	queueMgr, err := queue.NewManager(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize queue: %v", err)
	}
	defer queueMgr.Close()
	config.ChownToRealUser(dbPath) // ensure DB is accessible without sudo

	// Initialize connection pool manager (interface set later by VPN manager)
	poolMgr := downloader.NewPoolManager("")
	poolMgr.UpdateServers(cfg.GetServers())

	// Initialize download engine
	engine := downloader.NewEngine(poolMgr, queueMgr, cfg.Paths.Incomplete, cfg.Paths.Temp)

	// Initialize post-processor
	proc := postprocess.NewProcessor(cfg, queueMgr)
	engine.OnComplete(func(dl *queue.Download) {
		go proc.Process(dl)
	})

	// Initialize VPN manager
	vpnMgr := vpn.NewManager(cfg)
	vpnMgr.OnDown(func() {
		log.Println("VPN down — pausing downloads and closing connections")
		queueMgr.SetPaused(true)
		poolMgr.CloseAll()
	})
	vpnMgr.OnUp(func(interfaceName string) {
		log.Printf("VPN up on %s — resuming downloads", interfaceName)
		poolMgr.SetVPNInterface(interfaceName)
		poolMgr.UpdateServers(cfg.GetServers())
		queueMgr.SetPaused(false)
		engine.Notify()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vpnMgr.Start(ctx)
	defer vpnMgr.Stop()

	// Start download engine
	engine.Start()
	defer engine.Stop()

	// Set up HTTP server
	mux := http.NewServeMux()

	handler := &api.Handler{
		Config:   cfg,
		QueueMgr: queueMgr,
		Engine:   engine,
		VPNMgr:   vpnMgr,
		PoolMgr:  poolMgr,
	}
	handler.RegisterRoutes(mux)

	// Serve static files
	staticDir := findStaticDir()
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	addr := fmt.Sprintf(":%d", cfg.Web.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("Web UI listening on http://0.0.0.0%s", addr)
	if vpnMgr.IsUp() {
		log.Printf("VPN interface %s is UP", vpnMgr.InterfaceName())
	} else if cfg.VPN.Protocol != "" {
		log.Printf("VPN managed mode (%s) — connection in progress", cfg.VPN.Protocol)
	} else {
		log.Printf("WARNING: VPN interface %s is DOWN - downloads paused", cfg.VPN.Interface)
		queueMgr.SetPaused(true)
	}

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("NZB Connect stopped.")
}

// findStaticDir locates the web/static directory relative to the binary or CWD.
func findStaticDir() string {
	// Try relative to CWD
	candidates := []string{
		"web/static",
		"../../web/static",
		filepath.Join(filepath.Dir(os.Args[0]), "web/static"),
		filepath.Join(filepath.Dir(os.Args[0]), "../../web/static"),
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			abs, _ := filepath.Abs(dir)
			return abs
		}
	}
	log.Println("WARNING: Could not find web/static directory, web UI may not work")
	return "web/static"
}
