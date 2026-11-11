package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Secret providers (docs/extensions/secret-providers.md): the value of a reference (op://vault/item/field) comes from
// the extension whose secret_providers contribution claims its scheme, asked here and nowhere else. A value is never
// logged, put in an error, an audit detail or an event; only the caller that asked gets it.

var (
	// secretCallTimeout bounds one secrets.resolve or secrets.describe.
	secretCallTimeout = 10 * time.Second
	// secretResolvesMost resolutions per provider per secretResolveWindow; more are refused before reaching it.
	secretResolvesMost  = 60
	secretResolveWindow = time.Minute
	// secretDescribeKeep is how long a reference's words (item and field names) are kept.
	secretDescribeKeep = 10 * time.Minute
)

// SecretError is why a reference did not resolve. Code is for callers (no_provider, provider_off, not_found,
// not_signed_in, rate_limited, refused, failed); the words read in the asker's language.
type SecretError struct {
	Code string
	// RetryAfter is the provider's own wait for rate_limited, in seconds (0: unknown).
	RetryAfter float64
	err        error
}

func (e *SecretError) Error() string { return e.err.Error() }

func (e *SecretError) Unwrap() error { return e.err }

// Text is the error in l.
func (e *SecretError) Text(l i18n.Lang) string { return i18n.Localize(e.err, l) }

func secretErr(code string, args i18n.Args) *SecretError {
	return &SecretError{Code: code, err: i18n.Err("hub-extension_secrets."+code, args)}
}

// SecretDescription is a reference in words for people: whose it is and what it names, never a value.
type SecretDescription struct {
	Provider, ProviderName string
	Title, Detail          string
}

// secretWords keeps references' descriptions for a while: settings pages and cards show them often, and asking the
// provider each time costs its service a request.
type secretWords struct {
	mu   sync.Mutex
	kept map[string]keptWords
}

type keptWords struct {
	d  SecretDescription
	at time.Time
}

// ResolveSecret is reference's value for purpose, from the provider that claims its scheme. approvedBy is who approved
// this use: "settings" (a reference saved in Settings), "rule" (an Always rule) or "device:<id>" (the device that
// approved its hand-off card); it goes into the record of the use with the reference and the purpose. Errors are
// *SecretError.
func (h *Manager) ResolveSecret(ctx context.Context, reference string, purpose wire.SecretPurpose, approvedBy string) (string, error) {
	if h.extensions == nil || !h.ready(ctx) {
		return "", secretErr("provider_off", i18n.Args{"provider": schemeOf(reference)})
	}
	return h.extensions.resolveSecret(ctx, reference, purpose, approvedBy)
}

// DescribeSecret is reference in words (cards, settings), from its provider. Errors are *SecretError.
func (h *Manager) DescribeSecret(ctx context.Context, reference string) (SecretDescription, error) {
	if h.extensions == nil || !h.ready(ctx) {
		return SecretDescription{}, secretErr("provider_off", i18n.Args{"provider": schemeOf(reference)})
	}
	return h.extensions.describeSecret(ctx, reference)
}

// SecretProviderOf is the enabled provider of reference's scheme; false for no such provider or text that is not a
// reference.
func (h *Manager) SecretProviderOf(reference string) (extension.SecretProvider, bool) {
	if h.extensions == nil {
		return extension.SecretProvider{}, false
	}
	scheme, ok := extension.ReferenceScheme(reference)
	if !ok {
		return extension.SecretProvider{}, false
	}
	return h.extensions.reg.SecretProviderFor(scheme)
}

func schemeOf(reference string) string {
	if s, ok := extension.ReferenceScheme(reference); ok {
		return s + "://"
	}
	return "?"
}

// secretProvider is the provider of reference and its running program.
func (ps *extensions) secretProvider(reference string) (extension.SecretProvider, *hubExtension, error) {
	scheme, ok := extension.ReferenceScheme(reference)
	if !ok {
		return extension.SecretProvider{}, nil, secretErr("not_reference", nil)
	}
	prov, ok := ps.reg.SecretProviderFor(scheme)
	if !ok {
		return extension.SecretProvider{}, nil, secretErr("no_provider", i18n.Args{"scheme": scheme + "://"})
	}
	hp := ps.get(prov.Extension)
	if hp == nil || hp.proc == nil {
		return prov, nil, secretErr("provider_off", i18n.Args{"provider": prov.Name})
	}
	return prov, hp, nil
}

func (ps *extensions) resolveSecret(ctx context.Context, reference string, purpose wire.SecretPurpose, approvedBy string) (string, error) {
	prov, hp, err := ps.secretProvider(reference)
	if prov.Extension == "" {
		ps.kept.forgetReference(reference)
		return "", err // no provider to record it on
	}
	detail := map[string]any{"reference": reference, "purpose": purpose}
	fail := func(e *SecretError) (string, error) {
		ps.kept.forgetReference(reference) // a kept launcher key of it is not trusted any more (secret_kept.go)
		detail["code"] = e.Code
		ps.h.audit(ctx, prov.Extension, "secret_resolve_failed", approvedBy, detail)
		ps.logf("plugin %s: secrets.resolve for %s %s: %s", prov.Extension, purpose.Kind, purpose.ID, e.Code)
		return "", e
	}
	if err != nil {
		return fail(err.(*SecretError))
	}
	if !ps.resolves.allow(prov.Extension) {
		return fail(secretErr("rate_limited", i18n.Args{"provider": prov.Name}))
	}
	cctx, cancel := context.WithTimeout(ctx, secretCallTimeout)
	defer cancel()
	var res wire.SecretsResolveResult
	err = hp.proc.Call(cctx, "secrets.resolve",
		wire.SecretsResolveParams{Contribution: prov.Contribution, Reference: reference, Purpose: purpose}, &res)
	switch {
	case err != nil:
		return fail(providerError(prov, err, cctx.Err() != nil && ctx.Err() == nil))
	case res.Value == "":
		return fail(secretErr("failed", i18n.Args{"provider": prov.Name, "why": "no value"}))
	case len(res.Value) > extension.SecretMost:
		return fail(secretErr("too_long", i18n.Args{"provider": prov.Name}))
	}
	ps.h.audit(ctx, prov.Extension, "secret_resolved", approvedBy, detail)
	ps.logf("plugin %s: secrets.resolve for %s %s", prov.Extension, purpose.Kind, purpose.ID)
	// The record for its own page; a provider that does not keep one ignores it.
	_ = hp.proc.Notify("secrets.used", wire.SecretsUsedEvent{Contribution: prov.Contribution, Reference: reference,
		Purpose: purpose, At: time.Now().UTC(), ApprovedBy: approvedBy})
	return res.Value, nil
}

func (ps *extensions) describeSecret(ctx context.Context, reference string) (SecretDescription, error) {
	prov, hp, err := ps.secretProvider(reference)
	if err != nil {
		return SecretDescription{}, err
	}
	ps.words.mu.Lock()
	k, ok := ps.words.kept[reference]
	ps.words.mu.Unlock()
	if ok && time.Since(k.at) < secretDescribeKeep && k.d.Provider == prov.Extension {
		return k.d, nil
	}
	cctx, cancel := context.WithTimeout(ctx, secretCallTimeout)
	defer cancel()
	var res wire.SecretsDescribeResult
	if err := hp.proc.Call(cctx, "secrets.describe", wire.SecretsDescribeParams{Contribution: prov.Contribution, Reference: reference}, &res); err != nil {
		return SecretDescription{}, providerError(prov, err, cctx.Err() != nil && ctx.Err() == nil)
	}
	d := SecretDescription{Provider: prov.Extension, ProviderName: prov.Name, Title: providerWords(res.Title, 200), Detail: providerWords(res.Detail, 200)}
	if d.Title == "" {
		d.Title = reference
	}
	ps.words.mu.Lock()
	if ps.words.kept == nil {
		ps.words.kept = map[string]keptWords{}
	}
	ps.words.kept[reference] = keptWords{d: d, at: time.Now()}
	ps.words.mu.Unlock()
	return d, nil
}

// forgetSecretWords drops the kept descriptions of provider's references (it was switched off, upgraded or removed).
func (ps *extensions) forgetSecretWords(provider string) {
	ps.words.mu.Lock()
	defer ps.words.mu.Unlock()
	for ref, k := range ps.words.kept {
		if k.d.Provider == provider {
			delete(ps.words.kept, ref)
		}
	}
}

// providerError is a provider's failure as a SecretError: its code when it is one the protocol names, its words (one
// line, clipped) otherwise. timedOut: the call ran out of secretCallTimeout.
func providerError(prov extension.SecretProvider, err error, timedOut bool) *SecretError {
	args := i18n.Args{"provider": prov.Name}
	if timedOut {
		args["why"] = "no answer in " + secretCallTimeout.String()
		return secretErr("failed", args)
	}
	var re *rpc.Error
	if !errors.As(err, &re) {
		args["why"] = providerWords(err.Error(), 200)
		return secretErr("failed", args)
	}
	switch re.Code {
	case "not_found", "not_signed_in":
		return secretErr(re.Code, args)
	case "rate_limited":
		e := secretErr("rate_limited", args)
		var d struct {
			RetryAfter float64 `json:"retry_after"`
		}
		if json.Unmarshal(re.Data, &d) == nil && d.RetryAfter > 0 {
			e.RetryAfter = d.RetryAfter
		}
		return e
	case "refused":
		args["why"] = providerWords(re.Message, 200)
		return secretErr("refused", args)
	case extension.CodeUnknownMethod, extension.CodeNoContribution:
		args["why"] = "it does not resolve references (an older version?)"
		return secretErr("failed", args)
	}
	args["why"] = providerWords(re.Message, 200)
	return secretErr("failed", args)
}

func providerWords(s string, most int) string {
	return clip(strings.Join(strings.Fields(s), " "), most)
}
