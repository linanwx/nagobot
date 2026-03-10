---
name: apple-apps
description: Manage Apple apps (Calendar, Reminders, Notes, Contacts, Mail) via AppleScript.
tags: [apple, macos, productivity, calendar, reminders, notes, contacts, mail]
---
# Apple Apps (AppleScript)

All commands use `osascript`. First run triggers a permission dialog — user must click Allow.

Date format is locale-sensitive. Check locale if date creation fails:
```
exec: defaults read NSGlobalDomain AppleLocale
```

---

## Calendar

### List Today's Events

```
exec: osascript -e '
set today to current date
set hours of today to 0
set minutes of today to 0
set seconds of today to 0
set tomorrow to today + (1 * days)

tell application "Calendar"
    set output to ""
    repeat with cal in calendars
        set theEvents to (every event of cal whose start date ≥ today and start date < tomorrow)
        repeat with evt in theEvents
            set evtStart to start date of evt
            set evtEnd to end date of evt
            set output to output & (time string of evtStart) & " - " & (time string of evtEnd) & " | " & summary of evt & " [" & (name of cal) & "]" & linefeed
        end repeat
    end repeat
    return output
end tell
'
```

### List Upcoming Events (Next N Days)

Replace `7` with desired days:
```
exec: osascript -e '
set today to current date
set hours of today to 0
set minutes of today to 0
set seconds of today to 0
set endDate to today + (7 * days)

tell application "Calendar"
    set output to ""
    repeat with cal in calendars
        set theEvents to (every event of cal whose start date ≥ today and start date < endDate)
        repeat with evt in theEvents
            set output to output & (date string of (start date of evt)) & " " & (time string of (start date of evt)) & " | " & summary of evt & " [" & (name of cal) & "]" & linefeed
        end repeat
    end repeat
    return output
end tell
'
```

### List All Calendars

```
exec: osascript -e '
tell application "Calendar"
    set output to ""
    repeat with cal in calendars
        set output to output & (name of cal) & linefeed
    end repeat
    return output
end tell
'
```

### Create an Event

Replace `CALENDAR_NAME`, `EVENT_TITLE`, dates:
```
exec: osascript -e '
tell application "Calendar"
    tell calendar "CALENDAR_NAME"
        set startDate to date "2026-02-15 10:00:00"
        set endDate to date "2026-02-15 11:00:00"
        make new event with properties {summary:"EVENT_TITLE", start date:startDate, end date:endDate, location:"LOCATION", description:"NOTES"}
    end tell
end tell
return "Event created."
'
```

### Delete an Event

```
exec: osascript -e '
set targetDate to date "2026-02-15 00:00:00"
set nextDay to targetDate + (1 * days)
tell application "Calendar"
    repeat with cal in calendars
        set theEvents to (every event of cal whose summary is "EVENT_TITLE" and start date ≥ targetDate and start date < nextDay)
        repeat with evt in theEvents
            delete evt
        end repeat
    end repeat
end tell
return "Event deleted."
'
```

---

## Reminders

### List All Reminder Lists

```
exec: osascript -e '
tell application "Reminders"
    set output to ""
    repeat with lst in lists
        set incompleteCount to count of (reminders of lst whose completed is false)
        set output to output & name of lst & " (" & incompleteCount & " incomplete)" & linefeed
    end repeat
    return output
end tell
'
```

### List Incomplete Reminders

All lists:
```
exec: osascript -e '
tell application "Reminders"
    set output to ""
    repeat with lst in lists
        set theReminders to (reminders of lst whose completed is false)
        if (count of theReminders) > 0 then
            set output to output & "=== " & name of lst & " ===" & linefeed
            repeat with r in theReminders
                set rDate to ""
                try
                    set rDate to " | Due: " & (due date of r as text)
                end try
                set output to output & "- " & name of r & rDate & linefeed
            end repeat
            set output to output & linefeed
        end if
    end repeat
    if output is "" then return "No incomplete reminders."
    return output
end tell
'
```

### Create a Reminder

```
exec: osascript -e '
tell application "Reminders"
    tell list "LIST_NAME"
        make new reminder with properties {name:"REMINDER_TITLE", body:"NOTES"}
    end tell
end tell
return "Reminder created."
'
```

With due date:
```
exec: osascript -e '
tell application "Reminders"
    tell list "LIST_NAME"
        set dueDate to date "2026-02-15 09:00:00"
        make new reminder with properties {name:"REMINDER_TITLE", due date:dueDate, priority:0}
    end tell
end tell
return "Reminder created."
'
```

Priority: 0=none, 1=high, 5=medium, 9=low.

### Complete a Reminder

```
exec: osascript -e '
tell application "Reminders"
    set theReminders to (reminders whose name is "REMINDER_TITLE" and completed is false)
    if (count of theReminders) > 0 then
        set completed of item 1 of theReminders to true
        return "Completed."
    end if
    return "Not found."
end tell
'
```

### Delete a Reminder

```
exec: osascript -e '
tell application "Reminders"
    set theReminders to (reminders whose name is "REMINDER_TITLE" and completed is false)
    if (count of theReminders) > 0 then
        delete item 1 of theReminders
        return "Deleted."
    end if
    return "Not found."
end tell
'
```

