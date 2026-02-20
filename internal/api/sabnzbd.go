package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/joe/nzb-connect/internal/config"
	"github.com/joe/nzb-connect/internal/downloader"
	"github.com/joe/nzb-connect/internal/nzb"
	"github.com/joe/nzb-connect/internal/queue"
	"github.com/joe/nzb-connect/internal/vpn"
)

// Handler holds shared state for all API handlers.
type Handler struct {
	Config   *config.Config
	QueueMgr *queue.Manager
	Engine   *downloader.Engine
	VPNMgr   *vpn.Manager
	PoolMgr  *downloader.PoolManager
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// RegisterRoutes registers all API routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api", h.handleSABnzbd)
	mux.HandleFunc("/api/servers", h.handleServers)
	mux.HandleFunc("/api/servers/", h.handleServerByID)
	mux.HandleFunc("/api/servers/test", h.handleTestServer)
	mux.HandleFunc("/api/queue/", h.handleQueueItem)
	mux.HandleFunc("/api/vpn", h.handleVPN)
	mux.HandleFunc("/api/vpn/connect", h.handleVPNConnect)
	mux.HandleFunc("/api/vpn/disconnect", h.handleVPNDisconnect)
	mux.HandleFunc("/api/vpn/status", h.handleVPNStatus)
}

// handleSABnzbd handles the SABnzbd-compatible API endpoint.
func (h *Handler) handleSABnzbd(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleSABnzbdGet(w, r)
	case http.MethodPost:
		h.handleSABnzbdPost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleSABnzbdGet(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")

	switch mode {
	case "queue":
		h.getQueue(w, r)
	case "history":
		h.getHistory(w, r)
	case "status":
		h.getStatus(w, r)
	case "version":
		writeJSON(w, map[string]string{"version": "4.0.0"})
	case "fullstatus":
		h.getStatus(w, r)
	default:
		writeJSON(w, map[string]interface{}{
			"status": true,
			"mode":   mode,
		})
	}
}

func (h *Handler) handleSABnzbdPost(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = r.FormValue("mode")
	}

	switch mode {
	case "addfile":
		h.addNZBFile(w, r)
	case "addurl":
		h.addNZBURL(w, r)
	default:
		// Default: try to handle as NZB add
		h.addNZB(w, r)
	}
}

func (h *Handler) addNZB(w http.ResponseWriter, r *http.Request) {
	// Check for file upload
	file, header, err := r.FormFile("nzbfile")
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeJSON(w, map[string]interface{}{"status": false, "error": "read error"})
			return
		}

		name := strings.TrimSuffix(header.Filename, ".nzb")
		category := r.FormValue("cat")
		if category == "" {
			category = r.FormValue("category")
		}

		id, err := h.addDownload(name, category, data)
		if err != nil {
			writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
			return
		}

		writeJSON(w, map[string]interface{}{
			"status":  true,
			"nzo_ids": []string{id},
		})
		return
	}

	// Check for URL
	nzbURL := r.FormValue("name")
	if nzbURL == "" {
		nzbURL = r.FormValue("value")
	}
	if nzbURL != "" {
		h.downloadAndAddNZB(w, r, nzbURL)
		return
	}

	writeJSON(w, map[string]interface{}{
		"status": false,
		"error":  "no NZB file or URL provided",
	})
}

func (h *Handler) addNZBFile(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("nzbfile")
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "no file uploaded"})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "read error"})
		return
	}

	name := strings.TrimSuffix(header.Filename, ".nzb")
	category := r.FormValue("cat")

	id, err := h.addDownload(name, category, data)
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":  true,
		"nzo_ids": []string{id},
	})
}

func (h *Handler) addNZBURL(w http.ResponseWriter, r *http.Request) {
	nzbURL := r.FormValue("name")
	if nzbURL == "" {
		nzbURL = r.FormValue("value")
	}
	if nzbURL == "" {
		writeJSON(w, map[string]interface{}{"status": false, "error": "no URL provided"})
		return
	}
	h.downloadAndAddNZB(w, r, nzbURL)
}

func (h *Handler) downloadAndAddNZB(w http.ResponseWriter, r *http.Request, nzbURL string) {
	resp, err := http.Get(nzbURL)
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": fmt.Sprintf("download error: %v", err)})
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "read error"})
		return
	}

	// Extract name from URL
	parts := strings.Split(nzbURL, "/")
	name := parts[len(parts)-1]
	name = strings.TrimSuffix(name, ".nzb")
	if name == "" {
		name = "download"
	}

	category := r.FormValue("cat")
	if category == "" {
		category = r.FormValue("category")
	}

	id, err := h.addDownload(name, category, data)
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":  true,
		"nzo_ids": []string{id},
	})
}

