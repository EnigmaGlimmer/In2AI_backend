package main

import (
	"regexp"
	"testing"
)

var opaqueJobID = regexp.MustCompile(`^[0-9a-f]{24}$`)

func TestJobCatalog(t *testing.T) {
	jobs, err := loadJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected job listings")
	}

	seen := map[string]struct{}{}
	roles := map[string]bool{}
	var bidder, support bool
	for _, job := range jobs {
		id, _ := job["id"].(string)
		if !opaqueJobID.MatchString(id) {
			t.Fatalf("job id %q should be an opaque id", id)
		}
		if apply, _ := job["applyUrl"].(string); apply != "/careers/"+id {
			t.Fatalf("apply url = %s", apply)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate job id %s", id)
		}
		seen[id] = struct{}{}
		if company, _ := job["company"].(string); company != "In2AI" {
			t.Fatalf("%s is not an In2AI listing", id)
		}
		if _, ok := job["matchScore"]; ok {
			t.Fatalf("%s still has matchScore", id)
		}
		role, _ := job["role"].(string)
		roles[role] = true
		title, _ := job["title"].(string)
		switch title {
		case "AI Training Support Specialist":
			listed, _ := job["roles"].([]any)
			support = job["role"] == "consulting" && len(listed) == 2 && listed[0] == "consulting" && listed[1] == "ai-training"
		case "Senior Job Bidder — Part-Time Contract":
			tiers, _ := job["compensationTiers"].([]any)
			bidder = job["employmentType"] == "Part-time / Contract" && job["isInternal"] == true && len(tiers) == 4
		}
	}

	for _, role := range []string{"consulting", "bidder", "ai-training"} {
		if !roles[role] {
			t.Fatalf("missing role %s", role)
		}
	}
	if len(jobs) != 6 || !bidder || !support {
		t.Fatal("expected In2AI listings were not in the catalog")
	}
}
