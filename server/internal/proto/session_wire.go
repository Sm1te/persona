package proto

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"hash/fnv"
	"reflect"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// A session is encoded on every list and push the hub sends, and the reflect encoder spent most of that time walking
// its hundred-odd fields, most of them empty. appendSession writes the same bytes field by field. sessionWire, with
// encoding/json's options, stays the reference: TestSessionWireMatchesReflect compares the two for each field, each
// kind of value and each way a session is encoded, so a field added to Session without a line here fails there.

// sessionWireLayout is the hash (sessionLayout) of the Session fields appendSession is written for. While Session's
// fields differ from them (a field added, renamed, retyped or retagged by a change that did not update this file),
// appendSession leaves every session to the reflect encoder: the session is sent whole, only slower, until its line
// here and this hash are brought up to date. TestSessionWireMatchesReflect fails meanwhile and prints the new hash.
const sessionWireLayout = 0x375815a2ca60a60b

var sessionWireCurrent = sessionLayout() == sessionWireLayout && hierarchyLayout() == hierarchyWireLayout

// sessionLayout hashes the name, type and tag of each of Session's fields, in order.
func sessionLayout() uint64 {
	return typeLayout(reflect.TypeFor[Session]())
}

// The nested values sessions carry most, background tasks and subagents (with their latest tool), are written by hand
// too, each type under its own layout hash: while one's fields differ from the ones written here, its values are left to
// the reflect encoder (value), the rest of the session still written here.
const (
	backgroundWireLayout   = 0xf3f44642839ae39d
	subagentWireLayout     = 0xbf84f2ee699de074
	workflowToolWireLayout = 0x56c71f2c71223998
)

// hierarchyWireLayout is the layout hash of SessionHierarchy and SessionChildren, whose members appendSession writes
// on the row: while either differs, appendSession leaves every session to the reflect encoder, as for sessionWireLayout.
const hierarchyWireLayout = 0x3fe24b775756daab

// hierarchyLayout hashes SessionHierarchy's fields, then SessionChildren's.
func hierarchyLayout() uint64 {
	return typeLayout(reflect.TypeFor[SessionHierarchy]())*31 + typeLayout(reflect.TypeFor[SessionChildren]())
}

var (
	backgroundWireCurrent = typeLayout(reflect.TypeFor[BackgroundTask]()) == backgroundWireLayout
	subagentWireCurrent   = typeLayout(reflect.TypeFor[Subagent]()) == subagentWireLayout &&
		typeLayout(reflect.TypeFor[WorkflowTool]()) == workflowToolWireLayout
)

// typeLayout hashes the name, type and tag of each of a struct type's fields, in order.
func typeLayout(t reflect.Type) uint64 {
	h := fnv.New64a()
	for i := range t.NumField() {
		f := t.Field(i)
		h.Write([]byte(f.Name + " " + f.Type.String() + " " + string(f.Tag) + "\n"))
	}
	return h.Sum64()
}