func (h *Handler) addDownload(name, category string, nzbData []byte) (string, error) {
	// Parse to validate and get metadata
	parsed, err := nzb.ParseBytes(nzbData)
	if err != nil {
		return "", fmt.Errorf("invalid NZB: %w", err)
	}

	id := generateID()
	dl := &queue.Download{
		ID:            id,
		Name:          name,
		Category:      category,
		TotalBytes:    parsed.TotalSize(),
		TotalSegments: parsed.TotalSegments(),
		NZBData:       nzbData,
	}

	if err := h.QueueMgr.Add(dl); err != nil {
		return "", err
	}

	// Wake up the download engine
	h.Engine.Notify()

	log.Printf("Added NZB: %s (id=%s, %d files, %d segments)",
		name, id, len(parsed.Files), parsed.TotalSegments())

	return id, nil
}

func (h *Handler) getQueue(w http.ResponseWriter, r *http.Request) {
	downloads, err := h.QueueMgr.GetQueue()
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
		return
	}

	slots := make([]map[string]interface{}, 0, len(downloads))
	for _, dl := range downloads {
		slots = append(slots, map[string]interface{}{
			"nzo_id":      dl.ID,
			"filename":    dl.Name,
			"cat":         dl.Category,
			"status":      mapStatusToSAB(dl.Status),
			"mb":          fmt.Sprintf("%.2f", float64(dl.TotalBytes)/1024/1024),
			"mbleft":      fmt.Sprintf("%.2f", float64(dl.TotalBytes-dl.DownloadedBytes)/1024/1024),
			"percentage":  fmt.Sprintf("%.0f", dl.Progress()),
			"size":        nzb.FormatSize(dl.TotalBytes),
			"sizeleft":    nzb.FormatSize(dl.TotalBytes - dl.DownloadedBytes),
			"timeleft":     "unknown",
			"extract_pct":  fmt.Sprintf("%.0f", dl.ExtractPct),
			"extract_file": dl.ExtractFile,
		})
	}

	writeJSON(w, map[string]interface{}{
		"queue": map[string]interface{}{
			"paused": h.QueueMgr.IsPaused(),
			"slots":  slots,
			"speed":  fmt.Sprintf("%.0f", float64(h.Engine.CurrentSpeed())/1024),
			"noofslots": len(slots),
		},
	})
}

func (h *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	history, err := h.QueueMgr.GetHistory()
	if err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
		return
	}

	slots := make([]map[string]interface{}, 0, len(history))
	for _, dl := range history {
		completedAt := int64(0)
		downloadTime := 0
		if dl.CompletedAt != nil {
			completedAt = dl.CompletedAt.Unix()
			downloadTime = int(dl.CompletedAt.Sub(dl.CreatedAt).Seconds())
		}

		slots = append(slots, map[string]interface{}{
			"nzo_id":        dl.ID,
			"name":          dl.Name,
			"category":      dl.Category,
			"status":        mapStatusToSABHistory(dl.Status),
			"fail_message":  dl.ErrorMsg,
			"storage":       dl.Path,
			"bytes":         dl.TotalBytes,
			"download_time": downloadTime,
			"completed":     completedAt,
		})
	}

	writeJSON(w, map[string]interface{}{
		"history": map[string]interface{}{
			"slots":     slots,
			"noofslots": len(slots),
		},
	})
}

func (h *Handler) getStatus(w http.ResponseWriter, r *http.Request) {
	speedKBs := float64(h.Engine.CurrentSpeed()) / 1024

	queueItems, _ := h.QueueMgr.GetQueue()
	var remainingMB float64
	for _, dl := range queueItems {
		remainingMB += float64(dl.TotalBytes-dl.DownloadedBytes) / 1024 / 1024
	}

	vpnUp := true
	vpnIface := ""
	if h.VPNMgr != nil {
		vpnUp = h.VPNMgr.IsUp()
		vpnIface = h.VPNMgr.InterfaceName()
	}

	writeJSON(w, map[string]interface{}{
		"status": map[string]interface{}{
			"paused":           h.QueueMgr.IsPaused(),
			"speed":            fmt.Sprintf("%.0f", speedKBs),
			"kbpersec":         fmt.Sprintf("%.2f", speedKBs),
			"mbleft":           fmt.Sprintf("%.2f", remainingMB),
			"noofslots_total":  len(queueItems),
			"version":          "4.0.0",
			"vpn_connected":    vpnUp,
			"vpn_interface":    vpnIface,
		},
	})
}

// handleQueueItem handles DELETE /api/queue/{id} to cancel a download.
func (h *Handler) handleQueueItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/queue/")
	if id == "" {
		http.Error(w, "missing download ID", http.StatusBadRequest)
		return
	}
	h.Engine.CancelDownload(id)
	writeJSON(w, map[string]interface{}{"status": true})
}

