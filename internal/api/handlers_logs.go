package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gitopedia/researcher/internal/logging"
)

// handleListLogSources returns the list of available log sources.
func (s *Server) handleListLogSources(w http.ResponseWriter, r *http.Request) {
	sources := logging.ListLogSources()
	// Return as a flat array to match frontend expectations
	respondJSON(w, http.StatusOK, sources)
}

// handleGetLogs returns log content for a specific named source.
func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	source := mux.Vars(r)["source"]

	// Default to 512KB max
	var maxBytes int64 = 512 * 1024

	content, err := logging.ReadNamedLog(source, maxBytes)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	// Return with "text" key to match the researcher logs response format
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"source": source,
		"text":   content,
	})
}
