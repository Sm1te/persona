Read one entity: its metadata (links to people / chats / projects, tags), compressed context (summary.md), a project's timeline, a chat's messages of the last 7 days, a recording's meeting minutes, or an all-day recording's day recap (timeline of the day, meetings apart from the rest, suggested to-dos), with where each action item stands (a suggestion, or its to-do: open, done, cancelled, deleted) and its timestamped transcript while still retained (recordings: the user's phone / watch recordings, text only), a meeting's or event's recordings, a topic's knowledge points by subtopic (each with its kind, reason and the entries it came from). Transcripts expire after recording_days (14 days by default); meetings then keep only their minutes, and all-day recordings their recap, under minutes_days (forever by default). id from kb_list / kb_search (a project also by name).

```json
{
  "type": "object",
  "required": ["kind", "id"],
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["projects", "chats", "people", "docs", "meetings", "events", "recordings", "topics"]
    },
    "id": {
      "type": "string"
    }
  }
}
```
