# Reminders

AppleScript via `osascript`. Priority: 0=none, 1=high, 5=medium, 9=low.

## List All Reminder Lists

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

## List Incomplete Reminders

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

## Create a Reminder

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

## Complete a Reminder

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

## Delete a Reminder

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