// appendSession appends to b what encoding sessionWire for s with encoding/json's options gives; false when s holds a
// value it leaves to that encoder (a time outside the years 0-9999, a nested value that does not encode), or when
// Session's fields are not the ones it was written for (sessionWireLayout), which then encodes s itself (the same
// bytes, or the same error).
func appendSession(b []byte, s *Session) ([]byte, bool) {
	if !sessionWireCurrent {
		return b, false
	}
	w := wireWriter{b: append(b, '{'), first: true, ok: true}
	if s.ToolLoop != nil {
		w.value(`"tool_loop":`, s.ToolLoop)
	}
	w.str(`"manager":`, s.Manager, true)
	w.str(`"machine_id":`, s.MachineID, false)
	w.str(`"session_id":`, s.SessionID, false)
	w.str(`"source_kind":`, s.SourceKind, true)
	w.str(`"source_thread_id":`, s.SourceThreadID, true)
	w.str(`"source_instance_id":`, s.SourceInstanceID, true)
	w.str(`"parent_thread_id":`, s.ParentThreadID, true)
	w.str(`"parent_session_id":`, s.ParentSessionID, true)
	w.str(`"root_session_id":`, s.RootSessionID, true)
	w.str(`"read_only_reason":`, s.ReadOnlyReason, true)
	if s.Capabilities != nil {
		w.value(`"capabilities":`, s.Capabilities)
	}
	w.boolean(`"is_subagent":`, s.IsSubagent, true)
	w.boolean(`"attached":`, s.Attached, true)
	w.boolean(`"can_fork":`, s.CanFork, true)
	w.boolean(`"started_by_persona":`, s.StartedByPersona, false)
	w.boolean(`"assistant":`, s.Assistant, false)
	w.boolean(`"archived":`, s.Archived, true)
	w.boolean(`"title_manual":`, s.TitleManual, true)
	w.boolean(`"bypass_available":`, s.BypassAvailable, true)
	w.str(`"title":`, s.Title, false)
	w.str(`"cwd":`, s.Cwd, false)
	w.str(`"workspace_cwd":`, s.WorkspaceCwd, true)
	w.str(`"repo_root":`, s.RepoRoot, true)
	w.str(`"worktree":`, s.Worktree, true)
	w.str(`"branch":`, s.Branch, true)
	w.str(`"status":`, s.Status, false)
	w.str(`"pane_id":`, s.PaneID, true)
	w.str(`"tab_id":`, s.TabID, true)
	w.str(`"workspace_id":`, s.WorkspaceID, true)
	w.str(`"agent_name":`, s.AgentName, true)
	w.str(`"codex_route":`, s.CodexRoute, true)
	w.str(`"codex_route_note":`, s.CodexRouteNote, true)
	w.str(`"agent":`, s.Agent, true)
	w.str(`"agent_label":`, s.AgentLabel, true)
	w.str(`"agent_surface":`, s.AgentSurface, true)
	w.str(`"launcher":`, s.Launcher, true)
	w.str(`"model":`, s.Model, true)
	w.str(`"permission_mode":`, s.PermissionMode, true)
	w.str(`"effort":`, s.Effort, true)
	if s.Ultracode != nil {
		w.boolean(`"ultracode":`, *s.Ultracode, false)
	}
	w.str(`"ultracode_note":`, s.UltracodeNote, true)
	if s.UltracodeLostAt != nil {
		w.time(`"ultracode_lost_at":`, *s.UltracodeLostAt)
	}
	if s.PendingSwitch != nil {
		w.value(`"pending_switch":`, s.PendingSwitch)
	}
	w.integer(`"context_tokens":`, int64(s.ContextTokens), true)
	w.integer(`"context_window":`, int64(s.ContextWindow), true)
	w.integer(`"output_tokens":`, int64(s.OutputTokens), true)
	w.strs(`"efforts":`, s.Efforts)
	w.str(`"service_tier":`, s.ServiceTier, true)
	if len(s.ServiceTiers) > 0 {
		w.value(`"service_tiers":`, s.ServiceTiers)
	}
	w.str(`"space":`, s.Space, true)
	if s.ArchiveChangedAt != nil {
		w.time(`"archive_changed_at":`, *s.ArchiveChangedAt)
	}
	if s.NativeArchive != nil {
		w.value(`"native_archive":`, s.NativeArchive)
	}
	w.str(`"origin":`, s.Origin, true)
	w.str(`"origin_ref":`, s.OriginRef, true)
	w.str(`"opened_by":`, s.OpenedBy, true)
	if s.Compaction != nil {
		w.value(`"compaction":`, s.Compaction)
	}
	w.strs(`"channels":`, s.Channels)
	w.str(`"space_kind":`, s.SpaceKind, true)
	w.str(`"space_id":`, s.SpaceID, true)
	w.str(`"space_name":`, s.SpaceName, true)
	w.integer(`"space_index":`, int64(s.SpaceIndex), true)
	w.str(`"parent_space_id":`, s.ParentSpaceID, true)
	w.str(`"slot_id":`, s.SlotID, true)
	w.str(`"slot_name":`, s.SlotName, true)
	w.integer(`"slot_index":`, int64(s.SlotIndex), true)
	if s.PaneIndex != nil {
		w.integer(`"pane_index":`, int64(*s.PaneIndex), false)
	}
	w.str(`"state":`, s.State, false)
	w.boolean(`"background_working":`, s.BackgroundWorking, true)
	w.boolean(`"background_waiting":`, s.BackgroundWaiting, true)
	w.boolean(`"woke_by_task":`, s.WokeByTask, true)
	w.boolean(`"starting":`, s.Starting, true)
	w.boolean(`"quiet":`, s.Quiet, true)
	w.integer(`"queued_relayed":`, int64(s.QueuedRelayed), true)
	w.time(`"seen_at":`, s.SeenAt) // time.Time is a struct, never empty, so omitempty keeps it
	w.time(`"state_since":`, s.StateSince)
	w.strs(`"queued":`, s.Queued)
	if len(s.Background) > 0 {
		if backgroundWireCurrent {
			w.key(`"background":`)
			w.backgroundTasks(s.Background)
		} else {
			w.value(`"background":`, &s.Background)
		}
	}
	if len(s.Subagents) > 0 {
		if subagentWireCurrent {
			w.key(`"subagents":`)
			w.subagents(s.Subagents)
		} else {
			w.value(`"subagents":`, &s.Subagents)
		}
	}
	if len(s.Workflows) > 0 {
		w.value(`"workflows":`, &s.Workflows)
	}
	if s.Loop != nil {
		w.value(`"loop":`, s.Loop)
	}
	if s.Goal != nil {
		w.value(`"goal":`, s.Goal)
	}
	if s.Plan != nil {
		w.value(`"plan":`, s.Plan)
	}
	if len(s.Ports) > 0 {
		w.value(`"ports":`, &s.Ports)
	}
	w.time(`"updated_at":`, s.UpdatedAt)
	w.str(`"last_text":`, s.LastText, true)
	w.str(`"asks":`, s.Asks, true)
	w.boolean(`"ultracode_pending":`, s.UltracodePending, true)
	w.boolean(`"asks_read":`, s.AsksRead, true)
	w.boolean(`"awaits_reply":`, s.AwaitsReply, true)
	w.boolean(`"turn_error_resumes":`, s.TurnErrorResumes, true)
	w.boolean(`"turn_error_after_work":`, s.TurnErrorAfterWork, true)
	if s.TitleSource != TitleSourceUnknown {
		w.str(`"title_source":`, s.TitleSource.String(), false)
	}
	w.str(`"turn_error":`, s.TurnError, true)
	w.str(`"turn_error_kind":`, s.TurnErrorKind, true)
	if s.TurnErrorResetsAt != nil {
		w.time(`"turn_error_resets_at":`, *s.TurnErrorResetsAt)
	}
	w.str(`"turn_error_code":`, s.TurnErrorCode, true)
	if s.ModelFallback != nil {
		w.value(`"model_fallback":`, s.ModelFallback)
	}
	if s.ModelRestore != nil {
		w.value(`"model_restore":`, s.ModelRestore)
	}
	if s.Work != nil {
		w.value(`"work":`, s.Work)
	}
	w.str(`"activity":`, s.Activity, true)
	if s.ActivityAt != nil {
		w.time(`"activity_at":`, *s.ActivityAt)
	}
	w.integer(`"rev":`, s.Rev, true)
	w.integer(`"pending_interactions":`, int64(s.PendingInteractions), false)
	w.str(`"suggestion":`, s.Suggestion, true)
	w.str(`"start_error":`, s.StartError, true)
	w.str(`"start_error_code":`, s.StartErrorCode, true)
	if len(s.StartErrorArgs) > 0 {
		w.value(`"start_error_args":`, &s.StartErrorArgs)
	}
	if s.Todo != nil {
		w.value(`"todo":`, s.Todo)
	}
	w.str(`"spawn_from":`, s.SpawnFrom, true)
	w.str(`"attention_reason":`, s.AttentionReason, true)
	w.str(`"failure_reason":`, s.FailureReason, true)
	w.str(`"external_url":`, s.ExternalURL, true)
	w.str(`"runtime":`, s.Runtime, true)
	w.str(`"runtime_fallback":`, s.RuntimeFallback, true)
	w.str(`"chat":`, s.Chat, true)
	w.str(`"topic":`, s.Topic, true)
	w.strs(`"dispatched":`, s.Dispatched)
	w.strs(`"also_open_in":`, s.AlsoOpenIn)
	w.str(`"host":`, s.Host, true)
	if h := s.Hierarchy; h != nil { // sessionWire's hierarchy members, keys of the row itself (compatibility.go)
		if !h.StartedAt.IsZero() {
			w.time(`"started_at":`, h.StartedAt)
		}
		w.str(`"asking_parent_session_id":`, h.AskingParentSessionID, true)
		w.str(`"folded_under":`, h.FoldedUnder, true)
		if c := h.Children; c != nil {
			w.key(`"children":`)
			w.open()
			w.integer(`"total":`, int64(c.Total), false)
			w.integer(`"working":`, int64(c.Working), true)
			w.integer(`"needs_you":`, int64(c.NeedsYou), true)
			w.close()
		}
	}
	w.boolean(legacyStartedMember, s.StartedByPersona, false) // sessionWire's legacy key (compatibility.go)
	return append(w.b, '}'), w.ok
}

