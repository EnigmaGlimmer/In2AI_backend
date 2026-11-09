package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	db                 *pgxpool.Pool
	secret             []byte
	isProd             bool
	appOrigin          string
	googleClientID     string
	googleClientSecret string
	redirectURL        string
	resumeDir          string
	mail               mailConfig
	resend             resendConfig
	attempts           map[string][]time.Time
	mu                 sync.Mutex
}

var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func main() {
	loadEnvFiles()
	databaseURL := preferIPv4(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required, for example postgres://coral@127.0.0.1:5433/coral?sslmode=disable")
	}
	secret := os.Getenv("AUTH_SECRET")
	if secret == "" {
		log.Fatal("AUTH_SECRET is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := openDB(ctx, databaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()
	if err := syncAdminFlags(ctx, pool); err != nil {
		log.Fatalf("admin flags: %v", err)
	}
	synced, err := syncJobs(ctx, pool)
	if err != nil {
		log.Fatalf("jobs: %v", err)
	}
	log.Printf("synced %d jobs from the committed catalog", synced)

	isProd := os.Getenv("APP_ENV") == "production" || os.Getenv("NODE_ENV") == "production"
	origin := absoluteOrigin(envOr("APP_ORIGIN", "http://127.0.0.1:3000"))
	redirectURL := absoluteRedirect(os.Getenv("GOOGLE_REDIRECT_URL"), origin)
	resumeDir := "data/resumes"
	if info, statErr := os.Stat("backend"); statErr == nil && info.IsDir() {
		resumeDir = filepath.Join("backend", "data", "resumes")
	}
	if err := os.MkdirAll(resumeDir, 0o755); err != nil {
		log.Fatal(err)
	}

	server := &Server{
		db:                 pool,
		secret:             []byte(secret),
		isProd:             isProd,
		appOrigin:          origin,
		googleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		googleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		redirectURL:        redirectURL,
		resumeDir:          resumeDir,
		mail:               loadMailConfig(),
		resend:             loadResendConfig(),
		attempts:           map[string][]time.Time{},
	}
	if !server.mail.configured() {
		log.Print("Verification email is off until SMTP_HOST, SMTP_USERNAME, and SMTP_PASSWORD are set")
	}
	if !server.resend.configured() {
		log.Print("Application thank-you email is off until RESEND_API_KEY is set")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("POST /api/auth/register", server.register)
	mux.HandleFunc("POST /api/auth/login", server.login)
	mux.HandleFunc("POST /api/auth/logout", server.logout)
	mux.HandleFunc("GET /api/auth/me", server.me)
	mux.HandleFunc("PATCH /api/auth/me", server.updateMe)
	mux.HandleFunc("POST /api/auth/resend-verification", server.resendVerification)
	mux.HandleFunc("POST /api/auth/verify", server.verifyEmail)
	mux.HandleFunc("POST /api/auth/forgot", server.forgotPassword)
	mux.HandleFunc("POST /api/auth/reset", server.resetPassword)
	mux.HandleFunc("GET /api/auth/google", server.googleStart)
	mux.HandleFunc("GET /api/auth/google/callback", server.googleCallback)
	mux.HandleFunc("GET /api/applications", server.listApplications)
	mux.HandleFunc("POST /api/applications", server.createApplication)
	mux.HandleFunc("GET /api/jobs", server.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", server.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/view", server.recordJobView)
	mux.HandleFunc("GET /api/admin/job-views", server.listAdminJobViews)
	mux.HandleFunc("GET /api/admin/users", server.listAdminUsers)
	mux.HandleFunc("DELETE /api/admin/users/{id}", server.deleteAdminUser)
	mux.HandleFunc("GET /api/admin/applications", server.listAdminApplications)
	mux.HandleFunc("DELETE /api/admin/applications/{id}", server.deleteAdminApplication)
	mux.HandleFunc("GET /api/admin/applications/{id}/resume", server.adminResume)
	if isProd {
		mux.HandleFunc("/", server.static)
	}

	port := envOr("PORT", "8000")
	addr := "0.0.0.0:" + port
	log.Printf("Auth API listening on http://%s", addr)
	if !server.googleConfigured() {
		log.Print("Google sign-in is off until GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are set")
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) currentUser(r *http.Request) (User, bool) {
	id, ok := s.sessionUserID(r)
	if !ok {
		return User{}, false
	}
	user, err := s.userByID(r.Context(), id)
	if err != nil {
		return User{}, false
	}
	s.applyAdmin(r.Context(), &user)
	return user, true
}

type credentials struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Honeypot string `json:"hp_field"`
	Token    string `json:"token"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.allow("register:"+clientIP(r), 8, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
		return
	}
	var body credentials
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Could not read that request.")
		return
	}
	if body.Honeypot != "" {
		writeError(w, http.StatusBadRequest, "Could not create that profile.")
		return
	}
	name := strings.TrimSpace(body.Name)
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if len(name) < 2 || len(name) > 100 {
		writeError(w, http.StatusBadRequest, "Enter your full name.")
		return
	}
	if !emailPattern.MatchString(email) || len(email) > 255 {
		writeError(w, http.StatusBadRequest, "Enter a valid email address.")
		return
	}
	if len(body.Password) < 8 || len(body.Password) > 200 {
		writeError(w, http.StatusBadRequest, "Password must be at least 8 characters.")
		return
	}
	if existing, err := s.userByEmail(r.Context(), email); err == nil {
		if existing.PasswordHash == "" {
			writeError(w, http.StatusConflict, "An account with that email already exists. Continue with Google.")
			return
		}
		writeError(w, http.StatusConflict, "An account with that email already exists. Sign in instead.")
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "Could not create that profile.")
		return
	}

	hash, err := hashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create that profile.")
		return
	}
	user := User{ID: newID(), Name: name, Email: email, PasswordHash: hash, IsAdmin: isAdminEmail(email)}
	token, tokenSum := s.issueToken(user.ID, "verify", 30)
	user.VerifyHash = tokenSum
	if err := s.insertUser(r.Context(), user); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "An account with that email already exists. Sign in instead.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not create that profile.")
		return
	}
	s.setSession(w, user.ID)
	emailSent := s.sendVerificationEmail(name, email, token)
	payload := map[string]any{"user": s.publicUser(user), "emailSent": emailSent}
	if !s.isProd {
		payload["devVerifyToken"] = token
	}
	writeJSON(w, http.StatusCreated, payload)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allow("login:"+clientIP(r), 12, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
		return
	}
	var body credentials
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Could not sign in.")
		return
	}
	if body.Honeypot != "" {
		writeError(w, http.StatusBadRequest, "Could not sign in.")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	user, err := s.userByEmail(r.Context(), email)
	if err != nil || !checkPassword(user.PasswordHash, body.Password) {
		if err == nil && user.PasswordHash == "" {
			writeError(w, http.StatusUnauthorized, "This profile uses Google. Continue with Google.")
			return
		}
		writeError(w, http.StatusUnauthorized, "Email or password is incorrect.")
		return
	}
	s.applyAdmin(r.Context(), &user)
	s.setSession(w, user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"user": s.publicUser(user)})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	s.clearSession(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"user": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": s.publicUser(user)})
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	current, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "Sign in to update your profile.")
		return
	}
	var body map[string]any
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Could not read that request.")
		return
	}
	fields := map[string]string{
		"name": "name", "headline": "headline", "location": "location", "phone": "phone",
		"bio": "bio", "degree": "degree", "workAuthorization": "work_authorization",
		"englishFluent": "english_fluent", "skills": "skills",
	}
	sets := make([]string, 0)
	args := make([]any, 0)
	for key, column := range fields {
		raw, present := body[key]
		if !present {
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(raw))
		if len(value) > 2000 {
			writeError(w, http.StatusBadRequest, "One of the fields is too long.")
			return
		}
		if key == "name" && (len(value) < 2 || len(value) > 100) {
			writeError(w, http.StatusBadRequest, "Enter your full name.")
			return
		}
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if len(sets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"user": s.publicUser(current)})
		return
	}
	args = append(args, current.ID)
	query := fmt.Sprintf("UPDATE users SET %s WHERE id=$%d", strings.Join(sets, ", "), len(args))
	if _, err := s.db.Exec(r.Context(), query, args...); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save your profile.")
		return
	}
	updated, err := s.userByID(r.Context(), current.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not save your profile.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": s.publicUser(updated)})
}

func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	current, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "Sign in to resend the confirmation.")
		return
	}
	if current.EmailVerified {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "alreadyVerified": true})
		return
	}
	token, sum := s.issueToken(current.ID, "verify", 30)
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET verify_hash=$1 WHERE id=$2`, sum, current.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resend the confirmation.")
		return
	}
	if s.sendVerificationEmail(current.Name, current.Email, token) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "emailSent": true})
		return
	}
	if !s.isProd {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "emailSent": false, "devVerifyToken": token})
		return
	}
	writeError(w, http.StatusBadGateway, "Could not send the confirmation email. Try again in a moment.")
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Token) == "" {
		writeError(w, http.StatusBadRequest, "This confirmation link is invalid or expired.")
		return
	}
	payload, ok := s.readSigned(body.Token)
	if !ok || payload.Typ != "verify" {
		writeError(w, http.StatusBadRequest, "This confirmation link is invalid or expired.")
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE users SET email_verified=TRUE, verify_hash=''
		WHERE id=$1 AND verify_hash=$2 AND verify_hash<>''
	`, payload.Sub, tokenHash(body.Token))
	if err != nil || tag.RowsAffected() != 1 {
		writeError(w, http.StatusBadRequest, "This confirmation link is invalid or expired.")
		return
	}
	user, err := s.userByID(r.Context(), payload.Sub)
	if err != nil {
		writeError(w, http.StatusBadRequest, "This confirmation link is invalid or expired.")
		return
	}
	s.setSession(w, user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"user": s.publicUser(user)})
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.allow("forgot:"+clientIP(r), 6, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
		return
	}
	var body credentials
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	user, err := s.userByEmail(r.Context(), email)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	token, sum := s.issueToken(user.ID, "reset", 30)
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET reset_hash=$1 WHERE id=$2`, sum, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not start a reset.")
		return
	}
	s.logDevLink("Password reset", email, "/reset-password?token="+url.QueryEscape(token))
	payload := map[string]any{"ok": true}
	if !s.isProd {
		payload["devResetToken"] = token
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "This reset link is invalid or expired.")
		return
	}
	if len(body.Password) < 8 || len(body.Password) > 200 {
		writeError(w, http.StatusBadRequest, "Password must be at least 8 characters.")
		return
	}
	payload, ok := s.readSigned(body.Token)
	if !ok || payload.Typ != "reset" {
		writeError(w, http.StatusBadRequest, "This reset link is invalid or expired.")
		return
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not reset that password.")
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE users SET password_hash=$1, reset_hash=''
		WHERE id=$2 AND reset_hash=$3 AND reset_hash<>''
	`, hash, payload.Sub, tokenHash(body.Token))
	if err != nil || tag.RowsAffected() != 1 {
		writeError(w, http.StatusBadRequest, "This reset link is invalid or expired.")
		return
	}
	user, err := s.userByID(r.Context(), payload.Sub)
	if err != nil {
		writeError(w, http.StatusBadRequest, "This reset link is invalid or expired.")
		return
	}
	s.setSession(w, user.ID)
	writeJSON(w, http.StatusOK, map[string]any{"user": s.publicUser(user)})
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "Sign in to see your applications.")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, job_id, seat, note, resume_name, created_at
		FROM applications WHERE user_id=$1 ORDER BY created_at DESC
	`, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load applications.")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var item Application
		if err := rows.Scan(&item.ID, &item.JobID, &item.Seat, &item.Note, &item.ResumeName, &item.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not load applications.")
			return
		}
		items = append(items, map[string]any{
			"id": item.ID, "jobId": item.JobID, "seat": item.Seat, "note": item.Note,
			"resumeName": item.ResumeName, "createdAt": item.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": items})
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Resume must be 5 MB or smaller.")
		return
	}
	if r.FormValue("hp_field") != "" {
		writeError(w, http.StatusBadRequest, "Could not send that application.")
		return
	}
	if r.FormValue("applyMode") == "guest" {
		s.createGuestApplication(w, r)
		return
	}
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "Create a profile before you apply.")
		return
	}
	if !user.EmailVerified {
		writeError(w, http.StatusForbidden, "Confirm your email before you apply.")
		return
	}
	seat := strings.TrimSpace(r.FormValue("seat"))
	if seat == "" {
		seat = "General introduction"
	}
	location := strings.TrimSpace(r.FormValue("location"))
	if location == "" {
		location = user.Location
	}
	degree := strings.TrimSpace(r.FormValue("degree"))
	if degree == "" {
		degree = user.Degree
	}
	workAuthorization := strings.TrimSpace(r.FormValue("workAuthorization"))
	englishFluent := strings.TrimSpace(r.FormValue("englishFluent"))
	note := strings.TrimSpace(r.FormValue("note"))
	jobID := strings.TrimSpace(r.FormValue("jobId"))
	if location == "" {
		writeError(w, http.StatusBadRequest, "Choose your current location.")
		return
	}
	if workAuthorization != "Yes" && workAuthorization != "No" {
		writeError(w, http.StatusBadRequest, "Tell us whether you can legally work where you live.")
		return
	}
	if englishFluent != "Yes" && englishFluent != "No" {
		writeError(w, http.StatusBadRequest, "Tell us whether you are fluent in daily English.")
		return
	}
	if len(note) > 4000 {
		writeError(w, http.StatusBadRequest, "The note is too long.")
		return
	}
	if len(seat) < 2 || len(seat) > 160 {
		writeError(w, http.StatusBadRequest, "Choose a seat.")
		return
	}

	resumeName, resumePath, err := s.saveResume(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item := Application{
		ID: newID(), UserID: user.ID, JobID: jobID, Seat: seat, Note: note, ResumeName: resumeName,
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not send that application.")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `
		INSERT INTO applications (
			id, user_id, job_id, seat, note, resume_name, resume_path, location, degree, work_authorization, english_fluent
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, item.ID, item.UserID, item.JobID, item.Seat, item.Note, resumeName, resumePath, location, degree, workAuthorization, englishFluent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not send that application.")
		return
	}
	_, err = tx.Exec(r.Context(), `
		UPDATE users SET location=$1, degree=CASE WHEN $2<>'' THEN $2 ELSE degree END,
			work_authorization=$3, english_fluent=$4 WHERE id=$5
	`, location, degree, workAuthorization, englishFluent, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not send that application.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not send that application.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"application": map[string]any{
		"id": item.ID, "jobId": item.JobID, "seat": item.Seat, "note": item.Note,
		"resumeName": resumeName, "createdAt": time.Now().UTC().Format(time.RFC3339),
	}})
}

