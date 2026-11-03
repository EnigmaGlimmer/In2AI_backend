package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func adminEmailList() []string {
	raw := envOr("ADMIN_EMAILS", "info@in2ai.biz,us-info@in2ai.biz")
	seen := map[string]bool{}
	emails := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		email := strings.ToLower(strings.TrimSpace(part))
		if !emailPattern.MatchString(email) || seen[email] {
			continue
		}
		seen[email] = true
		emails = append(emails, email)
	}
	return emails
}

func isAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range adminEmailList() {
		if email == allowed {
			return true
		}
	}
	return false
}

func syncAdminFlags(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `UPDATE users SET is_admin = lower(email) = ANY($1::text[])`, adminEmailList())
	return err
}

func (s *Server) applyAdmin(ctx context.Context, user *User) {
	should := isAdminEmail(user.Email)
	if user.IsAdmin == should {
		return
	}
	if _, err := s.db.Exec(ctx, `UPDATE users SET is_admin=$1 WHERE id=$2`, should, user.ID); err != nil {
		return
	}
	user.IsAdmin = should
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (User, bool) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return User{}, false
	}
	if !user.IsAdmin {
		writeError(w, http.StatusForbidden, "This page is limited to admins.")
		return User{}, false
	}
	return user, true
}

func (s *Server) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, name, email, email_verified, headline, location, phone, degree, work_authorization, english_fluent, skills, is_admin, created_at
		FROM users ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load users.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, email, headline, location, phone, degree, workAuthorization, englishFluent, skills string
		var verified, admin bool
		var created time.Time
		if err := rows.Scan(&id, &name, &email, &verified, &headline, &location, &phone, &degree, &workAuthorization, &englishFluent, &skills, &admin, &created); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load users.")
			return
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "email": email, "emailVerified": verified, "headline": headline,
			"location": location, "phone": phone, "degree": degree, "workAuthorization": workAuthorization,
			"englishFluent": englishFluent, "skills": skills, "isAdmin": admin,
			"createdAt": created.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": items})
}

func (s *Server) listAdminApplications(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT a.id, a.job_id, a.seat, a.note, a.resume_name, a.location, a.degree, a.work_authorization, a.english_fluent, a.created_at,
			a.first_name, a.last_name, a.email, a.phone, a.whatsapp, a.linkedin, a.date_of_birth, a.gender,
			COALESCE(u.id, ''), COALESCE(u.name, ''), COALESCE(u.email, ''), COALESCE(u.phone, '')
		FROM applications a
		LEFT JOIN users u ON u.id = a.user_id
		ORDER BY a.created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load applications.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, jobID, seat, note, resumeName, location, degree, workAuthorization, englishFluent string
		var firstName, lastName, email, phone, whatsapp, linkedin, dateOfBirth, gender string
		var userID, name, userEmail, userPhone string
		var created time.Time
		if err := rows.Scan(
			&id, &jobID, &seat, &note, &resumeName, &location, &degree, &workAuthorization, &englishFluent, &created,
			&firstName, &lastName, &email, &phone, &whatsapp, &linkedin, &dateOfBirth, &gender,
			&userID, &name, &userEmail, &userPhone,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load applications.")
			return
		}
		items = append(items, map[string]any{
			"id": id, "jobId": jobID, "jobTitle": s.jobTitle(r.Context(), jobID), "seat": seat, "note": note,
			"resumeName": resumeName, "hasResume": resumeName != "", "location": location, "degree": degree,
			"workAuthorization": workAuthorization, "englishFluent": englishFluent,
			"firstName": firstName, "lastName": lastName, "email": email, "phone": phone,
			"whatsapp": whatsapp, "linkedin": linkedin, "dateOfBirth": dateOfBirth, "gender": gender,
			"createdAt": created.UTC().Format(time.RFC3339),
			"user":      map[string]any{"id": userID, "name": name, "email": userEmail, "phone": userPhone},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": items})
}

func (s *Server) adminResume(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var stored, download string
	err := s.db.QueryRow(r.Context(), `SELECT resume_path, resume_name FROM applications WHERE id=$1`, r.PathValue("id")).Scan(&stored, &download)
	if err != nil || stored == "" {
		writeError(w, http.StatusNotFound, "Resume not found.")
		return
	}
	stored = filepath.Base(stored)
	path := filepath.Join(s.resumeDir, stored)
	rel, err := filepath.Rel(s.resumeDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		writeError(w, http.StatusNotFound, "Resume not found.")
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, "Resume not found.")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safeDownloadName(download)+"\"")
	http.ServeFile(w, r, path)
}

func (s *Server) jobTitle(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	var title string
	err := s.db.QueryRow(ctx, `SELECT COALESCE(payload->>'title', '') FROM jobs WHERE id=$1`, id).Scan(&title)
	if err != nil {
		return ""
	}
	return title
}

func safeDownloadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r == '/' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." {
		return "resume"
	}
	return name
}
