package main

import "testing"

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
	var stripe, javaRole, bidder, support bool
	for _, job := range jobs {
		id, _ := job["id"].(string)
		if id == "" {
			t.Fatal("job is missing an id")
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate job id %s", id)
		}
		seen[id] = struct{}{}
		if _, ok := job["matchScore"]; ok {
			t.Fatalf("%s still has matchScore", id)
		}
		role, _ := job["role"].(string)
		roles[role] = true
		switch id {
		case "6ab5df8fc6fe0dec811a3959":
			stripe = job["company"] == "Stripe"
		case "6aba6188ee0b348be72994e1":
			javaRole = job["title"] == "Senior Java Full Stack Developer"
		case "in2ai-ai-training-support":
			listed, _ := job["roles"].([]any)
			support = job["role"] == "consulting" && len(listed) == 2 && listed[0] == "consulting" && listed[1] == "ai-training"
		case "in2ai-senior-job-bidder":
			tiers, _ := job["compensationTiers"].([]any)
			bidder = job["employmentType"] == "Part-time / Contract" && job["isInternal"] == true && len(tiers) == 4
		}
	}

	for _, role := range []string{"consulting", "bidder", "full-stack", "backend", "ai-training", "devops"} {
		if !roles[role] {
			t.Fatalf("missing role %s", role)
		}
	}
	if !stripe || !javaRole || !bidder || !support {
		t.Fatal("expected partner and internal listings were not in the catalog")
	}
}