// wireWriter writes an object's members, as the reflect encoder does with encoding/json's options; ok turns false on
// a value it does not write.
type wireWriter struct {
	b         []byte
	first, ok bool
}

// key starts a member; k is its quoted name and colon.
func (w *wireWriter) key(k string) {
	if !w.first {
		w.b = append(w.b, ',')
	}
	w.first = false
	w.b = append(w.b, k...)
}

func (w *wireWriter) str(k, v string, omitEmpty bool) {
	if omitEmpty && v == "" {
		return
	}
	w.key(k)
	w.b = appendWireString(w.b, v)
}

func (w *wireWriter) boolean(k string, v, omitEmpty bool) {
	if omitEmpty && !v {
		return
	}
	w.key(k)
	w.b = strconv.AppendBool(w.b, v)
}

func (w *wireWriter) integer(k string, v int64, omitEmpty bool) {
	if omitEmpty && v == 0 {
		return
	}
	w.key(k)
	w.b = strconv.AppendInt(w.b, v, 10)
}

// strs writes a []string under omitempty: nothing for an empty one.
func (w *wireWriter) strs(k string, v []string) {
	if len(v) == 0 {
		return
	}
	w.key(k)
	w.b = append(w.b, '[')
	for i, s := range v {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		w.b = appendWireString(w.b, s)
	}
	w.b = append(w.b, ']')
}

