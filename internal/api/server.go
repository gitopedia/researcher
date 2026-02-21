// Package api provides an HTTP API server for the researcher dashboard
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	gh "github.com/gitopedia/researcher/internal/github"
	"github.com/gitopedia/researcher/internal/queue"
	"github.com/gitopedia/researcher/internal/worker"
)

// Server is the HTTP API server for the researcher dashboard
type Server struct {
	router     *mux.Router
	httpServer *http.Server
	status     *StatusManager
	runner     *ResearchRunner
	config     *ConfigManager
	gitMgr     *GitManager
	ghClient   gh.GitHubClient

	// Worker & queue systems
	queueMgr      *queue.Manager
	workerMgr     *worker.Manager
	workerFactory *worker.Factory
	
	// WebSocket connections
	wsClients   map[*websocket.Conn]bool
	wsClientsMu sync.RWMutex
	wsBroadcast chan StatusUpdate
	
	repoPath string
}

// StatusUpdate represents a status update to broadcast via WebSocket
type StatusUpdate struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// NewServer creates a new API server. queueMgr and workerMgr may be nil for
// backward-compatible usage; when provided, workers and queue endpoints are
// available.
func NewServer(repoPath string, queueMgr *queue.Manager, workerMgr *worker.Manager, workerFactory *worker.Factory) (*Server, error) {
	s := &Server{
		router:      mux.NewRouter(),
		wsClients:   make(map[*websocket.Conn]bool),
		wsBroadcast: make(chan StatusUpdate, 100),
		repoPath:      repoPath,
		queueMgr:      queueMgr,
		workerMgr:     workerMgr,
		workerFactory: workerFactory,
	}

	// Initialize managers
	s.status = NewStatusManager()
	s.runner = NewResearchRunner(repoPath, s.broadcastStatus)
	s.config = NewConfigManager()
	s.gitMgr = NewGitManager(repoPath)

	// Initialize GitHub client (optional - may fail if no credentials)
	ctx := context.Background()
	ghClient, err := gh.NewClient(ctx)
	if err != nil {
		log.Printf("[Dashboard] GitHub client initialization failed (will run without GitHub features): %v", err)
	} else {
		s.ghClient = ghClient
		log.Printf("[Dashboard] GitHub client initialized successfully")
	}

	// Setup routes
	s.setupRoutes()

	return s, nil
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api").Subrouter()

	// Enable CORS for local development
	api.Use(corsMiddleware)

	// Status endpoints
	api.HandleFunc("/status", s.handleGetStatus).Methods("GET", "OPTIONS")
	
	// Researcher control
	api.HandleFunc("/researcher/status", s.handleResearcherStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/researcher/start", s.handleResearcherStart).Methods("POST", "OPTIONS")
	api.HandleFunc("/researcher/pause", s.handleResearcherPause).Methods("POST", "OPTIONS")
	api.HandleFunc("/researcher/resume", s.handleResearcherResume).Methods("POST", "OPTIONS")
	api.HandleFunc("/researcher/stop", s.handleResearcherStop).Methods("POST", "OPTIONS")

	// Service status (read-only health checks)
	api.HandleFunc("/services/status", s.handleServicesStatus).Methods("GET", "OPTIONS")

	// Git operations
	api.HandleFunc("/git/branch", s.handleGitBranch).Methods("GET", "OPTIONS")
	api.HandleFunc("/git/clean", s.handleGitClean).Methods("POST", "OPTIONS")

	// Configuration
	api.HandleFunc("/config", s.handleGetConfig).Methods("GET", "OPTIONS")
	api.HandleFunc("/config", s.handleUpdateConfig).Methods("PUT", "OPTIONS")

	// Topics (incoming articles)
	api.HandleFunc("/topics", s.handleListTopics).Methods("GET", "OPTIONS")
	api.HandleFunc("/topics/{slug}", s.handleGetTopic).Methods("GET", "OPTIONS")

	// Images
	api.HandleFunc("/images", s.handleListImages).Methods("GET", "OPTIONS")
	api.HandleFunc("/images/selections", s.handleGetImageSelections).Methods("GET", "OPTIONS")
	api.HandleFunc("/images/selections", s.handleUpdateImageSelection).Methods("PUT", "OPTIONS")
	api.HandleFunc("/images/group/{type}/{name}", s.handleDeleteImageGroup).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/images/single", s.handleDeleteImage).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/images/{path:.*}", s.handleGetImage).Methods("GET", "OPTIONS")

	// Finalization
	api.HandleFunc("/finalize", s.handleFinalize).Methods("POST", "OPTIONS")
	api.HandleFunc("/organize", s.handleOrganize).Methods("POST", "OPTIONS")

	// Logs
	api.HandleFunc("/logs/sources", s.handleListLogSources).Methods("GET", "OPTIONS")
	api.HandleFunc("/logs/researcher", s.handleGetResearcherLogs).Methods("GET", "OPTIONS")
	api.HandleFunc("/logs/{source}", s.handleGetLogs).Methods("GET", "OPTIONS")

	// GitHub Issue & Branch Management
	api.HandleFunc("/issues/topics", s.handleListTopicIssues).Methods("GET", "OPTIONS")
	api.HandleFunc("/issues/{number:[0-9]+}", s.handleGetIssue).Methods("GET", "OPTIONS")
	api.HandleFunc("/branch/issue", s.handleGetBranchIssue).Methods("GET", "OPTIONS")
	api.HandleFunc("/branch/delete", s.handleDeleteBranch).Methods("POST", "OPTIONS")
	api.HandleFunc("/branch/switch", s.handleSwitchBranch).Methods("POST", "OPTIONS")
	api.HandleFunc("/branch/create", s.handleCreateBranch).Methods("POST", "OPTIONS")
	api.HandleFunc("/branches", s.handleListBranches).Methods("GET", "OPTIONS")

	// Workers
	api.HandleFunc("/workers", s.handleListWorkers).Methods("GET", "OPTIONS")
	api.HandleFunc("/workers", s.handleCreateWorker).Methods("POST", "OPTIONS")
	api.HandleFunc("/workers/{id}", s.handleGetWorker).Methods("GET", "OPTIONS")
	api.HandleFunc("/workers/{id}", s.handleDeleteWorker).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/workers/{id}/start", s.handleStartWorker).Methods("POST", "OPTIONS")
	api.HandleFunc("/workers/{id}/stop", s.handleStopWorker).Methods("POST", "OPTIONS")
	api.HandleFunc("/workers/{id}/pause", s.handlePauseWorker).Methods("POST", "OPTIONS")
	api.HandleFunc("/workers/{id}/resume", s.handleResumeWorker).Methods("POST", "OPTIONS")
	api.HandleFunc("/workers/{id}/configure", s.handleConfigureWorker).Methods("PUT", "OPTIONS")

	// Queue status
	api.HandleFunc("/queue/status", s.handleQueueStatus).Methods("GET", "OPTIONS")

	// WebSocket
	api.HandleFunc("/ws", s.handleWebSocket)
}

