# Calendar

AppleScript via `osascript`. Date strings are locale-sensitive.

## List Today's Events

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

## List Upcoming Events (Next N Days)

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

## List All Calendars

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

## Create an Event

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

## Delete an Event

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
