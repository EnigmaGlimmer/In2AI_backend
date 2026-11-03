package main

import "testing"

func TestIsAdminEmail(t *testing.T) {
	t.Setenv("ADMIN_EMAILS", "info@in2ai.biz, us-info@in2ai.biz")
	if !isAdminEmail("INFO@in2ai.biz") || !isAdminEmail("us-info@in2ai.biz") {
		t.Fatal("company inboxes should be admins")
	}
	if isAdminEmail("person@example.com") || isAdminEmail("") {
		t.Fatal("other addresses should not be admins")
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
