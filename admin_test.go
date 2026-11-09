package main

import (
	"path/filepath"
	"testing"
	"time"
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

func TestValidateApplicationReview(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	ok, message := validateApplicationReview(applicationReview{
		ViewedBy: "  Ada Admin  ",
		CallType: "WhatsApp",
		Date:     today,
		Status:   "Approved",
	})
	if message != "" || ok.ViewedBy != "Ada Admin" || ok.CallType != "WhatsApp" || ok.Date != today || ok.Status != "Approved" {
		t.Fatalf("review = %#v message = %s", ok, message)
	}
	if _, message := validateApplicationReview(applicationReview{ViewedBy: "   ", CallType: "Phone", Date: today, Status: "Waiting"}); message == "" {
		t.Fatal("blank viewer name should be rejected")
	}
	if _, message := validateApplicationReview(applicationReview{ViewedBy: "Ada Admin", CallType: "Email", Date: today, Status: "Rejected"}); message == "" {
		t.Fatal("unknown call type should be rejected")
	}
	if _, message := validateApplicationReview(applicationReview{ViewedBy: "Ada Admin", CallType: "Phone", Date: today, Status: "Maybe"}); message == "" {
		t.Fatal("unknown status should be rejected")
	}
	if _, message := validateApplicationReview(applicationReview{ViewedBy: "Ada Admin", CallType: "Phone", Date: "2019-01-01", Status: "Waiting"}); message == "" {
		t.Fatal("review date before 2020 should be rejected")
	}
	if _, message := validateApplicationReview(applicationReview{ViewedBy: "Ada Admin", CallType: "Phone", Date: "", Status: "Waiting"}); message == "" {
		t.Fatal("missing review date should be rejected")
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