// time writes t as time.Time's MarshalJSON does (RFC 3339 with nanoseconds); a year it refuses is left to it.
func (w *wireWriter) time(k string, t time.Time) {
	if y := t.Year(); y < 0 || y > 9999 {
		w.ok = false
		return
	}
	if _, off := t.Zone(); off <= -24*60*60 || off >= 24*60*60 {
		w.ok = false
		return
	}
	w.key(k)
	w.b = append(t.AppendFormat(append(w.b, '"'), time.RFC3339Nano), '"')
}

// open and close write a nested object around its members, after its key.
func (w *wireWriter) open() {
	w.b = append(w.b, '{')
	w.first = true
}

func (w *wireWriter) close() {
	w.b = append(w.b, '}')
	w.first = false
}

// backgroundTasks writes a non-empty []BackgroundTask as the reflect encoder does.
func (w *wireWriter) backgroundTasks(list []BackgroundTask) {
	w.b = append(w.b, '[')
	for i := range list {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		t := &list[i]
		w.open()
		w.str(`"id":`, t.ID, false)
		w.str(`"kind":`, t.Kind, false)
		w.str(`"description":`, t.Description, true)
		w.str(`"command":`, t.Command, true)
		w.str(`"output_file":`, t.OutputFile, true)
		w.time(`"started_at":`, t.StartedAt)
		if t.ExpiresAt != nil {
			w.time(`"expires_at":`, *t.ExpiresAt)
		}
		w.str(`"run_id":`, t.RunID, true)
		w.close()
	}
	w.b = append(w.b, ']')
}

