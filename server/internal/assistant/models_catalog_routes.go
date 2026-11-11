package assistant

import (
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// POST /api/v1/launchers/catalog (#4975): the built-in catalog's metadata for model ids typed by hand, so a provider
// form can show a name, a context window and thinking levels without fetching the gateway's model list. It is the
// lookup discovery fills its models with (enrichModel); nothing is fetched, stored or keyed.

const (
	catalogLookupMaxIDs   = 100
	catalogLookupMaxIDLen = 200 // bytes, after trimming
)

func (s *Service) providerCatalogRoutes() {
	s.handle("POST /api/v1/launchers/catalog", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := decodeBody(w, r, &in, 1<<20); err != nil {
			writeBodyErr(w, err, i18n.Err("assistant-models_catalog_routes.err_bad_request"))
			return
		}
		models, err := catalogLookup(in.IDs)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		apierr.WriteJSON(w, struct {
			Models []proto.ProviderModel `json:"models"`
		}{models})
	})
}

// catalogLookup answers each trimmed, nonempty id once, in request order, with what enrichModel fills in for it; an id
// it fills nothing for is left out. The answer is never nil.
func catalogLookup(ids []string) ([]proto.ProviderModel, error) {
	if len(ids) > catalogLookupMaxIDs {
		return nil, i18n.Err("assistant-models_catalog_routes.err_too_many", i18n.Args{"max": catalogLookupMaxIDs})
	}
	out := []proto.ProviderModel{}
	seen := map[string]bool{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if len(id) > catalogLookupMaxIDLen {
			return nil, i18n.Err("assistant-models_catalog_routes.err_id_too_long", i18n.Args{"max": catalogLookupMaxIDLen})
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		m := enrichModel(proto.ProviderModel{ID: id})
		if m.Name == "" && m.ContextWindow == 0 && len(m.Efforts) == 0 && m.DefaultEffort == "" {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}
