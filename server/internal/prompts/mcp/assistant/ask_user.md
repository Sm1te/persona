Ask the user one or more multiple-choice questions on their iPhone (a card in this conversation plus a push notification) and wait for the answers. Use it to confirm anything risky (sending messages / email for them, deleting, changing live systems) or to let them pick between options. AskUserQuestion does not work here; use this instead. The user can also type a free-form answer. Waits up to 2 hours; if the user writes in this conversation instead of picking an option, the question is withdrawn and the tool returns at once, with their message following. If nobody answers in time, the question becomes a to-do that waits for the user's call (in their to-do list and the next briefing), and their answer comes to this conversation later as a new message. With a deadline (each question then recommends one of its options): if nobody answers by it and you marked it reversible, the recommended options are taken by default and recorded on the question's to-do; one that cannot be undone is only reminded again and keeps waiting. With wait false the card is put up the same way but this call returns at once without the answer: end the turn without guessing it, and the user's answer comes to this conversation later as a new message that quotes the question and the answer (so does word that the card was closed unanswered, or expired and became a to-do). Use wait false only where your instructions say to; a confirmation this same turn needs (approving a message before it is sent) keeps the default.

```json
{
  "type": "object",
  "required": ["questions"],
  "properties": {
    "wait": {
      "type": "boolean",
      "description": "default true: hold this turn until the user answers. false: put the card up and return at once; the answer arrives later as a new message in this conversation"
    },
    "deadline": {
      "type": "string",
      "description": "optional: until when the questions wait, a duration (\"30m\", \"4h\") or an RFC 3339 time, 1 minute to 30 days ahead; needs reversible and a recommend on every question"
    },
    "reversible": {
      "type": "boolean",
      "description": "with deadline: whether going ahead with the recommended options can be undone later. false for deleting, sending anything to other people, spending money or changing production: those are never taken by default"
    },
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "items": {
        "type": "object",
        "required": ["question", "options"],
        "properties": {
          "question": {
            "type": "string"
          },
          "header": {
            "type": "string",
            "description": "short label, ≤12 chars"
          },
          "multiSelect": {
            "type": "boolean"
          },
          "recommend": {
            "type": "string",
            "description": "the label of the option you recommend; required with deadline"
          },
          "options": {
            "type": "array",
            "minItems": 2,
            "maxItems": 4,
            "items": {
              "type": "object",
              "required": ["label"],
              "properties": {
                "label": {
                  "type": "string"
                },
                "description": {
                  "type": "string"
                }
              }
            }
          }
        }
      }
    }
  }
}
```