// subagents writes a non-empty []Subagent as the reflect encoder does.
func (w *wireWriter) subagents(list []Subagent) {
	w.b = append(w.b, '[')
	for i := range list {
		if i > 0 {
			w.b = append(w.b, ',')
		}
		a := &list[i]
		w.open()
		w.str(`"id":`, a.ID, false)
		w.str(`"session_id":`, a.SessionID, false)
		w.str(`"type":`, a.Type, true)
		w.str(`"description":`, a.Description, true)
		w.str(`"tool_use_id":`, a.ToolUseID, true)
		w.str(`"parent_id":`, a.ParentID, true)
		w.boolean(`"fork":`, a.Fork, true)
		w.boolean(`"running":`, a.Running, false)
		w.boolean(`"background":`, a.Background, true)
		w.time(`"started_at":`, a.StartedAt) // omitempty keeps a time, a struct
		w.time(`"updated_at":`, a.UpdatedAt)
		w.str(`"last_text":`, a.LastText, true)
		w.integer(`"steps":`, int64(a.Steps), true)
		if t := a.LastTool; t != nil {
			w.key(`"last_tool":`)
			w.open()
			w.str(`"id":`, t.ID, true)
			w.str(`"name":`, t.Name, false)
			w.str(`"summary":`, t.Summary, false)
			w.close()
		}
		w.str(`"model":`, a.Model, true)
		w.integer(`"tokens":`, int64(a.Tokens), true)
		w.close()
	}
	w.b = append(w.b, ']')
}

// value writes a nested value (a struct, a list of them, a map) as encoding/json does, straight after the key: these
// have many fields of their own and are set on fewer sessions, so they are not written out by hand. v is a pointer (to
// the field, for a list or a map), so passing it allocates nothing.
func (w *wireWriter) value(k string, v any) {
	w.key(k)
	buf := nestedBuffers.Get().(*bytes.Buffer)
	*buf = *bytes.NewBuffer(w.b) // writes go on after w.b, into its room when there is some
	if jsonv2.MarshalWrite(buf, v, json.DefaultOptionsV1()) != nil {
		w.ok = false // json.Marshal's options, so the same bytes and the same failures
	}
	w.b = buf.Bytes()
	*buf = bytes.Buffer{}
	nestedBuffers.Put(buf)
}

// nestedBuffers are the bytes.Buffers value hands json/v2, which writes into one in place.
var nestedBuffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

const wireHex = "0123456789abcdef"

// appendWireString appends s quoted as encoding/json writes a string: '"', '\\' and the control characters escaped
// (\b, \f, \n, \r, \t or \u00XX), '<', '>', '&', U+2028 and U+2029 as \uXXXX, and each byte that is not UTF-8 as
// U+FFFD.
func appendWireString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '"', '\\':
				b = append(b, '\\', c)
			case '\b':
				b = append(b, '\\', 'b')
			case '\f':
				b = append(b, '\\', 'f')
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', wireHex[c>>4], wireHex[c&0xf])
			}
			i++
			start = i
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b = append(append(b, s[start:i]...), "\ufffd"...)
		case r == '\u2028' || r == '\u2029':
			b = append(append(b, s[start:i]...), '\\', 'u', '2', '0', '2', wireHex[r&0xf])
		default:
			i += n
			continue
		}
		i += n
		start = i
	}
	return append(append(b, s[start:]...), '"')
}
