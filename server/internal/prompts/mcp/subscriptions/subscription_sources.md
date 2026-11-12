What can be subscribed to, and how a recipe is written. Without `connector`: every connector's sources as last read (never a call), each with its tools (refused ones with the reason's code), how many recipes of its own it ships and its subscriptions; connectors not read yet are named. With `connector` (and `source`): that connector's catalog read now, each tool's description and `input_schema`, `subscribable` or `refused` with why, the source's own recipes and `min_interval_seconds`. Every answer has `recipe_format`, taken from the code: the recipe's keys, the `fields` keys and what each fills, path syntax, `items_path`, the template variables `{{since}}` / `{{since_date}}`, the modes, kinds, `group_by` values and interval rules. Only read-only tools whose names say no write can be subscribed to.

```json
{
  "type": "object",
  "properties": {
    "connector": {"type": "string", "description": "read this connector's catalog now, with input schemas"},
    "source": {"type": "string", "description": "with connector: only this source"}
  },
  "additionalProperties": false
}
```