// corsMiddleware adds CORS headers for local development
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Start starts the HTTP server
func (s *Server) Start(ctx context.Context) error {
	host := os.Getenv("DASHBOARD_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("DASHBOARD_PORT")
	if port == "" {
		port = "3001"
	}

	addr := fmt.Sprintf("%s:%s", host, port)

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start WebSocket broadcaster
	go s.runWebSocketBroadcaster()

	// Start status poller
	go s.runStatusPoller(ctx)

	log.Printf("[Dashboard] API server starting on http://%s", addr)
	log.Printf("[Dashboard] WebSocket available at ws://%s/api/ws", addr)

	return s.httpServer.ListenAndServe()
}

// Stop gracefully stops the server
func (s *Server) Stop(ctx context.Context) error {
	// Stop the research runner if running
	s.runner.Stop()

	// Stop all workers
	if s.workerMgr != nil {
		s.workerMgr.StopAll()
	}

	// Stop the queue
	if s.queueMgr != nil {
		s.queueMgr.Stop()
	}

	// Close all WebSocket connections
	s.wsClientsMu.Lock()
	for client := range s.wsClients {
		client.Close()
	}
	s.wsClientsMu.Unlock()

	return s.httpServer.Shutdown(ctx)
}

// broadcastStatus sends a status update to all WebSocket clients
func (s *Server) broadcastStatus(update StatusUpdate) {
	select {
	case s.wsBroadcast <- update:
	default:
		// Channel full, skip update
	}
}

// runWebSocketBroadcaster handles broadcasting updates to all clients
func (s *Server) runWebSocketBroadcaster() {
	for update := range s.wsBroadcast {
		s.wsClientsMu.Lock()
		deadClients := make([]*websocket.Conn, 0)
		for client := range s.wsClients {
			err := client.WriteJSON(update)
			if err != nil {
				// Check if it's a connection closure error (common and harmless)
				errStr := err.Error()
				isConnectionClosed := strings.Contains(errStr, "wsasend") ||
					strings.Contains(errStr, "broken pipe") ||
					strings.Contains(errStr, "connection reset") ||
					strings.Contains(errStr, "use of closed network connection")
				
				if isConnectionClosed {
					// Silently remove dead connections - client closed the connection
					deadClients = append(deadClients, client)
				} else {
					// Log other errors (unexpected issues)
					log.Printf("[WebSocket] Error writing to client: %v", err)
					deadClients = append(deadClients, client)
				}
			}
		}
		// Remove dead clients
		for _, client := range deadClients {
			delete(s.wsClients, client)
			client.Close()
		}
		s.wsClientsMu.Unlock()
	}
}

// runStatusPoller periodically polls and broadcasts system status
func (s *Server) runStatusPoller(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status := s.status.GetFullStatus()
			s.broadcastStatus(StatusUpdate{
				Type:    "status",
				Payload: status,
			})

			// Broadcast worker status
			if s.workerMgr != nil {
				s.broadcastStatus(StatusUpdate{
					Type:    "workers",
					Payload: s.workerMgr.GetStatus(),
				})
			}

			// Broadcast queue status
			if s.queueMgr != nil {
				s.broadcastStatus(StatusUpdate{
					Type:    "queue",
					Payload: s.queueMgr.GetStatus(),
				})
			}
		}
	}
}

// JSON response helpers
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}
