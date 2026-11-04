package main

import (
	"path/filepath"
	"testing"
)

func TestIsAdminEmail(t *testing.T) {
	t.Setenv("ADMIN_EMAILS", "info@in2ai.biz, us-info@in2ai.biz")
	if !isAdminEmail("INFO@in2ai.biz") || !isAdminEmail("us-info@in2ai.biz") {
		t.Fatal("company inboxes should be admins")
	}
	if isAdminEmail("person@example.com") || isAdminEmail("") {
		t.Fatal("other addresses should not be admins")
	}
}

func TestResumeFilePath(t *testing.T) {
	dir := t.TempDir()
	path, ok := resumeFilePath(dir, "../secret.pdf")
	if !ok || filepath.Base(path) != "secret.pdf" {
		t.Fatalf("path=%s ok=%v", path, ok)
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel != "secret.pdf" {
		t.Fatalf("rel=%s err=%v", rel, err)
	}
	if _, ok := resumeFilePath(dir, ""); ok {
		t.Fatal("empty path should be rejected")
	}
	if _, ok := resumeFilePath(dir, "."); ok {
		t.Fatal("dot should be rejected")
	}
}

func TestSafeDownloadName(t *testing.T) {
	if got := safeDownloadName(`..\secret.pdf`); got != "secret.pdf" {
		t.Fatalf("name = %s", got)
	}
	if got := safeDownloadName(`resume "final".pdf`); got != "resume final.pdf" {
		t.Fatalf("name = %s", got)
	}
	if got := safeDownloadName(""); got != "resume" {
		t.Fatalf("name = %s", got)
	}
}
