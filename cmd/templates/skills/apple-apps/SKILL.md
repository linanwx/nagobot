---
name: apple-apps
description: Manage Apple apps (Calendar, Reminders, Notes, Contacts, Mail) via AppleScript. Loads as a router — read the relevant reference file for the task at hand instead of pulling all five apps into context.
tags: [apple, macos, productivity, calendar, reminders, notes, contacts, mail]
---
# Apple Apps (AppleScript)

All commands use `osascript`. First run per app triggers a permission dialog — the user must click Allow.

Date strings are locale-sensitive. If date creation fails, check locale:
```
exec: defaults read NSGlobalDomain AppleLocale
```

iCloud-synced data is accessible if signed in. All apps are default macOS apps; no installation needed.

## Pick the right reference file

Read only the file for the task at hand — do NOT read all of them.

| Task | Reference file |
|---|---|
| Calendar (events, scheduling, meetings) | `{{SKILLDIR}}/calendar.md` |
| Reminders (todos, lists, due dates) | `{{SKILLDIR}}/reminders.md` |
| Notes (free-form text, folders, search) | `{{SKILLDIR}}/notes.md` |
| Contacts (phone book, search, create) | `{{SKILLDIR}}/contacts.md` |
| Mail (inbox search, drafts, unread count) | `{{SKILLDIR}}/mail.md` |

Workspace root (substituted, used by `mail.md` for the `mail.py` helper script): `{{WORKSPACE}}`.
