package main

import (
	"strings"
	"testing"
)

func TestSignedTokenRoundTrip(t *testing.T) {
	server := &Server{secret: []byte("test-secret")}
	token, hash := server.issueToken("user-1", "verify", 30)
	payload, ok := server.readSigned(token)
	if !ok || payload.Sub != "user-1" || payload.Typ != "verify" {
		t.Fatalf("token did not verify: %+v ok=%v", payload, ok)
	}
	if tokenHash(token) != hash {
		t.Fatal("token hash mismatch")
	}
	if _, ok := server.readSigned(token + "x"); ok {
		t.Fatal("tampered token was accepted")
	}
}

func TestPreferIPv4(t *testing.T) {
	got := preferIPv4("postgres://Enigma@localhost:5432/in2ai?sslmode=disable")
	if got != "postgres://Enigma@127.0.0.1:5432/in2ai?sslmode=disable" {
		t.Fatalf("localhost was not rewritten: %s", got)
	}
}

func TestAbsoluteOrigin(t *testing.T) {
	if got := absoluteOrigin("localhost"); got != "http://127.0.0.1:3000" {
		t.Fatalf("bare host should fall back, got %s", got)
	}
	if got := absoluteOrigin("http://127.0.0.1:3000/"); got != "http://127.0.0.1:3000" {
		t.Fatalf("absolute origin was rewritten: %s", got)
	}
	if got := absoluteRedirect("localhost/api/auth/google/callback", "http://127.0.0.1:3000"); got != "http://127.0.0.1:3000/api/auth/google/callback" {
		t.Fatalf("relative redirect was not rewritten: %s", got)
	}
}

func TestVerificationEmail(t *testing.T) {
	subject, textBody, htmlBody := verificationEmail("Daniel Goossens", "https://in2ai.biz/verify-email?token=abc")
	if subject != "Verify your email for In2AI" {
		t.Fatalf("subject: %s", subject)
	}
	for _, part := range []string{
		"Hello Daniel Goossens,",
		"Please verify your email address using the link below to continue with In2AI.",
		"Verify Your Email",
		"https://in2ai.biz/verify-email?token=abc",
		"This link expires in 30 minutes.",
		"In2AI Support Team",
		"info@in2ai.biz",
	} {
		if !strings.Contains(textBody, part) {
			t.Fatalf("plain text missing %q\n%s", part, textBody)
		}
	}
	if !strings.Contains(htmlBody, `href="https://in2ai.biz/verify-email?token=abc"`) {
		t.Fatalf("html link missing: %s", htmlBody)
	}
}

func TestSafeNext(t *testing.T) {
	if safeNext("/careers/role") != "/careers/role" {
		t.Fatal("relative path should pass")
	}
	if safeNext("https://evil.example") != "/account" || safeNext("//evil.example") != "/account" {
		t.Fatal("external path should fall back")
	}
}
