Try a recipe once without saving anything: it calls the tool and answers `dry_run` (`ok`, the five `checks` read_only, args, fields, dedup, interval with their errors; `count` items, `maybe_more` when a full page was all new, `items_path` where the items were found, up to 3 sample `items` as the Inbox would show them (title, sender, time, `summary` the row's preview line, and `problems`: the mapped fields that item has no readable value for), the first item's leaf `paths` with examples for writing `fields`, and `warnings`) and `recipe`, the recipe tried with its items_path filled in, to pass to subscription_save. Warnings do not fail a dry run but mean the recipe will not do what it seems: `unknown_key` (a key the format does not know, such as `fields.author` or `fields.url`: it is dropped and its column stays empty; use the keys in recipe_format) and `empty_field` (a mapped field whose path is empty in every item pulled). Give a subscription's `id` to try its own recipe, or a change to it: `recipe` is then merged onto its recipe (keys given replace, null removes, objects such as fields and args merge). Give `connector` and `source` with a whole `recipe` for a source not subscribed yet (no recipe: the source's own). Leave `items_path` out to have it found.

```json
{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "an existing subscription: try its recipe, or recipe merged onto it"},
    "connector": {"type": "string", "description": "without id: the connector"},
    "source": {"type": "string", "description": "without id: the source"},
    "recipe": {"type": "object", "description": "a recipe, or with id the change to its recipe: {mode, tool, args, items_path, fields, kind, group_by} (subscription_sources recipe_format)"}
  },
  "additionalProperties": false
}
```