// Server management handlers

func (h *Handler) handleServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listServers(w, r)
	case http.MethodPost:
		h.addServer(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleServerByID(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/servers/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/servers/")
	if id == "" || id == "test" {
		http.Error(w, "missing server ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.updateServer(w, r, id)
	case http.MethodDelete:
		h.deleteServer(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) listServers(w http.ResponseWriter, r *http.Request) {
	servers := h.Config.GetServers()
	writeJSON(w, map[string]interface{}{
		"servers": servers,
	})
}

func (h *Handler) addServer(w http.ResponseWriter, r *http.Request) {
	var srv config.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "invalid JSON"})
		return
	}

	if srv.Host == "" {
		writeJSON(w, map[string]interface{}{"status": false, "error": "host is required"})
		return
	}
	if srv.ID == "" {
		srv.ID = generateID()
	}
	if srv.Name == "" {
		srv.Name = srv.Host
	}

	h.Config.AddServer(srv)
	if err := h.Config.Save(); err != nil {
		log.Printf("Error saving config: %v", err)
	}
	h.PoolMgr.UpdateServers(h.Config.GetServers())

	writeJSON(w, map[string]interface{}{
		"status": true,
		"server": srv,
	})
}

func (h *Handler) updateServer(w http.ResponseWriter, r *http.Request, id string) {
	var srv config.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "invalid JSON"})
		return
	}

	// Preserve password if the client left it blank (user didn't change it)
	if srv.Password == "" {
		for _, existing := range h.Config.GetServers() {
			if existing.ID == id || existing.Name == id {
				srv.Password = existing.Password
				break
			}
		}
	}

	if !h.Config.UpdateServer(id, srv) {
		writeJSON(w, map[string]interface{}{"status": false, "error": "server not found"})
		return
	}

	if err := h.Config.Save(); err != nil {
		log.Printf("Error saving config: %v", err)
	}
	h.PoolMgr.UpdateServers(h.Config.GetServers())

	writeJSON(w, map[string]interface{}{"status": true})
}

func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request, id string) {
	if !h.Config.DeleteServer(id) {
		writeJSON(w, map[string]interface{}{"status": false, "error": "server not found"})
		return
	}

	if err := h.Config.Save(); err != nil {
		log.Printf("Error saving config: %v", err)
	}
	h.PoolMgr.UpdateServers(h.Config.GetServers())

	writeJSON(w, map[string]interface{}{"status": true})
}

func (h *Handler) handleTestServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var srv config.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "invalid JSON"})
		return
	}

	vpnIface := ""
	if h.VPNMgr != nil {
		vpnIface = h.VPNMgr.InterfaceName()
	}

	ctx, cancel := timeoutContext(10 * time.Second)
	defer cancel()

	if err := downloader.TestConnection(ctx, srv, vpnIface); err != nil {
		writeJSON(w, map[string]interface{}{
			"status":  false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		"status":  true,
		"message": "Connection successful",
	})
}

// VPN configuration handlers

