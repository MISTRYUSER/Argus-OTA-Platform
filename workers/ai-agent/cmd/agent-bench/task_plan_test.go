package main

import "testing"

func TestTaskPlanSelectsOnlyDeclaredFailedTrials(t *testing.T) {
	cases := []inputCase{{ID: "1"}, {ID: "2"}}
	selected, err := parseTaskPlan([]byte(`{"tasks":[{"scenario_id":"1","trial":2},{"scenario_id":"2","trial":0}]}`), cases, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || !selected[taskKey{"1", 2}] || selected[taskKey{"1", 0}] {
		t.Fatal("retry schedule changed original trial identities")
	}
}
func TestTaskPlanRejectsUnknownDuplicateAndOutOfRangeTrials(t *testing.T) {
	for _, body := range []string{
		`{"tasks":[]}`,
		`{"tasks":[{"scenario_id":"1"}]}`,
		`{"tasks":[{"scenario_id":"1","trial":null}]}`,
		`{"tasks":[{"scenario_id":"unknown","trial":0}]}`,
		`{"tasks":[{"scenario_id":"1","trial":3}]}`,
		`{"tasks":[{"scenario_id":"1","trial":-1}]}`,
		`{"tasks":[{"scenario_id":"1","trial":0},{"scenario_id":"1","trial":0}]}`,
	} {
		if _, err := parseTaskPlan([]byte(body), []inputCase{{ID: "1"}}, 3); err == nil {
			t.Fatalf("accepted invalid schedule: %s", body)
		}
	}
}
