package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID                string
	Name              string
	Email             string
	PasswordHash      string
	EmailVerified     bool
	GoogleSub         string
	VerifyHash        string
	ResetHash         string
	Headline          string
	Location          string
	Phone             string
	Bio               string
	Degree            string
	WorkAuthorization string
	EnglishFluent     string
	Skills            string
	IsAdmin           bool
	CreatedAt         time.Time
}

type Application struct {
	ID         string
	UserID     string
	JobID      string
	Seat       string
	Note       string
	ResumeName string
	CreatedAt  time.Time
}

func openDB(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			email_verified BOOLEAN NOT NULL DEFAULT FALSE,
			google_sub TEXT UNIQUE,
			verify_hash TEXT NOT NULL DEFAULT '',
			reset_hash TEXT NOT NULL DEFAULT '',
			headline TEXT NOT NULL DEFAULT '',
			location TEXT NOT NULL DEFAULT '',
			phone TEXT NOT NULL DEFAULT '',
			bio TEXT NOT NULL DEFAULT '',
			degree TEXT NOT NULL DEFAULT '',
			work_authorization TEXT NOT NULL DEFAULT '',
			english_fluent TEXT NOT NULL DEFAULT '',
			skills TEXT NOT NULL DEFAULT '',
			is_admin BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			payload JSONB NOT NULL,
			published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS applications (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			job_id TEXT NOT NULL DEFAULT '',
			seat TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			resume_name TEXT NOT NULL DEFAULT '',
			resume_path TEXT NOT NULL DEFAULT '',
			location TEXT NOT NULL DEFAULT '',
			degree TEXT NOT NULL DEFAULT '',
			work_authorization TEXT NOT NULL DEFAULT '',
			english_fluent TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func scanUser(row pgx.Row) (User, error) {
	var user User
	var googleSub *string
	err := row.Scan(
		&user.ID, &user.Name, &user.Email, &user.PasswordHash, &user.EmailVerified, &googleSub,
		&user.VerifyHash, &user.ResetHash, &user.Headline, &user.Location, &user.Phone, &user.Bio,
		&user.Degree, &user.WorkAuthorization, &user.EnglishFluent, &user.Skills, &user.IsAdmin, &user.CreatedAt,
	)
	if googleSub != nil {
		user.GoogleSub = *googleSub
	}
	return user, err
}

const userColumns = `id, name, email, password_hash, email_verified, google_sub, verify_hash, reset_hash,
	headline, location, phone, bio, degree, work_authorization, english_fluent, skills, is_admin, created_at`

func (s *Server) userByID(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1`, id))
}

func (s *Server) userByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email=$1`, email))
}

func (s *Server) userByGoogleSub(ctx context.Context, sub string) (User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE google_sub=$1`, sub))
}

func (s *Server) insertUser(ctx context.Context, user User) error {
	var googleSub any
	if user.GoogleSub != "" {
		googleSub = user.GoogleSub
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO users (
			id, name, email, password_hash, email_verified, google_sub, verify_hash, reset_hash, is_admin
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, user.ID, user.Name, user.Email, user.PasswordHash, user.EmailVerified, googleSub, user.VerifyHash, user.ResetHash, user.IsAdmin)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Server) publicUser(user User) map[string]any {
	provider := "password"
	if user.GoogleSub != "" {
		provider = "google"
	}
	return map[string]any{
		"id":                user.ID,
		"name":              user.Name,
		"email":             user.Email,
		"emailVerified":     user.EmailVerified,
		"headline":          user.Headline,
		"location":          user.Location,
		"phone":             user.Phone,
		"bio":               user.Bio,
		"degree":            user.Degree,
		"workAuthorization": user.WorkAuthorization,
		"englishFluent":     user.EnglishFluent,
		"skills":            user.Skills,
		"provider":          provider,
		"isAdmin":           user.IsAdmin,
	}
}