func (s *Server) saveResume(r *http.Request) (string, string, error) {
	file, header, err := r.FormFile("resume")
	if err == http.ErrMissingFile {
		return "", "", nil
	}
	if err != nil {
		return "", "", errors.New("Could not read that resume.")
	}
	defer file.Close()
	if header.Size > 5<<20 {
		return "", "", errors.New("Resume must be 5 MB or smaller.")
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".pdf" && ext != ".doc" && ext != ".docx" {
		return "", "", errors.New("Resume must be a PDF or Word document.")
	}
	name := newID() + ext
	dest, err := os.Create(filepath.Join(s.resumeDir, name))
	if err != nil {
		return "", "", errors.New("Could not store that resume.")
	}
	defer dest.Close()
	if _, err := io.Copy(dest, io.LimitReader(file, 5<<20+1)); err != nil {
		return "", "", errors.New("Could not store that resume.")
	}
	return header.Filename, name, nil
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	dist, err := filepath.Abs(filepath.Join("..", "dist"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	clean := filepath.Clean(r.URL.Path)
	target := filepath.Join(dist, clean)
	rel, err := filepath.Rel(dist, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.ServeFile(w, r, filepath.Join(dist, "index.html"))
		return
	}
	http.ServeFile(w, r, target)
}

func (s *Server) sendVerificationEmail(name, email, token string) bool {
	link := s.appOrigin + "/verify-email?token=" + url.QueryEscape(token)
	subject, textBody, htmlBody := verificationEmail(name, link)
	if !s.mail.configured() {
		s.logDevLink("Email confirmation", email, "/verify-email?token="+url.QueryEscape(token))
		return false
	}
	if err := s.mail.send(email, subject, textBody, htmlBody); err != nil {
		log.Printf("verification email failed for %s: %v", email, err)
		s.logDevLink("Email confirmation", email, "/verify-email?token="+url.QueryEscape(token))
		return false
	}
	log.Printf("verification email sent to %s", email)
	return true
}

func (s *Server) logDevLink(label, email, path string) {
	if s.isProd {
		return
	}
	log.Printf("%s for %s: %s%s", label, email, s.appOrigin, path)
}

func absoluteOrigin(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "http://127.0.0.1:3000"
	}
	return parsed.Scheme + "://" + parsed.Host
}

func absoluteRedirect(value, origin string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return origin + "/api/auth/google/callback"
	}
	return parsed.String()
}

func preferIPv4(databaseURL string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Hostname() != "localhost" {
		return databaseURL
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	parsed.Host = net.JoinHostPort("127.0.0.1", port)
	return parsed.String()
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loadEnvFiles() {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	// Only the backend env file. Never read the frontend .env.
	for _, path := range []string{filepath.Join(wd, ".env"), filepath.Join(wd, "backend", ".env")} {
		loadEnvFile(path)
	}
}

func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
