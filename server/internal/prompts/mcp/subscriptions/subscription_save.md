Create, change or delete a subscription for the user. Each save puts a card on the user's phone saying what changes (before → after, and for a destination turned on what is pulled, how often, where it is kept and which model triages it); nothing changes until they approve it there, and an unanswered card changes nothing. Change only what the user asked for. `create` needs `connector` and `source` (subscription_sources) and takes a whole `recipe` (none: the source's own); it starts with both destinations off unless you set `to_inbox` / `to_kb`. `update` and `delete` need `id` and the `revision` subscription_get or subscription_list showed (a changed one is a conflict: read it again). On update, give only what changes: `label`, `recipe` (merged onto the current recipe as subscription_dry_run does), `interval_seconds`, `daily_at` (snapshots, HH:MM), `triage_rules`, `fallback_tier`, `to_inbox`, `to_kb`, or `pause` (both destinations off: nothing is pulled until one is turned on again). A recipe is dry-run first; one that fails, or has keys the format does not know, is not saved and the answer says why. When the recipe changes, `existing` says what happens to what the new recipe finds at once (it is all new to the subscription): `digest` (default) brings more than 3 into the Inbox as one digest row, as when the Inbox is turned on; `baseline` records them as seen and delivers none, for when they were delivered before (for example only the field mapping changed); `deliver` brings each in as new. `delete_data` with delete also removes what it added to the Inbox and the knowledge base. The answer has the saved subscription and, for an update, `previous`: the values to put it back with another save.

```json
{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["create", "update", "delete"]},
    "id": {"type": "string", "description": "update / delete: the subscription"},
    "revision": {"type": "integer", "description": "update / delete: the revision you read"},
    "connector": {"type": "string", "description": "create: the connector"},
    "source": {"type": "string", "description": "create: the source"},
    "label": {"type": "string", "description": "the name the user sees"},
    "recipe": {"type": "object", "description": "create: the whole recipe; update: the change to it"},
    "existing": {"type": "string", "enum": ["digest", "baseline", "deliver"], "description": "with a recipe that changes: what its first finds become, default digest"},
    "interval_seconds": {"type": "integer", "description": "how often to pull, at least 300 and the source's minimum (a snapshot at least 3600)"},
    "daily_at": {"type": "string", "description": "a snapshot's daily time, HH:MM in the user's zone; \"\" to use interval_seconds"},
    "triage_rules": {"type": "string", "description": "rules for triaging its items in the Inbox; \"\" for the extension's or the Inbox's own"},
    "fallback_tier": {"type": "string", "enum": ["", "fyi", "action", "wait"], "description": "without a triage model: \"\" FYI (Needs you with a due date), fyi, action, or wait for the model"},
    "to_inbox": {"type": "boolean", "description": "add its items to the Inbox"},
    "to_kb": {"type": "boolean", "description": "save its items to the knowledge base"},
    "pause": {"type": "boolean", "description": "turn both destinations off"},
    "delete_data": {"type": "boolean", "description": "delete: also remove what it added to the Inbox and the knowledge base"}
  },
  "required": ["action"],
  "additionalProperties": false
}
```
