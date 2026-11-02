package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type tokenPayload struct {
	Sub  string `json:"sub"`
	Typ  string `json:"typ"`
	Next string `json:"next,omitempty"`
	Exp  int64  `json:"exp"`
}

func newID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func hashPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

func checkPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Server) sign(payload tokenPayload) string {
	body, _ := json.Marshal(payload)
	encoded := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) readSigned(token string) (tokenPayload, bool) {
	encoded, sig, ok := strings.Cut(token, ".")
	if !ok || encoded == "" || sig == "" {
		return tokenPayload{}, false
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(encoded))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return tokenPayload{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return tokenPayload{}, false
	}
	var payload tokenPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Exp < time.Now().UnixMilli() {
		return tokenPayload{}, false
	}
	return payload, true
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) issueToken(sub, typ string, minutes int) (string, string) {
	token := s.sign(tokenPayload{
		Sub: sub,
		Typ: typ,
		Exp: time.Now().Add(time.Duration(minutes) * time.Minute).UnixMilli(),
	})
	return token, tokenHash(token)
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
	if s.isProd {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

func (s *Server) setSession(w http.ResponseWriter, userID string) {
	token := s.sign(tokenPayload{
		Sub: userID,
		Typ: "session",
		Exp: time.Now().Add(7 * 24 * time.Hour).UnixMilli(),
	})
	s.setCookie(w, "ctp_session", token, 7*24*60*60)
}

func (s *Server) clearSession(w http.ResponseWriter) {
	s.setCookie(w, "ctp_session", "", -1)
}

func (s *Server) sessionUserID(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("ctp_session")
	if err != nil {
		return "", false
	}
	payload, ok := s.readSigned(cookie.Value)
	if !ok || payload.Typ != "session" || payload.Sub == "" {
		return "", false
	}
	return payload.Sub, true
}

func (s *Server) allow(key string, limit int, window time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	prev := s.attempts[key]
	recent := make([]time.Time, 0, len(prev)+1)
	for _, at := range prev {
		if now.Sub(at) < window {
			recent = append(recent, at)
		}
	}
	if len(recent) >= limit {
		s.attempts[key] = recent
		return false
	}
	s.attempts[key] = append(recent, now)
	return true
}

func clientIP(r *http.Request) string {
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	if host == "" {
		return "local"
	}
	return host
}

func safeNext(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return "/account"
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(r *http.Request, dest any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	return nil
}
