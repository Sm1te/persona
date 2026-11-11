package extmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Always rules for hand-offs (docs/extensions/secret-providers.md "Always rules"): "Always for this launcher on this
// computer" on a hand-off card keeps one rule per reference × launcher × computer, and a later hand-off that every rule
// covers asks nothing (its uses are recorded with approved_by "rule"). A rule holds for the provider that claimed the
// reference's scheme when it was made, and only while that install lasts: it goes with the provider's uninstall, and a
// rule older than the provider's install (a removal that failed, an id taken again) covers nothing. A provider whose id
// changes takes its rules along. The provider's details list its rules,
// and each can be removed there. They are the manager's setting handoffRulesKey, written whole (as the chat rules are,
// chat_rules_store.go): no table, nothing in manager.json.
const (
	handoffRulesKey = "secret_handoff_rules"
	// handoffRulesMost is the most rules kept; one more is refused, and its hand-off goes ahead once (the card asked).
	handoffRulesMost = 200
)

// HandoffRule is one Always rule: the reference, the launcher and the computer it covers, the provider it holds for,
// who made it and when.
type HandoffRule struct {
	Reference string    `json:"reference"`
	Launcher  string    `json:"launcher"`
	Machine   string    `json:"machine"`
	Provider  string    `json:"provider"`
	By        string    `json:"by"`
	At        time.Time `json:"at"`
}

// ID names the rule: the same reference, launcher and computer make the same rule.
func (r HandoffRule) ID() string {
	sum := sha256.Sum256([]byte(r.Reference + "\x00" + r.Launcher + "\x00" + r.Machine))
	return hex.EncodeToString(sum[:8])
}

// HandoffRuleView is a rule as the provider's details show it.
type HandoffRuleView struct {
	ID        string    `json:"id"`
	Reference string    `json:"reference"`
	Launcher  string    `json:"launcher"`
	Machine   string    `json:"machine"`
	By        string    `json:"by,omitempty"`
	At        time.Time `json:"at"`
}

var errHandoffRulesFull = fmt.Errorf("there are %d hand-off rules already: remove one first", handoffRulesMost)

// handoffRules reads the rules kept: none for no database or a setting that is not a list (said in the log), an error
// for a database that cannot be read. Elements that are not rules are left out.
func (h *Manager) handoffRules(ctx context.Context) ([]HandoffRule, error) {
	if h.store == nil {
		return nil, errNoDatabase
	}
	var raw json.RawMessage
	if _, err := h.store.Setting(ctx, handoffRulesKey, &raw); err != nil {
		return nil, fmt.Errorf("read %s: %w", handoffRulesKey, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		h.logf("hand-off rules: %s is not a list (%v): taken as none, every hand-off asks", handoffRulesKey, err)
		return nil, nil
	}
	var list []HandoffRule
	for _, e := range elements {
		var r HandoffRule
		if json.Unmarshal(e, &r) != nil || r.Reference == "" || r.Launcher == "" || r.Machine == "" || r.Provider == "" {
			continue
		}
		list = append(list, r)
	}
	return list, nil
}

// HandoffRuleCovers reports whether an Always rule covers handing reference's value to launcher on machine: one made
// for this reference, launcher and computer, for the provider that claims the reference now, since its install.
func (h *Manager) HandoffRuleCovers(ctx context.Context, reference, launcher, machine string) bool {
	prov, ok := h.SecretProviderOf(reference)
	if !ok {
		return false
	}
	installed := h.providerInstalled(prov.Extension)
	h.handoffRulesMu.Lock()
	defer h.handoffRulesMu.Unlock()
	list, err := h.handoffRules(ctx)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(list, func(r HandoffRule) bool {
		return r.Reference == reference && r.Launcher == launcher && r.Machine == machine && r.Provider == prov.Extension &&
			!r.At.Before(installed)
	})
}

// providerInstalled is when extension id was installed (zero: not known).
func (h *Manager) providerInstalled(id string) time.Time {
	if h.extensions == nil || h.extensions.reg == nil {
		return time.Time{}
	}
	if p := h.extensions.reg.Snapshot(id); p != nil {
		return p.Record.InstalledAt
	}
	return time.Time{}
}

// AddHandoffRules keeps rules (a card's "Always", one per variable): each for the provider its card named, which must
// still claim its reference (else it is left out); one kept already is made again. Past handoffRulesMost the rest are
// refused (errHandoffRulesFull).
func (h *Manager) AddHandoffRules(ctx context.Context, rules []HandoffRule) error {
	h.handoffRulesMu.Lock()
	defer h.handoffRulesMu.Unlock()
	list, err := h.handoffRules(ctx) // a database that cannot be read must not be written over
	if err != nil {
		return err
	}
	for _, r := range rules {
		prov, ok := h.SecretProviderOf(r.Reference)
		if !ok || prov.Extension != r.Provider || r.Launcher == "" || r.Machine == "" {
			continue
		}
		id := r.ID()
		list = slices.DeleteFunc(list, func(o HandoffRule) bool { return o.ID() == id })
		if len(list) >= handoffRulesMost {
			return errHandoffRulesFull
		}
		list = append(list, r)
	}
	if list == nil {
		list = []HandoffRule{}
	}
	return h.store.PutSetting(ctx, handoffRulesKey, list)
}

// handoffRulesOf are provider's rules as its details show them, newest first.
func (h *Manager) handoffRulesOf(ctx context.Context, provider string) []HandoffRuleView {
	h.handoffRulesMu.Lock()
	list, _ := h.handoffRules(ctx)
	h.handoffRulesMu.Unlock()
	var out []HandoffRuleView
	for _, r := range list {
		if r.Provider == provider {
			out = append(out, HandoffRuleView{ID: r.ID(), Reference: r.Reference, Launcher: r.Launcher, Machine: r.Machine, By: r.By, At: r.At})
		}
	}
	slices.SortFunc(out, func(a, b HandoffRuleView) int { return b.At.Compare(a.At) })
	return out
}

// moveHandoffRules gives provider from's rules to to: its id changed, it is the same install.
func (h *Manager) moveHandoffRules(ctx context.Context, from, to string) error {
	h.handoffRulesMu.Lock()
	defer h.handoffRulesMu.Unlock()
	list, err := h.handoffRules(ctx)
	if errors.Is(err, errNoDatabase) {
		return nil
	}
	if err != nil {
		return err
	}
	moved := false
	for i := range list {
		if list[i].Provider == from {
			list[i].Provider, moved = to, true
		}
	}
	if !moved {
		return nil
	}
	return h.store.PutSetting(ctx, handoffRulesKey, list)
}

// removeHandoffRules drops provider's rules with id ("" for all of them, as its uninstall does), and says how many went.
func (h *Manager) removeHandoffRules(ctx context.Context, provider, id string) (int, error) {
	h.handoffRulesMu.Lock()
	defer h.handoffRulesMu.Unlock()
	list, err := h.handoffRules(ctx)
	if errors.Is(err, errNoDatabase) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	kept := slices.DeleteFunc(slices.Clone(list), func(r HandoffRule) bool {
		return r.Provider == provider && (id == "" || r.ID() == id)
	})
	if len(kept) == len(list) {
		return 0, nil
	}
	if kept == nil {
		kept = []HandoffRule{}
	}
	return len(list) - len(kept), h.store.PutSetting(ctx, handoffRulesKey, kept)
}
