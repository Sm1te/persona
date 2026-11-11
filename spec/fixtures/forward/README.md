# Forwarded messages

A message forwarded from one chat to another (#3807) carries the original in a `<persona-context kind="forward">` block
before the note the user typed. The format is in [protocol.md](../../../docs/protocol/protocol.md), "Forwarded messages".
The iOS app (`apple/Persona/Views/Chat/Forward.swift`, `ContextRef` in `apple/Persona/Views/Link/LinkSheet.swift`)
writes and reads these blocks and checks them against these cases; the web app and Android follow the same cases.

## Rules

- The opening tag is `kind`, `title`, `url`, `msg`, then the metadata in this order: `role`, `author`, `chat`,
  `project`, `machine`, `time`; any other attribute follows, by name. `write_agent_reply`
- The text sits on its own lines between the tags. `write_agent_reply`
- Attribute values escape `&`, `"`, `<` and `>`; a closing tag inside the text is taken out. `write_escapes`
- `url` is `persona://session/<machine id>/<session id>?at=<item id>`, the item id percent-encoded (`#` as `%23`).
  `write_agent_reply`, `read_with_note`
- Reading back gives the block's fields, unknown attributes included, and the note after it; the origin is the
  session and item `url` names. `read_with_note`

## Cases

`cases.json` holds `{"version": 1, "cases": [...]}`. A writing case has `block` (the fields) and `xml` (the block it
writes); a reading case has `message` and `expected`.
