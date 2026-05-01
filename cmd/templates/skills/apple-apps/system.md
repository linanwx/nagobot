---
name: macos-automation
description: Use when the user wants to control macOS system settings (dark mode, volume, brightness, Wi-Fi, Bluetooth, Focus, sleep/lock) or manage apps (launch, quit, bring to front). Does NOT manage app data — use apple-apps for Calendar/Reminders/Notes/Mail content.
tags: [macos, automation, settings, utility]
---
# macOS Automation

Control macOS system settings and applications via shell commands and AppleScript.

## Appearance

Toggle dark mode:
```
exec: osascript -e 'tell application "System Events" to tell appearance preferences to set dark mode to not dark mode'
```

Check current mode:
```
exec: osascript -e 'tell application "System Events" to tell appearance preferences to return dark mode'
```

Set dark mode on:
```
exec: osascript -e 'tell application "System Events" to tell appearance preferences to set dark mode to true'
```

Set light mode:
```
exec: osascript -e 'tell application "System Events" to tell appearance preferences to set dark mode to false'
```

## Volume Control

Get current volume:
```
exec: osascript -e 'output volume of (get volume settings)'
```

Set volume (0–100):
```
exec: osascript -e 'set volume output volume 50'
```

Mute:
```
exec: osascript -e 'set volume output muted true'
```

Unmute:
```
exec: osascript -e 'set volume output muted false'
```

Toggle mute:
```
exec: osascript -e '
set currentMute to output muted of (get volume settings)
set volume output muted (not currentMute)
if currentMute then
    return "Unmuted"
else
    return "Muted"
end if
'
```

## Brightness (Display)

Get brightness (requires `brightness` tool or direct call):
```
exec: osascript -e '
tell application "System Events"
    tell appearance preferences
        return "Use brightness CLI for direct control"
    end tell
end tell
'
```

Via `brightness` CLI (install: `brew install brightness`):
```
exec: brightness -l
```
```
exec: brightness 0.7
```

## Application Management

Launch an app:
```
exec: open -a "APP_NAME"
```

Quit an app:
```
exec: osascript -e 'tell application "APP_NAME" to quit'
```

Force quit:
```
exec: pkill -9 "APP_NAME"
```

List running apps:
```
exec: osascript -e '
tell application "System Events"
    set output to ""
    repeat with proc in (every process whose background only is false)
        set output to output & name of proc & linefeed
    end repeat
    return output
end tell
'
```

Check if app is running:
```
exec: osascript -e '
tell application "System Events"
    return (name of every process) contains "APP_NAME"
end tell
'
```

Bring app to front:
```
exec: osascript -e 'tell application "APP_NAME" to activate'
```

## Finder

Open folder in Finder:
```
exec: open /path/to/folder
```

Reveal file in Finder:
```
exec: open -R /path/to/file
```

Get selected files in Finder:
```
exec: osascript -e '
tell application "Finder"
    set output to ""
    repeat with f in (selection as list)
        set output to output & (POSIX path of (f as alias)) & linefeed
    end repeat
    return output
end tell
'
```

Empty Trash:
```
exec: osascript -e 'tell application "Finder" to empty trash'
```

Get Trash size:
```
exec: du -sh ~/.Trash 2>/dev/null
```

## Wi-Fi

Turn Wi-Fi on:
```
exec: networksetup -setairportpower Wi-Fi on
```

Turn Wi-Fi off:
```
exec: networksetup -setairportpower Wi-Fi off
```

Current Wi-Fi network:
```
exec: networksetup -getairportnetwork Wi-Fi
```

## Bluetooth

Check Bluetooth status:
```
exec: defaults read /Library/Preferences/com.apple.Bluetooth ControllerPowerState
```

Toggle via `blueutil` (install: `brew install blueutil`):
```
exec: blueutil --power toggle
```

## Do Not Disturb / Focus

Check Focus status (macOS 12+):
```
exec: plutil -p ~/Library/DoNotDisturb/DB/Assertions.json 2>/dev/null | head -20
```

## Sleep / Lock / Restart

Lock screen:
```
exec: osascript -e 'tell application "System Events" to keystroke "q" using {command down, control down}'
```

Put display to sleep:
```
exec: pmset displaysleepnow
```

Start screen saver:
```
exec: open -a ScreenSaverEngine
```

## Keyboard Input

Type text:
```
exec: osascript -e 'tell application "System Events" to keystroke "TEXT"'
```

Press key combo:
```
exec: osascript -e 'tell application "System Events" to keystroke "c" using command down'
```

Press return:
```
exec: osascript -e 'tell application "System Events" to key code 36'
```

## Open URLs

```
exec: open "https://example.com"
```

With specific browser:
```
exec: open -a "Safari" "https://example.com"
```

## Notes

- Some operations require Accessibility permission in System Settings > Privacy & Security > Accessibility.
- `System Events` automation requires user approval on first use.
- Wi-Fi interface name may vary (`Wi-Fi`, `en0`). Check with `networksetup -listallhardwareports`.
- Sleep/restart commands may not work in all contexts.
- Combine with `manage-cron` for scheduled automation (e.g., toggle dark mode at sunset).
