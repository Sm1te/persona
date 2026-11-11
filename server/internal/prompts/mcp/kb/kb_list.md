List knowledge-base entities of one kind ranked by importance (P0 first), relevance to the user, recency: projects (short = a feature / launch, long = a team mission), chats, people, docs, meetings, events, recordings (the user's phone / watch recordings; newest first, saying which have meeting minutes, which are all-day recordings with a day recap and its state, and which transcripts have expired), topics (knowledge points curated by subject out of the timelines and chats: concepts, decisions with their reasons, practices, pitfalls, facts, open questions; the biggest first). Transcripts are kept for recording_days (14 days by default); meetings' minutes and all-day recordings' recaps are kept longer under minutes_days (forever by default). Meetings, events and recordings show when they started, in the user's time zone. Optional tag filter.

```json
{
  "type": "object",
  "required": ["kind"],
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["projects", "chats", "people", "docs", "meetings", "events", "recordings", "topics"]
    },
    "tag": {
      "type": "string"
    },
    "limit": {
      "type": "integer",
      "description": "default 30"
    }
  }
}
```
