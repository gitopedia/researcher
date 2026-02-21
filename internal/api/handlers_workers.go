package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gitopedia/researcher/internal/worker"
)

// --- Worker handlers ---

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondJSON(w, http.StatusOK, []worker.Status{})
		return
	}
	respondJSON(w, http.StatusOK, s.workerMgr.GetStatus())
}

func (s *Server) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusNotFound, "worker system not initialized")
		return
	}

	id := mux.Vars(r)["id"]
	st, err := s.workerMgr.GetWorkerStatus(id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, st)
}

func (s *Server) handleCreateWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "worker system not initialized")
		return
	}

	var req struct {
		ID       string      `json:"id"`
		Type     worker.Type `json:"type"`
		Branch   string      `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ID == "" || req.Type == "" {
		respondError(w, http.StatusBadRequest, "id and type are required")
		return
	}

	cfg := worker.Config{
		ID:       req.ID,
		Type:     req.Type,
		Branch:   req.Branch,
		RepoPath: s.repoPath,
		Enabled:  true,
	}

	// Use the factory stored on the server (set during initialization)
	if s.workerFactory == nil {
		respondError(w, http.StatusServiceUnavailable, "worker factory not configured")
		return
	}

	wkr := s.workerFactory.Create(cfg)
	if err := s.workerMgr.Register(wkr); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, wkr.Status())
}

func (s *Server) handleDeleteWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "worker system not initialized")
		return
	}

	id := mux.Vars(r)["id"]
	if err := s.workerMgr.Remove(id); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleStartWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "worker system not initialized")
		return
	}

	id := mux.Vars(r)["id"]
	if err := s.workerMgr.StartWorker(r.Context(), id); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "starting"})
}

func (s *Server) handleStopWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "worker system not initialized")
		return
	}

	id := mux.Vars(r)["id"]
	if err := s.workerMgr.StopWorker(id); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (s *Server) handlePauseWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "worker system not initialized")
		return
	}

	id := mux.Vars(r)["id"]
	if err := s.workerMgr.PauseWorker(id); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (s *Server) handleResumeWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "worker system not initialized")
		return
	}

	id := mux.Vars(r)["id"]
	if err := s.workerMgr.ResumeWorker(id); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) handleConfigureWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "worker system not initialized")
		return
	}

	id := mux.Vars(r)["id"]
	var cfg worker.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg.ID = id // enforce URL param

	if err := s.workerMgr.ConfigureWorker(id, cfg); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "configured"})
}

// --- Queue handler ---

func (s *Server) handleQueueStatus(w http.ResponseWriter, r *http.Request) {
	if s.queueMgr == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{})
		return
	}
	respondJSON(w, http.StatusOK, s.queueMgr.GetStatus())
}