func (h *Handler) handleVPN(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getVPN(w, r)
	case http.MethodPut:
		h.updateVPN(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) getVPN(w http.ResponseWriter, r *http.Request) {
	vpnCfg := h.Config.GetVPN()

	resp := map[string]interface{}{
		"enabled":   vpnCfg.Enabled,
		"protocol":  vpnCfg.Protocol,
		"interface": vpnCfg.Interface,
	}

	// WireGuard: return config with has_* booleans instead of secrets
	if vpnCfg.WireGuard != nil {
		wg := vpnCfg.WireGuard
		resp["wireguard"] = map[string]interface{}{
			"has_private_key":    wg.PrivateKey != "",
			"address":           wg.Address,
			"dns":               wg.DNS,
			"listen_port":       wg.ListenPort,
			"has_peer_public_key": wg.PeerPublicKey != "",
			"peer_endpoint":     wg.PeerEndpoint,
			"has_preshared_key": wg.PresharedKey != "",
			"allowed_ips":       wg.AllowedIPs,
			"persistent_keepalive": wg.PersistentKeepalive,
		}
	}

	// OpenVPN: return config with has_* booleans instead of secrets
	if vpnCfg.OpenVPN != nil {
		ov := vpnCfg.OpenVPN
		resp["openvpn"] = map[string]interface{}{
			"remote_host":      ov.RemoteHost,
			"remote_port":      ov.RemotePort,
			"protocol":         ov.Protocol,
			"auth_type":        ov.AuthType,
			"has_username":     ov.Username != "",
			"has_password":     ov.Password != "",
			"has_ca_cert":      ov.CACert != "",
			"has_client_cert":  ov.ClientCert != "",
			"has_client_key":   ov.ClientKey != "",
			"has_tls_auth":     ov.TLSAuth != "",
			"cipher":           ov.Cipher,
			"auth":             ov.Auth,
			"compress":         ov.Compress,
			"device_type":      ov.DeviceType,
		}
	}

	writeJSON(w, resp)
}

func (h *Handler) updateVPN(w http.ResponseWriter, r *http.Request) {
	var req config.VPNConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "invalid JSON"})
		return
	}

	existing := h.Config.GetVPN()

	// For managed protocols, merge secrets: empty fields = keep existing
	if req.WireGuard != nil && existing.WireGuard != nil {
		if req.WireGuard.PrivateKey == "" {
			req.WireGuard.PrivateKey = existing.WireGuard.PrivateKey
		}
		if req.WireGuard.PeerPublicKey == "" {
			req.WireGuard.PeerPublicKey = existing.WireGuard.PeerPublicKey
		}
		if req.WireGuard.PresharedKey == "" {
			req.WireGuard.PresharedKey = existing.WireGuard.PresharedKey
		}
	}
	if req.OpenVPN != nil && existing.OpenVPN != nil {
		if req.OpenVPN.Username == "" {
			req.OpenVPN.Username = existing.OpenVPN.Username
		}
		if req.OpenVPN.Password == "" {
			req.OpenVPN.Password = existing.OpenVPN.Password
		}
		if req.OpenVPN.CACert == "" {
			req.OpenVPN.CACert = existing.OpenVPN.CACert
		}
		if req.OpenVPN.ClientCert == "" {
			req.OpenVPN.ClientCert = existing.OpenVPN.ClientCert
		}
		if req.OpenVPN.ClientKey == "" {
			req.OpenVPN.ClientKey = existing.OpenVPN.ClientKey
		}
		if req.OpenVPN.TLSAuth == "" {
			req.OpenVPN.TLSAuth = existing.OpenVPN.TLSAuth
		}
	}

	h.Config.SetVPN(req)
	if err := h.Config.Save(); err != nil {
		log.Printf("Error saving config: %v", err)
		writeJSON(w, map[string]interface{}{"status": false, "error": "failed to save config"})
		return
	}

	// Reconfigure the VPN manager with new settings
	if h.VPNMgr != nil {
		h.VPNMgr.Reconfigure()
	}

	log.Printf("VPN config updated (protocol: %s)", req.Protocol)
	writeJSON(w, map[string]interface{}{"status": true})
}

func (h *Handler) handleVPNConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.VPNMgr == nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "VPN manager not initialized"})
		return
	}

	if !h.VPNMgr.IsManaged() {
		// Config may have been saved but manager not yet reconfigured
		vpnCfg := h.Config.GetVPN()
		if vpnCfg.Protocol == "" {
			writeJSON(w, map[string]interface{}{"status": false, "error": "VPN is in passive mode — configure a protocol first, then save"})
			return
		}
		h.VPNMgr.Reconfigure()
	}

	if err := h.VPNMgr.Connect(); err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
		return
	}

	writeJSON(w, map[string]interface{}{"status": true})
}

func (h *Handler) handleVPNDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.VPNMgr == nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": "VPN manager not initialized"})
		return
	}

	if err := h.VPNMgr.Disconnect(); err != nil {
		writeJSON(w, map[string]interface{}{"status": false, "error": err.Error()})
		return
	}

	writeJSON(w, map[string]interface{}{"status": true})
}

func (h *Handler) handleVPNStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.VPNMgr == nil {
		writeJSON(w, map[string]interface{}{
			"state":     vpn.StateDisconnected,
			"managed":   false,
		})
		return
	}

	cs := h.VPNMgr.ConnectorStatus()
	resp := map[string]interface{}{
		"state":          cs.State,
		"interface_name": cs.InterfaceName,
		"error":          cs.Error,
		"managed":        h.VPNMgr.IsManaged(),
	}
	if !cs.ConnectedAt.IsZero() {
		resp["connected_at"] = cs.ConnectedAt.Format(time.RFC3339)
		resp["uptime_seconds"] = int(time.Since(cs.ConnectedAt).Seconds())
	}

	writeJSON(w, resp)
}

func timeoutContext(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func mapStatusToSAB(status string) string {
	switch status {
	case queue.StatusQueued:
		return "Queued"
	case queue.StatusDownloading:
		return "Downloading"
	case queue.StatusProcessing:
		return "Extracting"
	default:
		return status
	}
}

func mapStatusToSABHistory(status string) string {
	switch status {
	case queue.StatusCompleted:
		return "Completed"
	case queue.StatusFailed:
		return "Failed"
	default:
		return status
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
