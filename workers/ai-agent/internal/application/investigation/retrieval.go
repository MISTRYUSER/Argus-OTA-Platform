package investigation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// derive the search budget from durable observations, so restart cannot reset it.
func allowHistorySearch(s *agentState, args string) error {
	key := func(raw string) (string, error) {
		var q queryInput
		if err := json.Unmarshal([]byte(raw), &q); err != nil {
			return "", err
		}
		if len([]rune(q.Query)) > 512 || len(q.ErrorCodes) > 20 {
			return "", fmt.Errorf("history query exceeds bounds")
		}
		sort.Strings(q.ErrorCodes)
		q.Query = strings.ToLower(strings.Join(strings.Fields(q.Query), " "))
		b, _ := json.Marshal(q)
		return string(b), nil
	}
	candidate, err := key(args)
	if err != nil {
		return err
	}
	count := 0
	for _, o := range s.observations {
		if o.Tool != "search_history" {
			continue
		}
		count++
		old, _ := key(string(o.Arguments))
		if old == candidate {
			return fmt.Errorf("duplicate history query; use new evidence to refine query")
		}
	}
	if count >= 3 {
		return fmt.Errorf("history search budget exhausted; report evidence limitations")
	}
	return nil
}
