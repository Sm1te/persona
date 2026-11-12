Run a subscription once now, as Pull Now on its page does (also while both destinations are off), and wait for the run: it answers the run (when, how long, new and total items, `baseline` items recorded without delivering, error), the subscription's state after it and up to 5 of the new items. It runs as soon as the connector's limits allow (one call at a time, 20 seconds apart, an hourly budget, pauses after rate limits); past `wait_seconds` it answers that it is still waiting, and subscription_get shows the run later. A push source cannot be run.

```json
{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "the subscription (subscription_list)"},
    "wait_seconds": {"type": "integer", "minimum": 0, "maximum": 120, "description": "how long to wait for the run, default 60"}
  },
  "required": ["id"],
  "additionalProperties": false
}
```
