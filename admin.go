package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
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

func (s *Server) deleteAdminUser(w http.ResponseWriter, r *http.Request) {
	admin, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusNotFound, "User not found.")
		return
	}
	if id == admin.ID {
		writeError(w, http.StatusBadRequest, "You cannot delete your own account.")
		return
	}
	var isAdmin bool
	err := s.db.QueryRow(r.Context(), `SELECT is_admin FROM users WHERE id=$1`, id).Scan(&isAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "User not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete that user.")
		return
	}
	if isAdmin {
		writeError(w, http.StatusBadRequest, "Admin accounts cannot be deleted.")
		return
	}
	paths, err := s.resumePathsForUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete that user.")
		return
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM users WHERE id=$1 AND is_admin=FALSE`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete that user.")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "User not found.")
		return
	}
	for _, stored := range paths {
		s.removeResumeFile(stored)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) resumePathsForUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT resume_path FROM applications WHERE user_id=$1 AND resume_path <> ''`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var stored string
		if err := rows.Scan(&stored); err != nil {
			return nil, err
		}
		paths = append(paths, stored)
	}
	return paths, rows.Err()
}

func (s *Server) listAdminJobViews(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT job_id, view_count FROM job_views ORDER BY job_id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load role views.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var jobID string
		var count int64
		if err := rows.Scan(&jobID, &count); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load role views.")
			return
		}
		items = append(items, map[string]any{"jobId": jobID, "count": count})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load role views.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"views": items})
}

var applicationCallTypes = []string{"Phone", "WhatsApp", "Video", "In person"}

var applicationDecisionStatuses = []string{"Approved", "Rejected", "Waiting", "Message sent"}

type applicationReview struct {
	ViewedBy string
	CallType string
	Date     string
	Status   string
	Reason   string
}

func validApplicationCallType(value string) bool {
	for _, item := range applicationCallTypes {
		if item == value {
			return true
		}
	}
	return false
}

func validApplicationStatus(value string) bool {
	for _, item := range applicationDecisionStatuses {
		if item == value {
			return true
		}
	}
	return false
}

func normalizeApplicationStatus(status string) string {
	if validApplicationStatus(status) || status == "reviewed" {
		return status
	}
	return "new"
}

func cleanViewerName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return value
}

func validReviewDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return false
	}
	earliest := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	latest := time.Now().UTC().AddDate(1, 0, 1)
	return !parsed.Before(earliest) && !parsed.After(latest)
}

func reviewNeedsReason(status string) bool {
	return status == "Rejected" || status == "Waiting"
}

func cleanReviewReason(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "Enter a reason."
	}
	if len([]rune(value)) > 500 {
		return "", "Keep the reason under 500 characters."
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return "", "Enter a reason."
		}
	}
	return value, ""
}

func validateApplicationReview(in applicationReview) (applicationReview, string) {
	in.ViewedBy = cleanViewerName(in.ViewedBy)
	in.CallType = strings.TrimSpace(in.CallType)
	in.Date = strings.TrimSpace(in.Date)
	in.Status = strings.TrimSpace(in.Status)
	in.Reason = strings.TrimSpace(in.Reason)
	if in.ViewedBy == "" {
		return in, "Enter who viewed this application."
	}
	if !validApplicationCallType(in.CallType) {
		return in, "Choose a call type."
	}
	if !validReviewDate(in.Date) {
		return in, "Enter the review date."
	}
	if !validApplicationStatus(in.Status) {
		return in, "Choose a status."
	}
	if reviewNeedsReason(in.Status) {
		reason, message := cleanReviewReason(in.Reason)
		if message != "" {
			return in, message
		}
		in.Reason = reason
	} else {
		in.Reason = ""
	}
	return in, ""
}

