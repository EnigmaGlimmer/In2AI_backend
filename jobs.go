package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// jobs.json is the catalog shipped with each backend deploy. Startup copies
// it into the jobs table: new and edited roles are upserted, and roles removed
// from the file are deleted. Commit a change to jobs.json and deploy the
// backend for the live listings to match.
//
//go:embed jobs.json
var jobsFile []byte

func loadJobs() ([]map[string]any, error) {
	var jobs []map[string]any
	if err := json.Unmarshal(jobsFile, &jobs); err != nil {
		return nil, err
	}
	for _, job := range jobs {
		normalizeJob(job)
	}
	return jobs, nil
}

func normalizeJob(job map[string]any) {
	if company, _ := job["company"].(string); strings.TrimSpace(company) == "" {
		job["company"] = "In2AI"
	}
}

func jobPublishedAt(job map[string]any) time.Time {
	raw, _ := job["publishedAt"].(string)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if published, err := time.Parse(layout, raw); err == nil {
			return published.UTC()
		}
	}
	return time.Now().UTC()
}

func syncJobs(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	jobs, err := loadJobs()
	if err != nil {
		return 0, err
	}
	if len(jobs) == 0 {
		return 0, errors.New("job catalog is empty")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	ids := make([]string, 0, len(jobs))
	seen := map[string]struct{}{}
	for _, job := range jobs {
		id, _ := job["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return 0, errors.New("job is missing an id")
		}
		if _, ok := seen[id]; ok {
			return 0, errors.New("duplicate job id " + id)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		payload, err := json.Marshal(job)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (id, payload, published_at, updated_at)
			VALUES ($1, $2::jsonb, $3, NOW())
			ON CONFLICT (id) DO UPDATE SET
				payload = EXCLUDED.payload,
				published_at = EXCLUDED.published_at,
				updated_at = NOW()
		`, id, string(payload), jobPublishedAt(job)); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM jobs WHERE NOT (id = ANY($1::text[]))`, ids); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobsFromDB(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load roles.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT payload FROM jobs WHERE id=$1`, r.PathValue("id")).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Role not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load that role.")
		return
	}
	var job map[string]any
	if err := json.Unmarshal(raw, &job); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load that role.")
		return
	}
	normalizeJob(job)
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) jobsFromDB(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `SELECT payload FROM jobs ORDER BY published_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]map[string]any, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var job map[string]any
		if err := json.Unmarshal(raw, &job); err != nil {
			return nil, err
		}
		normalizeJob(job)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}
