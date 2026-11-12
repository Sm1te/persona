List the user's source subscriptions: each pulls one connector source's read-only tool on a schedule into the Inbox and / or the knowledge base. Each line has its id, label, connector, source, tool, mode, kind, interval (or daily time for a snapshot), the two destination switches, its state (`off` both destinations off, `ok`, `failing`, `not_connected`, `rate_limited`, `needs_recipe`) with its error, the last run, the last successful run, the next run, how many new items the last run brought, and its revision (what subscription_save takes). A push source (filled by a device's shortcut) has no tool or schedule. Times are UTC; `time_zone` is the user's.

```json
{
  "type": "object",
  "properties": {
    "connector": {"type": "string", "description": "only this connector's"}
  },
  "additionalProperties": false
}
```