func (s *Server) listAdminApplications(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT a.id, a.job_id, a.seat, a.note, a.resume_name, a.location, a.degree, a.work_authorization, a.english_fluent, a.created_at,
			a.first_name, a.last_name, a.email, a.phone, a.whatsapp, a.linkedin, a.date_of_birth, a.gender, a.heard_about,
			COALESCE(u.id, ''), COALESCE(u.name, ''), COALESCE(u.email, ''), COALESCE(u.phone, ''),
			a.status, a.call_type, a.reviewed_on, a.reviewed_by, a.rejection_reason,
			COALESCE(reviewer.id, ''), COALESCE(reviewer.name, ''), COALESCE(reviewer.email, '')
		FROM applications a
		LEFT JOIN users u ON u.id = a.user_id
		LEFT JOIN users reviewer ON reviewer.id = a.reviewed_by
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
		var firstName, lastName, email, phone, whatsapp, linkedin, dateOfBirth, gender, heardAbout string
		var userID, name, userEmail, userPhone string
		var status, callType, reviewedOn, reviewedBy, rejectionReason, reviewerID, reviewerName, reviewerEmail string
		var created time.Time
		if err := rows.Scan(
			&id, &jobID, &seat, &note, &resumeName, &location, &degree, &workAuthorization, &englishFluent, &created,
			&firstName, &lastName, &email, &phone, &whatsapp, &linkedin, &dateOfBirth, &gender, &heardAbout,
			&userID, &name, &userEmail, &userPhone,
			&status, &callType, &reviewedOn, &reviewedBy, &rejectionReason, &reviewerID, &reviewerName, &reviewerEmail,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load applications.")
			return
		}
		status = normalizeApplicationStatus(status)
		if reviewerName == "" {
			reviewerName = reviewedBy
		}
		items = append(items, map[string]any{
			"id": id, "jobId": jobID, "jobTitle": s.jobTitle(r.Context(), jobID), "seat": seat, "note": note,
			"resumeName": resumeName, "hasResume": resumeName != "", "location": location, "degree": degree,
			"workAuthorization": workAuthorization, "englishFluent": englishFluent,
			"firstName": firstName, "lastName": lastName, "email": email, "phone": phone,
			"whatsapp": whatsapp, "linkedin": linkedin, "dateOfBirth": dateOfBirth, "gender": gender, "heardAbout": heardAbout,
			"createdAt": created.UTC().Format(time.RFC3339),
			"user":      map[string]any{"id": userID, "name": name, "email": userEmail, "phone": userPhone},
			"status":    status, "callType": callType, "reviewedOn": reviewedOn, "rejectionReason": rejectionReason,
			"reviewedBy": map[string]any{"id": reviewerID, "name": reviewerName, "email": reviewerEmail},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": items})
}

func (s *Server) reviewAdminApplication(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		ViewedBy string `json:"viewedBy"`
		CallType string `json:"callType"`
		Date     string `json:"date"`
		Status   string `json:"status"`
		Reason   string `json:"reason"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Enter who viewed this application, the call type, the date, and the status.")
		return
	}
	review, message := validateApplicationReview(applicationReview{
		ViewedBy: body.ViewedBy,
		CallType: body.CallType,
		Date:     body.Date,
		Status:   body.Status,
		Reason:   body.Reason,
	})
	if message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	var status, callType, reviewedOn, rejectionReason string
	err := s.db.QueryRow(r.Context(), `
		UPDATE applications
		SET status=$1, reviewed_by=$2, call_type=$3, reviewed_on=$4, rejection_reason=$5
		WHERE id=$6 AND NOT (status = ANY($7::text[]))
		RETURNING status, call_type, reviewed_on, rejection_reason
	`, review.Status, review.ViewedBy, review.CallType, review.Date, review.Reason, id, append(append([]string{}, applicationDecisionStatuses...), "reviewed")).Scan(&status, &callType, &reviewedOn, &rejectionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing string
		lookupErr := s.db.QueryRow(r.Context(), `SELECT status FROM applications WHERE id=$1`, id).Scan(&existing)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Application not found.")
			return
		}
		if lookupErr != nil {
			writeError(w, http.StatusInternalServerError, "Could not save that review.")
			return
		}
		writeError(w, http.StatusConflict, "This application is already reviewed.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save that review.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"application": map[string]any{
			"id": id, "status": status, "callType": callType, "reviewedOn": reviewedOn, "rejectionReason": rejectionReason,
			"reviewedBy": map[string]any{"id": "", "name": review.ViewedBy, "email": ""},
		},
	})
}

func (s *Server) deleteAdminApplication(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var stored string
	err := s.db.QueryRow(r.Context(), `DELETE FROM applications WHERE id=$1 RETURNING resume_path`, id).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Application not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not delete that application.")
		return
	}
	s.removeResumeFile(stored)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	path, ok := resumeFilePath(s.resumeDir, stored)
	if !ok {
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
	title, _ := s.jobPosting(ctx, id)
	return title
}

func (s *Server) jobPosting(ctx context.Context, id string) (title, company string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ""
	}
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(payload->>'title', ''),
		       COALESCE(NULLIF(btrim(COALESCE(payload->>'company', '')), ''), 'In2AI')
		FROM jobs WHERE id=$1
	`, id).Scan(&title, &company)
	if err != nil {
		return "", ""
	}
	title = strings.TrimSpace(title)
	company = strings.TrimSpace(company)
	if title == "" {
		return "", ""
	}
	if company == "" {
		company = "In2AI"
	}
	return title, company
}

func resumeFilePath(dir, stored string) (string, bool) {
	stored = filepath.Base(strings.TrimSpace(stored))
	if stored == "" || stored == "." || stored == ".." {
		return "", false
	}
	path := filepath.Join(dir, stored)
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return path, true
}

func (s *Server) removeResumeFile(stored string) {
	path, ok := resumeFilePath(s.resumeDir, stored)
	if !ok {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("could not remove resume %s: %v", path, err)
	}
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
