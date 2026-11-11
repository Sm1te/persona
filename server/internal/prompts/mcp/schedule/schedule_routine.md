Set up a recurring routine: every day (or on some weekdays) at a time, Persona starts a new conversation with `prompt`, you do the work, and the user gets a push with your answer (with `push: "asked"`, only what you send with notify_user). Use for "每天 9 点给我早报", "每周五下午起草周报". For one-off reminders use schedule_followup. For times a time of day and weekdays can't say, pass `cron` instead of `time` / `weekdays`: a standard 5-field cron expression `minute hour day-of-month month day-of-week` read in the user's time zone, such as `*/30 9-18 * * 1-5` (every 30 minutes from 9:00 to 18:30 on weekdays), `0 10 1 * *` (10:00 on the 1st of each month) or `0 9 * * 1,3,5`, or an alias (@hourly, @daily, @weekly, @monthly, @yearly). Day of week 0 and 7 are Sunday; when both day of month and day of week are restricted, a day matching either runs. Runs must be at least 5 minutes apart; seconds, @every and TZ= are refused. A bad expression is refused with what is wrong; the reply lists the next runs, so check them against what the user asked. Pass `id` (from list_followups) to change an existing routine: only the fields you give change; `enabled: false` pauses it. A routine can take `until`, an instant after which it stops (RFC 3339 or 30m / 2h / 1d, not a bare clock time): Persona then turns it off and records a skipped run instead of running. Omit `until` to keep an existing routine's end time; null clears it. Times are in the user's time zone. A routine runs on Main chat's launcher, model and thinking effort unless you give it its own: pass `launcher` (a name from machines_list / the launcher list on the computer where its runs start), and `model` / `effort` for that launcher (a model or effort alone pins the launcher it runs on now). An empty `launcher` clears the override and returns the routine to Main chat. Leave these out to keep what the routine has.

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string",
      "description": "to change an existing routine"
    },
    "title": {
      "type": "string",
      "description": "short name shown in the app, e.g. 早报"
    },
    "prompt": {
      "type": "string",
      "description": "what to do each time, self-contained"
    },
    "time": {
      "type": "string",
      "description": "HH:MM, 24-hour"
    },
    "weekdays": {
      "type": "array",
      "items": {
        "type": "integer",
        "minimum": 1,
        "maximum": 7
      },
      "description": "1 = Monday … 7 = Sunday; omit or empty = every day"
    },
    "cron": {
      "type": "string",
      "description": "instead of time / weekdays: minute hour day-of-month month day-of-week in the user's time zone, e.g. */30 9-18 * * 1-5 or 0 10 1 * *; or @hourly / @daily / @weekly / @monthly / @yearly"
    },
    "enabled": {
      "type": "boolean"
    },
    "report": {
      "type": "boolean",
      "description": "a daily report (such as a leader conversation's): each run opens with Persona's north-star lines (today's interruptions and availability, the change against yesterday until the same time, the source), which the report starts with. Omit to keep an existing routine's"
    },
    "push": {
      "type": "string",
      "enum": ["every", "asked"],
      "description": "every (default for a new task): each run's answer is pushed to the user; asked: only what a run sends with notify_user, for a task that watches something (it then runs in its own conversation). Omit to keep an existing task's"
    },
    "until": {
      "type": ["string", "null"],
      "description": "optional end time (RFC 3339 or 30m / 2h / 1d). After it, Persona turns the task off and records a skipped run instead of running. Omit to keep an existing task's end time; null clears it. Ignored for a one-off."
    },
    "launcher": {
      "type": "string",
      "description": "this routine's own launcher, instead of Main chat's (a name on the computer where its runs start). Empty clears the override and follows Main chat again. Omit to keep an existing routine's"
    },
    "model": {
      "type": "string",
      "description": "model id for the launcher above; without a launcher it pins the launcher the routine runs on now. Empty uses the launcher's default. Omit to keep an existing routine's"
    },
    "effort": {
      "type": "string",
      "enum": ["low", "medium", "high", "xhigh", "max"],
      "description": "thinking effort for Claude launchers; without a launcher it pins the launcher the routine runs on now. Omit to keep an existing routine's; empty clears it"
    }
  }
}
```
