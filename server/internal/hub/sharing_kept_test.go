package hub

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
)

func TestShareAuthorityHandoverIncludesOnlyTrustedKeptMachines(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	h := newTestHub(t)
	h.store = store
	if err := h.initHubID(ctx); err != nil {
		t.Fatal("hub identity unavailable")
	}
	const foreign = "synthetic-other-keeper"
	for _, manager := range []string{devicesOwner, foreign} {
		if err := store.SaveBusIdentity(ctx, db.BusIdentity{ID: manager, Role: "manager", Name: "Synthetic manager"}, "synthetic-keeper-hash-"+manager); err != nil {
			t.Fatal("manager registration failed")
		}
	}
	const ownMachine, foreignMachine, reportedOnly = "synthetic-trusted-kept", "synthetic-foreign-kept", "synthetic-report-only"
	h.cfg.Machines = nil
	h.devices.kept = map[string]string{ownMachine: devicesOwner, foreignMachine: foreign}
	claim, _ := json.Marshal(MachineConfig{ID: reportedOnly, Kind: "local"})
	h.devices.reported = reportedMachines{devicesOwner: map[string]reportedEntry{reportedOnly: {Cfg: claim}}}
	cfg := h.devicesConfigFor(devicesOwner)
	a, ok := cfg.ShareAuthorities[ownMachine]
	if !ok {
		t.Fatal("trusted kept machine absent from sharing-authority handover")
	}
	if _, ok := cfg.ShareAuthorities[foreignMachine]; ok {
		t.Fatal("foreign keeper received authority")
	}
	if _, ok := cfg.ShareAuthorities[reportedOnly]; ok {
		t.Fatal("manager report established authority")
	}
	if len(cfg.Machines) != 0 {
		t.Fatal("authority repair changed actual machine-list handover")
	}
	route, err := store.ShareRouting(ctx, h.hubID(), ownMachine, devicesOwner)
	if err != nil || route.Authority != a {
		t.Fatal("handover and live request authorities differ")
	}
	delete(h.devices.kept, ownMachine)
	if _, ok := h.devicesConfigFor(devicesOwner).ShareAuthorities[ownMachine]; ok {
		t.Fatal("removed keeper retained authority")
	}
}
