// Package subscriptions is the manager's source subscriptions core (#2038, docs/extensions/subscriptions.md): a
// subscription pulls one connector source's read-only tool on a schedule by a recipe (#2037), keeps what is new, and
// holds it for the destinations (#2039). This package validates recipes, dry-runs them, schedules and runs them, and
// answers the API; it keeps its state through Store and reaches connectors through Connectors.
package subscriptions

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// Recipe is a pull recipe as the connector protocol defines it (#2037).
type Recipe = connector.SubscriptionRecipe

// The states a subscription shows (Subscription.State).
const (
	StateOff          = "off"           // neither destination is on: not pulled on schedule
	StateOK           = "ok"            // pulled on schedule; the last run succeeded or none ran yet
	StateFailing      = "failing"       // the last run failed; retried with backoff
	StateNotConnected = "not_connected" // the connector cannot be called now; not a failure, resumes by itself
	StateRateLimited  = "rate_limited"  // the connector asked to wait, or backs off after repeated failures
	StateNeedsRecipe  = "needs_recipe"  // the tool or its results changed: the recipe must be edited or made again
)

// Where a recipe came from (Subscription.RecipeOrigin).
const (
	OriginExtension = "extension"
	OriginModel     = "model"
	OriginUser      = "user"
	OriginPush      = "push" // a push source (#3848): no recipe, its device sends its items
)

// Error codes: in API errors, Subscription.Error, a dry run's error and its checks.
const (
	CodeBadRequest       = "bad_request"
	CodeBadRecipe        = "bad_recipe"
	CodeReadOnlyDevice   = "read_only_device"
	CodeNotFound         = "not_found"
	CodeConflict         = "conflict"
	CodeNeedsRecipe      = "needs_recipe"
	CodeNotReadOnly      = "not_read_only"
	CodeWriteVerb        = "write_verb"
	CodeAnnotation       = "annotation"
	CodeBadArgs          = "bad_args"
	CodeNoList           = "no_list"
	CodeNoTitle          = "no_title"
	CodeBadTime          = "bad_time"
	CodeNotDeduped       = "not_deduped"
	CodeTextNeedsSnap    = "text_needs_snapshot"
	CodeIntervalTooShort = "interval_too_short"
	CodeDryRunFailed     = "dry_run_failed"
	CodeNotConnected     = "not_connected"
	CodeRateLimited      = "rate_limited"
	CodeCallFailed       = "call_failed"
	CodeShapeChanged     = "shape_changed"
	CodeToolChanged      = "tool_changed"
	CodeNoModel          = "no_model"     // drafting: no model is set up for it
	CodeModelFailed      = "model_failed" // drafting: the model's call failed
)

// Problem is a code and one English sentence. Clients localize the code; the sentence never carries source content.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Subscription is one subscription as the API shows it (docs/extensions/subscriptions.md "API").
type Subscription struct {
	ID         string `json:"id"`
	Connector  string `json:"connector"`
	Extension  string `json:"extension"`
	Source     string `json:"source"`
	SourceName string `json:"source_name"`
	Icon       string `json:"icon,omitempty"`
	Service    string `json:"service,omitempty"`
	Label      string `json:"label"`

	Recipe       Recipe    `json:"recipe"`
	RecipeOrigin string    `json:"recipe_origin"`
	RecipeAt     time.Time `json:"recipe_at"`

	ToInbox         bool   `json:"to_inbox"`
	ToKB            bool   `json:"to_kb"`
	IntervalSeconds int    `json:"interval_seconds"`
	DailyAt         string `json:"daily_at"`
	TriageRules     string `json:"triage_rules"`
	FallbackTier    string `json:"fallback_tier"`

	State        string     `json:"state"`
	Error        *Problem   `json:"error"`
	FailCount    int        `json:"fail_count"`
	FailingSince *time.Time `json:"failing_since"`
	PausedUntil  *time.Time `json:"paused_until"`
	LastRunAt    *time.Time `json:"last_run_at"`
	LastOKAt     *time.Time `json:"last_ok_at"`
	NextRunAt    *time.Time `json:"next_run_at"`
	LastNewCount int        `json:"last_new_count"`
	WeakDedup    bool       `json:"weak_dedup"`
	Shape        *Shape     `json:"shape"`

	MinIntervalSeconds int `json:"min_interval_seconds"`

	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Kept, not shown.
	Tool            string     `json:"-"` // Recipe.Tool, a column of its own
	ToolFingerprint string     `json:"-"`
	SourceMin       int        `json:"-"` // the source's min_interval_seconds as last read
	MaxCallsPerHour int        `json:"-"` // the connector's budget as last read (0: the default)
	SinceAt         *time.Time `json:"-"` // the newest item time pulled so far
	LeaseUntil      *time.Time `json:"-"`
	ShapeMisses     int        `json:"-"` // runs in a row that pulled items without a title or id
	SnapshotHash    string     `json:"-"` // the last snapshot's content hash
	RunState        string     `json:"-"` // the state as runs left it; State is StateOff over it while both switches are off
	Runs            []Run      `json:"-"`
	// The destinations' marks (#2039, deliver.go): when the Inbox was turned on (its catch-up reaches a day before),
	// the first run since (the items stored up to it are the catch-up), and when the knowledge base was turned on
	// (it starts a day before).
	InboxFrom    *time.Time `json:"-"`
	InboxCatchup *time.Time `json:"-"`
	KBFrom       *time.Time `json:"-"`
}

