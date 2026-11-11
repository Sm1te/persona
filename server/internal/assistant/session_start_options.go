package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
)

// Launcher start options belong to the manager: the hub relays the request,
// and the manager checks the machine's trust and hub-configured launcher before
// asking its node. Client query parameters cannot vouch for a launcher.
func (s *Service) workSessionStartOptions(w http.ResponseWriter, r *http.Request) {
	mid := r.PathValue("mid")
	var listed struct {
		Devices  []Device `json:"devices"`
		Complete bool     `json:"complete"`
		Others   []string `json:"others"`
	}
	if err := s.call(r.Context(), DeviceAll, DeviceDevicesList, map[string]any{}, &listed); err != nil {
		apierr.Call(w, err)
		return
	}
	if !listed.Complete || slices.Contains(listed.Others, mid) {
		apierr.WriteCode(w, http.StatusConflict, "machine_pending", errors.New("Confirm this computer before starting a session."))
		return
	}
	if !slices.ContainsFunc(listed.Devices, func(d Device) bool { return d.ID == mid }) {
		apierr.Write(w, http.StatusNotFound, errUnknownMachine)
		return
	}
	name := r.PathValue("name")
	params := map[string]any{"name": name}
	if l, configured := s.configuredLauncher(r.Context(), mid, name); configured {
		params["configured"] = true
		params["configured_agent"] = l.AgentType
	}
	ctx, cancel := context.WithTimeout(r.Context(), 85*time.Second)
	defer cancel()
	var opts json.RawMessage
	if err := s.call(ctx, mid, "session.start.options", params, &opts); err != nil {
		apierr.Call(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(opts)
}

func (s *Service) workSessionStartRoutes() {
	s.handle("GET /api/v1/machines/{mid}/launchers/{name}/start-options", s.workSessionStartOptions)
}
