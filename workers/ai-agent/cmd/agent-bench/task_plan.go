package main

import (
	"encoding/json"
	"fmt"
)

type taskKey struct {
	ScenarioID string `json:"scenario_id"`
	Trial      int    `json:"trial"`
}

// A replay is an explicit schedule, not a new sample or a changed trial ID.
func parseTaskPlan(data []byte, cases []inputCase, trials int) (map[taskKey]bool, error) {
	var plan struct {
		Tasks []struct {
			ScenarioID string `json:"scenario_id"`
			Trial      *int   `json:"trial"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if len(plan.Tasks) == 0 {
		return nil, fmt.Errorf("empty retry task plan")
	}
	known := map[string]bool{}
	for _, c := range cases {
		known[c.ID] = true
	}
	selected := map[taskKey]bool{}
	for _, requested := range plan.Tasks {
		if requested.Trial == nil {
			return nil, fmt.Errorf("retry trial must be explicitly specified")
		}
		task := taskKey{requested.ScenarioID, *requested.Trial}
		if !known[task.ScenarioID] || task.Trial < 0 || task.Trial >= trials {
			return nil, fmt.Errorf("unknown case or out-of-range retry trial")
		}
		if selected[task] {
			return nil, fmt.Errorf("duplicate retry trial")
		}
		selected[task] = true
	}
	return selected, nil
}