// Run is one execution, as the subscription's history keeps it (the newest RunsKept).
type Run struct {
	At      time.Time `json:"at"`
	MS      int64     `json:"ms"`
	Trigger string    `json:"trigger"` // schedule, now, retry, push
	New     int       `json:"new"`
	Count   int       `json:"count"`
	Error   *Problem  `json:"error"`
}

// Run triggers.
const (
	TriggerSchedule = "schedule"
	TriggerNow      = "now"
	TriggerRetry    = "retry"
)

// Shape is what a result looked like: never a value.
type Shape struct {
	Type string   `json:"type"` // json or text
	Keys []string `json:"keys,omitempty"`
	// Arrays are the top-level keys whose values are lists: where items may be.
	Arrays []string `json:"arrays,omitempty"`
	Items  *int     `json:"items,omitempty"`
}

// ModelInfo is what a model purpose runs on now: the display name of its connection and its model, for the consent
// the pages ask before content goes to it. OK false: nothing can be called for it (drafting answers no_model).
type ModelInfo struct {
	OK       bool   `json:"ok"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Features says which later stages are in: the pages show their parts only then.
type Features struct {
	Deliver  bool `json:"deliver"`  // #2039: items reach the Inbox and the knowledge base
	Generate bool `json:"generate"` // #2040: a model can draft a recipe
}

// ConnectorSummary is one connector's line for the entry rows (Summary), from the cached catalog only.
type ConnectorSummary struct {
	Connector    string `json:"connector"`
	Extension    string `json:"extension"`
	Subscribable *bool  `json:"subscribable,omitempty"` // nil: not known yet
	On           int    `json:"on"`
	Failing      int    `json:"failing"`
}

// Catalog is what a connector offers to subscribe to, with the reason a tool cannot be used.
type Catalog struct {
	Connector string          `json:"connector"`
	Extension string          `json:"extension"`
	Status    string          `json:"status"`
	Direct    Direct          `json:"direct"`
	Sources   []CatalogSource `json:"sources"`
	Error     *Problem        `json:"error"`
}

// Direct says whether the connector's own Inbox / archive feeds are on.
type Direct struct {
	Inbox   bool `json:"inbox"`
	Archive bool `json:"archive"`
}

type CatalogSource struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Icon               string        `json:"icon,omitempty"`
	Service            string        `json:"service,omitempty"`
	MinIntervalSeconds *int          `json:"min_interval_seconds,omitempty"`
	MaxCallsPerHour    *int          `json:"max_calls_per_hour,omitempty"`
	Tools              []CatalogTool `json:"tools"`
	Recipes            []Recipe      `json:"recipes"`
	Subscriptions      []string      `json:"subscriptions"`
}

type CatalogTool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	// InputSchema is the tool's, as the connector declares it: the pages draw the recipe's argument form from it.
	InputSchema  json.RawMessage `json:"input_schema"`
	Subscribable bool            `json:"subscribable"`
	Refused      *Problem        `json:"refused,omitempty"`
}

// DryRunRequest names a subscription (ID) or a source not subscribed yet (Connector, Source); Recipe replaces the
// subscription's own.
type DryRunRequest struct {
	ID        string  `json:"id,omitempty"`
	Connector string  `json:"connector,omitempty"`
	Source    string  `json:"source,omitempty"`
	Recipe    *Recipe `json:"recipe,omitempty"`
}

// DryRun is a dry run's answer. Items and Paths hold source values: they are in this answer only, never kept or logged.
type DryRun struct {
	OK        bool    `json:"ok"`
	Checks    []Check `json:"checks"`
	Shape     *Shape  `json:"shape"`
	Count     int     `json:"count"`
	MaybeMore bool    `json:"maybe_more"`
	// ItemsPath is where the items were read: the recipe's, or the one found when it left items_path empty.
	ItemsPath string      `json:"items_path"`
	WeakDedup bool        `json:"weak_dedup"`
	Items     []Preview   `json:"items"`
	Paths     []PathValue `json:"paths"`
	Error     *Problem    `json:"error"`
}

// The checks of a dry run, always these and in this order.
const (
	CheckReadOnly = "read_only"
	CheckArgs     = "args"
	CheckFields   = "fields"
	CheckDedup    = "dedup"
	CheckInterval = "interval"
)

type Check struct {
	Key   string   `json:"key"`
	OK    bool     `json:"ok"`
	Error *Problem `json:"error,omitempty"`
}

// Preview is one item of a dry run.
type Preview struct {
	UID     string  `json:"uid"`
	New     bool    `json:"new"`
	Title   string  `json:"title"`
	Sender  string  `json:"sender"`
	Time    *string `json:"time"`
	Thread  string  `json:"thread"`
	Link    string  `json:"link"`
	DueDate *string `json:"due_date"`
	Body    string  `json:"body"`
}

// PathValue is a leaf of the first item, for the field pickers.
type PathValue struct {
	Path    string `json:"path"`
	Example string `json:"example"`
}

// Filter narrows List.
type Filter struct {
	Connector string
	Extension string
}

// CreateRequest makes a subscription: with no recipe, the source's first own one. RecipeOrigin says a given recipe
// is a model's draft taken as it was (model); otherwise it is the user's.
type CreateRequest struct {
	Connector    string  `json:"connector"`
	Source       string  `json:"source"`
	Recipe       *Recipe `json:"recipe,omitempty"`
	RecipeOrigin string  `json:"recipe_origin,omitempty"`
	Label        string  `json:"label,omitempty"`
}

// Patch changes a subscription made at Revision; nil fields stay as they are.
type Patch struct {
	Revision        int64   `json:"revision"`
	Label           *string `json:"label,omitempty"`
	Recipe          *Recipe `json:"recipe,omitempty"`
	ToInbox         *bool   `json:"to_inbox,omitempty"`
	ToKB            *bool   `json:"to_kb,omitempty"`
	IntervalSeconds *int    `json:"interval_seconds,omitempty"`
	DailyAt         *string `json:"daily_at,omitempty"`
	TriageRules     *string `json:"triage_rules,omitempty"`
	FallbackTier    *string `json:"fallback_tier,omitempty"`
}

// Actor is who changed something: a device, for plugin_audit.
type Actor struct {
	ID   string
	Name string
}

// Error is what the Service answers on failure: Status is the HTTP status for the API; DryRun is set when a dry run
// refused a recipe, Current on a revision conflict.
type Error struct {
	Status     int           `json:"-"`
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	RetryAfter float64       `json:"retry_after,omitempty"`
	DryRun     *DryRun       `json:"-"`
	Current    *Subscription `json:"-"`
	// Draft is a model's draft that did not pass, as far as it got: the pages start the manual editor from it.
	Draft *Recipe `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errorf(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func notFound(what string) *Error {
	return errorf(http.StatusNotFound, CodeNotFound, what+" not found")
}

// Item is one pulled item held for the destinations (subscription_items).
type Item struct {
	SubscriptionID string     `json:"-"`
	UID            string     `json:"uid"`
	Thread         string     `json:"thread,omitempty"`
	Title          string     `json:"title"`
	Sender         string     `json:"sender,omitempty"`
	Time           *time.Time `json:"time,omitempty"`
	Body           string     `json:"body,omitempty"`
	Link           string     `json:"link,omitempty"`
	DueDate        *time.Time `json:"due_date,omitempty"`
	Kind           string     `json:"kind"`
	Snapshot       bool       `json:"snapshot,omitempty"`
}

// Seen is a dedup key kept for a subscription (subscription_seen).
type Seen struct {
	UID         string
	ContentHash string
}

// rawJSON is a JSON object value used where a recipe's args default.
var emptyObject = json.RawMessage(`{}`)
