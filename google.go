package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type googleProfile struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func (s *Server) googleConfigured() bool {
	return s.googleClientID != "" && s.googleClientSecret != "" && s.redirectURL != ""
}

func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	if !s.googleConfigured() {
		http.Redirect(w, r, s.appOrigin+"/login?error=google_not_configured", http.StatusFound)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	state := s.sign(tokenPayload{
		Typ:  "oauth",
		Next: next,
		Exp:  time.Now().Add(10 * time.Minute).UnixMilli(),
		Sub:  newID(),
	})
	s.setCookie(w, "ctp_oauth", state, 10*60)
	query := url.Values{}
	query.Set("client_id", s.googleClientID)
	query.Set("redirect_uri", s.redirectURL)
	query.Set("response_type", "code")
	query.Set("scope", "openid email profile")
	query.Set("state", state)
	query.Set("prompt", "select_account")
	http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth?"+query.Encode(), http.StatusFound)
}

func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(code string) {
		http.Redirect(w, r, s.appOrigin+"/login?error="+code, http.StatusFound)
	}
	if !s.googleConfigured() {
		fail("google_not_configured")
		return
	}
	if r.URL.Query().Get("error") != "" {
		fail("google_denied")
		return
	}
	cookie, err := r.Cookie("ctp_oauth")
	state := r.URL.Query().Get("state")
	s.setCookie(w, "ctp_oauth", "", -1)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		fail("google_failed")
		return
	}
	payload, ok := s.readSigned(state)
	if !ok || payload.Typ != "oauth" {
		fail("google_failed")
		return
	}
	profile, err := s.googleProfile(r.Context(), r.URL.Query().Get("code"))
	if err != nil || profile.Sub == "" || profile.Email == "" || !profile.EmailVerified {
		if err != nil && errors.Is(err, errGoogleUnverified) {
			fail("google_unverified")
			return
		}
		fail("google_failed")
		return
	}
	user, err := s.upsertGoogleUser(r.Context(), profile)
	if err != nil {
		fail("google_failed")
		return
	}
	s.setSession(w, user.ID)
	http.Redirect(w, r, s.appOrigin+safeNext(payload.Next), http.StatusFound)
}

var errGoogleUnverified = errors.New("google email is not verified")

func (s *Server) googleProfile(ctx context.Context, code string) (googleProfile, error) {
	if code == "" {
		return googleProfile{}, errors.New("missing code")
	}
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", s.googleClientID)
	form.Set("client_secret", s.googleClientSecret)
	form.Set("redirect_uri", s.redirectURL)
	form.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return googleProfile{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return googleProfile{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return googleProfile{}, fmt.Errorf("token exchange failed")
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		return googleProfile{}, errors.New("missing access token")
	}
	infoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		return googleProfile{}, err
	}
	infoReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	infoResp, err := http.DefaultClient.Do(infoReq)
	if err != nil {
		return googleProfile{}, err
	}
	defer infoResp.Body.Close()
	infoBody, _ := io.ReadAll(io.LimitReader(infoResp.Body, 1<<20))
	if infoResp.StatusCode != http.StatusOK {
		return googleProfile{}, errors.New("userinfo failed")
	}
	var profile googleProfile
	if err := json.Unmarshal(infoBody, &profile); err != nil {
		return googleProfile{}, err
	}
	profile.Email = strings.ToLower(strings.TrimSpace(profile.Email))
	profile.Name = strings.TrimSpace(profile.Name)
	if !profile.EmailVerified {
		return googleProfile{}, errGoogleUnverified
	}
	if len(profile.Name) < 2 {
		profile.Name = profile.Email
	}
	return profile, nil
}

func (s *Server) upsertGoogleUser(ctx context.Context, profile googleProfile) (User, error) {
	if existing, err := s.userByGoogleSub(ctx, profile.Sub); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}

	existing, err := s.userByEmail(ctx, profile.Email)
	if err == nil {
		if existing.GoogleSub != "" && existing.GoogleSub != profile.Sub {
			return User{}, errors.New("email linked to another google account")
		}
		_, err = s.db.Exec(ctx, `UPDATE users SET google_sub=$1, email_verified=TRUE WHERE id=$2`, profile.Sub, existing.ID)
		if err != nil {
			return User{}, err
		}
		existing.GoogleSub = profile.Sub
		existing.EmailVerified = true
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}

	user := User{
		ID:            newID(),
		Name:          profile.Name,
		Email:         profile.Email,
		EmailVerified: true,
		GoogleSub:     profile.Sub,
	}
	if err := s.insertUser(ctx, user); err != nil {
		if isUniqueViolation(err) {
			if raced, findErr := s.userByGoogleSub(ctx, profile.Sub); findErr == nil {
				return raced, nil
			}
		}
		return User{}, err
	}
	return user, nil
}
