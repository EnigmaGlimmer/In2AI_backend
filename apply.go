package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

var applicationLocations = map[string]bool{
	"United States":  true,
	"Canada":         true,
	"United Kingdom": true,
	"Mexico":         true,
	"Colombia":       true,
	"Argentina":      true,
	"Brazil":         true,
	"South Africa":   true,
	"Philippines":    true,
	"India":          true,
	"Pakistan":       true,
	"Japan":          true,
	"Singapore":      true,
	"Australia":      true,
	"Europe":         true,
	"Other":          true,
}

var applicationGenders = map[string]bool{
	"Male":                     true,
	"Female":                   true,
	"Decline to self-identify": true,
}

type guestApplication struct {
	FirstName         string
	LastName          string
	Email             string
	Phone             string
	WhatsApp          string
	LinkedIn          string
	Location          string
	HeardAbout        string
	DateOfBirth       string
	WorkAuthorization string
	Gender            string
}

func (s *Server) createGuestApplication(w http.ResponseWriter, r *http.Request) {
	if !s.allow("apply:"+clientIP(r), 8, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "Too many applications. Try again in a few minutes.")
		return
	}
	fields, message := validateGuestApplication(guestApplication{
		FirstName:         r.FormValue("firstName"),
		LastName:          r.FormValue("lastName"),
		Email:             r.FormValue("email"),
		Phone:             r.FormValue("phone"),
		WhatsApp:          r.FormValue("whatsapp"),
		LinkedIn:          r.FormValue("linkedin"),
		Location:          r.FormValue("location"),
		HeardAbout:        r.FormValue("heardAbout"),
		DateOfBirth:       r.FormValue("dateOfBirth"),
		WorkAuthorization: r.FormValue("workAuthorization"),
		Gender:            r.FormValue("gender"),
	})
	if message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}
	jobID := strings.TrimSpace(r.FormValue("jobId"))
	seat, company := s.jobPosting(r.Context(), jobID)
	if seat == "" {
		writeError(w, http.StatusBadRequest, "That role is no longer open.")
		return
	}
	resumeName, resumePath, err := s.saveResume(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var userID any
	if user, ok := s.currentUser(r); ok {
		userID = user.ID
	}
	id := newID()
	_, err = s.db.Exec(r.Context(), `
		INSERT INTO applications (
			id, user_id, job_id, seat, resume_name, resume_path, location, work_authorization,
			first_name, last_name, email, phone, whatsapp, linkedin, date_of_birth, gender, heard_about
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
	`, id, userID, jobID, seat, resumeName, resumePath, fields.Location, fields.WorkAuthorization,
		fields.FirstName, fields.LastName, fields.Email, fields.Phone, fields.WhatsApp, fields.LinkedIn, fields.DateOfBirth, fields.Gender, fields.HeardAbout)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not send that application.")
		return
	}
	s.resend.sendApplicationThankYou(fields.Email, fields.FirstName, seat, company)
	writeJSON(w, http.StatusCreated, map[string]any{"application": map[string]any{
		"id": id, "jobId": jobID, "seat": seat, "resumeName": resumeName,
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	}})
}

func validateGuestApplication(in guestApplication) (guestApplication, string) {
	in.FirstName = cleanPersonName(in.FirstName)
	in.LastName = cleanPersonName(in.LastName)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Phone = strings.TrimSpace(in.Phone)
	in.WhatsApp = strings.TrimSpace(in.WhatsApp)
	in.LinkedIn = strings.TrimSpace(in.LinkedIn)
	in.Location = strings.TrimSpace(in.Location)
	in.HeardAbout = strings.TrimSpace(in.HeardAbout)
	in.DateOfBirth = strings.TrimSpace(in.DateOfBirth)
	in.WorkAuthorization = strings.TrimSpace(in.WorkAuthorization)
	in.Gender = strings.TrimSpace(in.Gender)

	if in.FirstName == "" || len(in.FirstName) > 60 {
		return in, "Enter your first name."
	}
	if in.LastName == "" || len(in.LastName) > 60 {
		return in, "Enter your last name."
	}
	if !emailPattern.MatchString(in.Email) || len(in.Email) > 255 {
		return in, "Enter a valid email address."
	}
	if !validPhone(in.Phone) {
		return in, "Enter a valid phone number."
	}
	if in.WhatsApp != "" && !validPhone(in.WhatsApp) {
		return in, "Enter a valid WhatsApp number, or leave it blank."
	}
	linkedIn, ok := normalizeLinkedIn(in.LinkedIn)
	if !ok {
		return in, "Enter a LinkedIn profile link, or leave it blank."
	}
	in.LinkedIn = linkedIn
	if !applicationLocations[in.Location] {
		return in, "Choose your location."
	}
	if !validHeardAbout(in.HeardAbout) {
		return in, "Tell us how you initially heard about this job."
	}
	born, err := time.Parse("2006-01-02", in.DateOfBirth)
	if err != nil || !born.Before(time.Now()) || born.Year() < 1920 {
		return in, "Enter a valid date of birth."
	}
	if in.WorkAuthorization != "Yes" && in.WorkAuthorization != "No" {
		return in, "Tell us whether you are legally authorized to work in the United States."
	}
	if !applicationGenders[in.Gender] {
		return in, "Choose a gender option."
	}
	return in, ""
}

func cleanPersonName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 60 {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return value
}

func validHeardAbout(value string) bool {
	if value == "" || len(value) > 500 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validPhone(value string) bool {
	if len(value) < 7 || len(value) > 30 {
		return false
	}
	digits := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case r == '+' || r == '-' || r == ' ' || r == '(' || r == ')':
		default:
			return false
		}
	}
	return digits >= 7 && digits <= 15
}

func normalizeLinkedIn(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	if len(value) > 300 {
		return "", false
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "linkedin.com" && host != "www.linkedin.com" {
		return "", false
	}
	return parsed.String(), true
}
