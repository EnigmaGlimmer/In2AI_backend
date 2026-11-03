package main

import "testing"

func TestValidateGuestApplication(t *testing.T) {
	valid := guestApplication{
		FirstName:         "Ada",
		LastName:          "Lovelace",
		Email:             "Ada@Example.com",
		Phone:             "+1 (415) 555-0100",
		WhatsApp:          "",
		LinkedIn:          "linkedin.com/in/ada",
		Location:          "United States",
		DateOfBirth:       "1990-12-10",
		WorkAuthorization: "Yes",
		Gender:            "Female",
	}
	got, message := validateGuestApplication(valid)
	if message != "" {
		t.Fatal(message)
	}
	if got.Email != "ada@example.com" {
		t.Fatalf("email = %s", got.Email)
	}
	if got.LinkedIn != "https://linkedin.com/in/ada" {
		t.Fatalf("linkedin = %s", got.LinkedIn)
	}

	valid.WorkAuthorization = "Maybe"
	if _, message = validateGuestApplication(valid); message == "" {
		t.Fatal("expected work authorization to be required")
	}
	valid.WorkAuthorization = "No"
	valid.Gender = "Other"
	if _, message = validateGuestApplication(valid); message == "" {
		t.Fatal("expected a known gender option")
	}
	valid.Gender = "Decline to self-identify"
	valid.Phone = "123"
	if _, message = validateGuestApplication(valid); message == "" {
		t.Fatal("expected a real phone number")
	}
}

func TestNormalizeLinkedIn(t *testing.T) {
	if got, ok := normalizeLinkedIn(""); !ok || got != "" {
		t.Fatalf("blank linkedin = %q %v", got, ok)
	}
	if _, ok := normalizeLinkedIn("https://example.com/in/ada"); ok {
		t.Fatal("expected non-linkedin hosts to fail")
	}
}
