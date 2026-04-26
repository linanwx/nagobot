# Notes

AppleScript via `osascript`. Default folder: `"Notes"` (or `"备忘录"` in Chinese locale).

## List All Folders

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

## List Notes in a Folder

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

## Read a Note

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

## Create a Note

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

## Search Notes

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

## Delete a Note

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
