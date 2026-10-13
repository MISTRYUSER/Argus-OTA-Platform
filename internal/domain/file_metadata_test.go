package domain

import (
	"encoding/json"
	"testing"
)

func TestRecordMetadataPerFileValidation(t *testing.T) {
	valid := json.RawMessage(`{"record_format":"apollo_cyber","version":"v1","vehicle_id":"V1","vehicle_class":"truck","replay_id":"R1","workload":"AD"}`)
	if e := ValidateFileMetadata(valid); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`[]`, `{"record_format":"bad"}`, `{"record_format":"apollo_cyber","version":"v1"}`, `{"record_format":"apollo_cyber","version":5}`, `null`} {
		if e := ValidateFileMetadata([]byte(raw)); e == nil {
			t.Fatalf("accepted invalid %s", raw)
		}
	}
	if e := ValidateFileMetadata(nil); e != nil {
		t.Fatal(e)
	}
}
