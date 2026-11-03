package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed jobs.json
var jobsFile []byte

func loadJobs() ([]map[string]any, error) {
	var jobs []map[string]any
	if err := json.Unmarshal(jobsFile, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.jobs})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, job := range s.jobs {
		if jobID, _ := job["id"].(string); jobID == id {
			writeJSON(w, http.StatusOK, map[string]any{"job": job})
			return
		}
	}
	writeError(w, http.StatusNotFound, "Role not found.")
}
