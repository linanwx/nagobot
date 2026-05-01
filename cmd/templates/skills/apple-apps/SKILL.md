---
name: apple-apps
description: Control Apple apps (Calendar, Reminders, Notes, Contacts, Mail), macOS system settings (dark mode, volume, Wi-Fi, app launch, Finder, sleep/lock), and Spotlight file search via AppleScript and shell commands. Loads as a router — read the relevant reference file for the task at hand instead of pulling everything into context.
tags: [apple, macos, productivity, calendar, reminders, notes, contacts, mail, automation, spotlight]
---
# Apple Apps & macOS

Most commands use `osascript` (AppleScript) or shell utilities. First run per app may trigger a permission dialog — the user must click Allow. Some operations also require Accessibility permission in System Settings > Privacy & Security > Accessibility.

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
| macOS system (dark mode, volume, Wi-Fi, Bluetooth, app launch/quit, Finder, sleep/lock, keyboard) | `{{SKILLDIR}}/system.md` |
| Spotlight file search (`mdfind`, `mdls`, metadata queries) | `{{SKILLDIR}}/spotlight.md` |

Workspace root (substituted, used by `mail.md` for the `mail.py` helper script): `{{WORKSPACE}}`.
