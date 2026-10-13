package telemetryquery

import (
	"context"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	pg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	"testing"
	"time"
)

func TestTrustedScopeMigrationAndImmutability(t *testing.T) {
	db := testutil.OpenDB(t)
	b, _ := core.NewBatch("scope-test", "SYNTHETIC", 1)
	b.ClearEvents()
	if e := pg.NewPostgresBatchRepository(db).Save(context.Background(), b); e != nil {
		t.Fatal(e)
	}
	start := time.Now().UTC().Add(-time.Minute)
	end := time.Now().UTC()
	if _, e := db.Exec(`INSERT INTO incident_telemetry_scopes(batch_id,namespace,service_name,started_at,ended_at) VALUES($1,'argus','argus-ai-agent',$2,$3)`, b.ID, start, end); e != nil {
		t.Fatal(e)
	}
	scope, e := NewPostgresScopeResolver(db)(context.Background(), b.ID.String())
	if e != nil || scope.Namespace != "argus" || scope.Service != "argus-ai-agent" {
		t.Fatalf("binding incorrect %+v %v", scope, e)
	}
	if _, e = db.Exec(`UPDATE incident_telemetry_scopes SET namespace='other' WHERE batch_id=$1`, b.ID); e == nil {
		t.Fatal("scope changed while investigation can resume")
	}
	if _, e = NewPostgresScopeResolver(db)(context.Background(), "bad-id"); e == nil {
		t.Fatal("invalid scope ID accepted")
	}
}