---

## Notes

### List All Folders

```
exec: osascript -e '
tell application "Notes"
    set output to ""
    repeat with f in folders
        set noteCount to count of notes in f
        set output to output & name of f & " (" & noteCount & " notes)" & linefeed
    end repeat
    return output
end tell
'
```

### List Notes in a Folder

```
exec: osascript -e '
tell application "Notes"
    set output to ""
    repeat with n in notes of folder "FOLDER_NAME"
        set output to output & name of n & " | " & (modification date of n as text) & linefeed
    end repeat
    return output
end tell
'
```

### Read a Note

```
exec: osascript -e '
tell application "Notes"
    set theNotes to notes whose name is "NOTE_TITLE"
    if (count of theNotes) > 0 then
        return "Title: " & name of item 1 of theNotes & linefeed & linefeed & plaintext of item 1 of theNotes
    end if
    return "Note not found."
end tell
'
```

### Create a Note

```
exec: osascript -e '
tell application "Notes"
    tell folder "FOLDER_NAME"
        make new note with properties {name:"NOTE_TITLE", body:"NOTE_BODY"}
    end tell
end tell
return "Note created."
'
```

### Search Notes

```
exec: osascript -e '
set searchTerm to "KEYWORD"
tell application "Notes"
    set output to ""
    repeat with f in folders
        repeat with n in notes of f
            if (name of n contains searchTerm) or (plaintext of n contains searchTerm) then
                set output to output & "[" & (name of f) & "] " & name of n & linefeed
            end if
        end repeat
    end repeat
    if output is "" then return "No notes found."
    return output
end tell
'
```

### Delete a Note

```
exec: osascript -e '
tell application "Notes"
    set theNotes to notes whose name is "NOTE_TITLE"
    if (count of theNotes) > 0 then
        delete item 1 of theNotes
        return "Deleted."
    end if
    return "Not found."
end tell
'
```

Default folder: `"Notes"` (or `"备忘录"` in Chinese locale).

---

## Contacts

### Search by Name

```
exec: osascript -e '
tell application "Contacts"
    set output to ""
    set results to (every person whose name contains "SEARCH_NAME")
    repeat with p in results
        set output to output & "Name: " & name of p & linefeed
        try
            set output to output & "Phone: " & (value of phone 1 of p) & linefeed
        end try
        try
            set output to output & "Email: " & (value of email 1 of p) & linefeed
        end try
        set output to output & "---" & linefeed
    end repeat
    if output is "" then return "No contacts found."
    return output
end tell
'
```

### Search by Phone

```
exec: osascript -e '
tell application "Contacts"
    set output to ""
    repeat with p in people
        repeat with ph in phones of p
            if value of ph contains "PHONE_NUMBER" then
                set output to output & name of p & " | " & (value of ph) & linefeed
            end if
        end repeat
    end repeat
    if output is "" then return "Not found."
    return output
end tell
'
```

### Create a Contact

```
exec: osascript -e '
tell application "Contacts"
    set newPerson to make new person with properties {first name:"FIRST_NAME", last name:"LAST_NAME"}
    tell newPerson
        make new phone at end of phones with properties {label:"mobile", value:"PHONE_NUMBER"}
        make new email at end of emails with properties {label:"work", value:"EMAIL"}
    end tell
    save
    return "Contact created."
end tell
'
```

---

## Mail

### List Recent Inbox Messages

```
exec: osascript -e '
tell application "Mail"
    set output to ""
    set msgs to messages 1 thru 10 of inbox
    repeat with msg in msgs
        set isRead to read status of msg
        set readMark to "  "
        if not isRead then set readMark to "● "
        set output to output & readMark & (date string of (date received of msg)) & " " & (time string of (date received of msg)) & " | " & sender of msg & " | " & subject of msg & linefeed
    end repeat
    return output
end tell
'
```

### Count Unread

```
exec: osascript -e '
tell application "Mail"
    return "Unread: " & (count of (messages of inbox whose read status is false))
end tell
'
```

### Read Email by Subject

```
exec: osascript -e '
tell application "Mail"
    set msgs to (messages of inbox whose subject contains "SEARCH_SUBJECT")
    if (count of msgs) > 0 then
        set msg to item 1 of msgs
        return "From: " & sender of msg & linefeed & "Date: " & (date received of msg as text) & linefeed & "Subject: " & subject of msg & linefeed & "---" & linefeed & (content of msg)
    end if
    return "Not found."
end tell
'
```

### Create Draft

```
exec: osascript -e '
tell application "Mail"
    set newMsg to make new outgoing message with properties {subject:"SUBJECT", content:"BODY", visible:true}
    tell newMsg
        make new to recipient at end of to recipients with properties {address:"recipient@example.com"}
    end tell
    return "Draft created."
end tell
'
```

### Check for New Mail

```
exec: osascript -e '
tell application "Mail"
    check for new mail
    return "Checking..."
end tell
'
```

---

## General Notes

- All apps are default macOS apps; no installation needed.
- First run per app triggers a permission dialog.
- iCloud-synced data is accessible if signed in.
- Date parsing is locale-sensitive (see locale check at top).
