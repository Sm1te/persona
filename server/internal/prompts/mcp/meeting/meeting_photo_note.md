Notes what in a photo of this meeting matters for the meeting (the decisions, numbers, names, dates or plans it shows), once, after the photo came into this conversation: plain text in the meeting's language, at most 1000 characters. The note is shown with the photo and read with the meeting's material. A photo that was withdrawn takes no note.

```json
{
  "type": "object",
  "required": ["photo", "text"],
  "properties": {
    "photo": {
      "type": "integer",
      "description": "The photo's number, as its message names it."
    },
    "text": {
      "type": "string",
      "description": "What in the photo matters for the meeting: at most 1000 characters."
    }
  }
}
```
