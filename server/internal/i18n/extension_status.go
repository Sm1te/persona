package i18n

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// ExtensionStatusText renders only hub-owned extension status and validation text. Registry and host caches keep
// their canonical source text; extension-authored messages, manifest labels, logs and CLI output never call this.
func ExtensionStatusText(l Lang, text string) string { return extensionStatusText(l, text, 0) }

var extensionStatusTemplates = sync.OnceValue(func() []deploymentTemplate {
	return makeCatalogTemplates(func(key string) bool {
		return strings.HasPrefix(key, "extension-status.") && key != "extension-status.separator" ||
			strings.HasPrefix(key, "extension-version.") || strings.HasPrefix(key, "extension-identity.") || key == "hub-extensions.missing" ||
			key == "hub-extensions.socket_path_too_long" || key == "extension-market_git.git_missing" ||
			key == "extension-market_git.git_too_old" ||
			key == "extension-sandbox.probe_tmp"
	})
})

func extensionStatusText(l Lang, text string, depth int) string {
	if text == "" || depth >= 16 {
		return text
	}
	for _, form := range extensionStatusTemplates() {
		match := form.pattern.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		args := Args{}
		for i, name := range form.names {
			value := match[i+1]
			switch name {
			case "count":
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return text
				}
				args[name] = n
			case "err":
				if knownExtensionStatus(value, depth+1) {
					args[name] = extensionStatusText(l, value, depth+1)
				} else {
					args[name] = value
				}
			default:
				args[name] = value
			}
		}
		if form.key == "extension-status.not_found" {
			return T(l, "extension-status.missing_extension")
		}
		return T(l, form.key, args)
	}
	return text
}

// ExtensionFailureText separates unrecognized SDK/program diagnostics from a localized hub-owned summary.
func ExtensionFailureText(l Lang, text, fallback string) (string, string) {
	if text == "" {
		return "", ""
	}
	if knownExtensionStatus(text, 0) {
		return ExtensionStatusText(l, text), ""
	}
	return T(l, fallback), text
}

func knownExtensionStatus(text string, depth int) bool {
	if depth >= 16 {
		return false
	}
	for _, form := range extensionStatusTemplates() {
		match := form.pattern.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		for i, name := range form.names {
			if name == "err" && !knownExtensionStatus(match[i+1], depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// ExtensionClientJSON projects the extension list/detail/preview/operation envelope only. It never descends into
// authored manifests, configuration, views or auth prompts. Copies retain canonical status text for later requests.
func ExtensionClientJSON(l Lang, raw []byte) []byte {
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return raw
	}
	projectList := func(key string) {
		var list []json.RawMessage
		if json.Unmarshal(body[key], &list) != nil {
			return
		}
		for i, value := range list {
			list[i] = ExtensionClientJSON(l, value)
		}
		body[key], _ = json.Marshal(list)
	}
	for _, key := range []string{"extensions", "plugins", "operations", "bundle"} {
		if _, exists := body[key]; exists {
			projectList(key)
		}
	}
	for _, key := range []string{"operation", "preview"} {
		if value, exists := body[key]; exists {
			body[key] = ExtensionClientJSON(l, value)
		}
	}
	_, status := body["status"]
	_, steps := body["steps"]
	_, manifest := body["manifest"]
	_, market := body["spec"]
	if status || steps || market {
		var text string
		if json.Unmarshal(body["error"], &text) == nil && text != "" {
			fallback := "extension-status.failed"
			if steps {
				fallback = "extension-status.operation_failed"
			}
			summary, detail := ExtensionFailureText(l, text, fallback)
			body["error"], _ = json.Marshal(summary)
			if detail != "" {
				body["error_detail"], _ = json.Marshal(detail)
			}
		}
	}
	if steps {
		var list []map[string]json.RawMessage
		if json.Unmarshal(body["steps"], &list) == nil {
			for _, step := range list {
				var machines map[string]json.RawMessage
				var failures map[string]string
				if json.Unmarshal(step["machines"], &machines) != nil || json.Unmarshal(machines["errors"], &failures) != nil {
					continue
				}
				details := map[string]string{}
				for id, text := range failures {
					summary, detail := ExtensionFailureText(l, text, "extension-status.operation_failed")
					failures[id] = summary
					if detail != "" {
						details[id] = detail
					}
				}
				machines["errors"], _ = json.Marshal(failures)
				if len(details) > 0 {
					machines["error_details"], _ = json.Marshal(details)
				}
				step["machines"], _ = json.Marshal(machines)
			}
			body["steps"], _ = json.Marshal(list)
		}
	}
	if manifest || market {
		var warnings []string
		if json.Unmarshal(body["warnings"], &warnings) == nil {
			details := make([]string, len(warnings))
			hasDetails := false
			for i, text := range warnings {
				if market {
					warnings[i], details[i] = ExtensionFailureText(l, text, "extension-status.market_warning")
					hasDetails = hasDetails || details[i] != ""
				} else {
					warnings[i] = ExtensionStatusText(l, text)
				}
			}
			body["warnings"], _ = json.Marshal(warnings)
			if hasDetails {
				body["warning_details"], _ = json.Marshal(details)
			}
		}
		if manifest {
			// Existing clients already show warnings but may not understand rename_from.
			// Disclose the transition at the envelope boundary, never in author metadata.
			var from, to string
			var fields map[string]json.RawMessage
			if json.Unmarshal(body["rename_from"], &from) == nil && from != "" &&
				json.Unmarshal(body["manifest"], &fields) == nil && json.Unmarshal(fields["id"], &to) == nil && to != "" {
				notice := T(l, "extension-identity.confirmation", Args{"from": from, "to": to})
				if !slices.Contains(warnings, notice) {
					warnings = append([]string{notice}, warnings...)
				}
				body["warnings"], _ = json.Marshal(warnings)
			}
		}
	}
	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}

// ExtensionEvent applies the same copy projection to live and replayed operation events and auth failures.
func ExtensionEvent(l Lang, raw []byte) []byte {
	if bytes.Contains(raw, []byte(`"extension_operation"`)) {
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &head) == nil && head.Type == "extension_operation" {
			return ExtensionClientJSON(l, raw)
		}
	}
	return ExtensionAuthEvent(l, raw)
}

// ExtensionAuthEvent localizes only auth failures marked by the manager as hub-owned. The private transport marker
// stays in the event log for replay but is removed from every client response; extension messages stay verbatim.
func ExtensionAuthEvent(l Lang, raw []byte) []byte {
	if !bytes.Contains(raw, []byte(`"hub_auth_message"`)) {
		return raw
	}
	var event map[string]json.RawMessage
	if json.Unmarshal(raw, &event) != nil || string(event["type"]) != `"extension_auth"` {
		return raw
	}
	owned := string(event["hub_auth_message"]) == "true"
	delete(event, "hub_auth_message")
	if owned {
		var auth map[string]json.RawMessage
		var text string
		if json.Unmarshal(event["auth"], &auth) == nil && json.Unmarshal(auth["message"], &text) == nil {
			summary, detail := ExtensionFailureText(l, text, "extension-status.auth_failed")
			auth["message"], _ = json.Marshal(summary)
			if detail != "" {
				auth["message_detail"], _ = json.Marshal(detail)
			}
			event["auth"], _ = json.Marshal(auth)
		}
	}
	out, err := json.Marshal(event)
	if err != nil {
		return raw
	}
	return out
}
