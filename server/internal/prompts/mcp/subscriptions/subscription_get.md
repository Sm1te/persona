Read one subscription in full: its whole recipe (mode, tool, args, items_path, fields, kind, group_by) and where it came from, its schedule, destinations, triage rules, fallback tier, state and error, the shape of the last result, its newest runs (newest first: when, how long, trigger, new and total items, `baseline` items recorded without delivering, error) and its newest held items as samples (title, sender, time, thread, link, the body cut to 300 characters). Use it before changing a subscription, for its revision and current values. A message whose `<persona-context>` is about a subscription (the subscription page's "configure with the assistant") names `subscription`: read that one first; one naming only `connector` and `source` is a source not subscribed yet: start with subscription_sources for them.

```json
{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "the subscription's id (subscription_list)"},
    "runs": {"type": "integer", "minimum": 0, "maximum": 20, "description": "how many runs, default 5"},
    "items": {"type": "integer", "minimum": 0, "maximum": 20, "description": "how many newest items, default 5"}
  },
  "required": ["id"],
  "additionalProperties": false
}
```
