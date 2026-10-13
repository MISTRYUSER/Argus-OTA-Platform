package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateFileMetadata accepts explicit per-file provenance, never inferring OTA
// versions from a recording container's serialization version.
func ValidateFileMetadata(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > 4096 {
		return fmt.Errorf("file metadata exceeds4096bytes")
	}
	var data map[string]string
	if e := json.Unmarshal(raw, &data); e != nil || data == nil {
		return fmt.Errorf("file metadata must be an object of strings")
	}
	if len(data) == 0 {
		return nil
	}
	if data["record_format"] != "apollo_cyber" {
		return fmt.Errorf("unsupported explicit record_format")
	}
	for _, key := range []string{"version", "vehicle_id", "vehicle_class", "replay_id", "workload"} {
		value := strings.TrimSpace(data[key])
		if value == "" || len(value) > 128 {
			return fmt.Errorf("missing or invalid metadata %s", key)
		}
	}
	return nil
}
