# Contacts

AppleScript via `osascript`.

## Search by Name

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

## Search by Phone

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

## Create a Contact

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
