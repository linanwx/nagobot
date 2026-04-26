# Mail

Two layers:
- **AppleScript** for send-side (create draft) and small counters — simple and native.
- **`mail.py` (disk-first Python script)** for search, read, and stats — reads
  `.emlx` files directly under `~/Library/Mail/V*/`, bypasses AppleScript
  entirely. Tested at 20k messages: full-body search ~8s, subject search ~1s;
  AppleScript equivalents timeout or take 70s+.

> **Note on paths:** This file references `<workspace>/scripts/mail.py`. The
> entry skill (`apple-apps`) shows the absolute path of `<workspace>` — use
> that value when running these commands.

**Permission prerequisite (macOS):** Both layers require granting permissions
to the nagobot binary. If `mail.py` returns empty results / 0 files even
though `~/Library/Mail/V10/` exists in your shell, the daemon lacks Full Disk
Access:

> System Settings → Privacy & Security → Full Disk Access → enable for
> `~/.local/bin/nagobot` (or your nagobot install path).

After granting, restart the service. AppleScript Mail commands additionally
require Automation permission (granted on first prompt).

| Task | Use |
|------|-----|
| List recent N messages | `mail.py recent --limit N` |
| Search subject/from/body | `mail.py search <kw> --in {subject,from,body,all}` |
| Read a specific email | `mail.py read <emlx_path>` |
| Mailbox stats | `mail.py stats` |
| Count unread (quick) | AppleScript `unread count of inbox` |
| Create draft | AppleScript `make new outgoing message` |
| Trigger mail fetch | AppleScript `check for new mail` |

## Search / Read via `mail.py`

```
exec: <workspace>/scripts/mail.py stats
```

```
exec: <workspace>/scripts/mail.py recent --limit 20
```

Output (TSV): `mtime \t from \t subject \t path`. Use `path` as input to `read`.

```
exec: <workspace>/scripts/mail.py search "visa" --in all --limit 10
```

Scopes:
- `--in subject` (default): fastest, scans only headers (first 8KB per file)
- `--in from`: header scan
- `--in body`: full-file read, ~8s for 20k inbox worst case
- `--in all`: subject → from → body short-circuit

Scope to a mailbox (substring match on path):
```
exec: <workspace>/scripts/mail.py search "invoice" --mailbox Spam --limit 5
```

Read one email once you have the `.emlx` path:
```
exec: <workspace>/scripts/mail.py read "/Users/linan/Library/Mail/V10/.../20872.emlx"
```

## Count Unread (AppleScript — tracked counter, instant)

```
exec: osascript -e '
tell application "Mail"
    return "Unread: " & (unread count of inbox)
end tell
'
```

**DO NOT use `count of (messages of inbox whose read status is false)`** —
`read status` is a non-indexed boolean, forcing a full scan. Measured ~71s on
a 17k-message inbox, commonly trips Mail's internal AppleEvent timeout. The
tracked `unread count` may drift slightly from a scan but is the right
primitive for a quick check.

## Create Draft (AppleScript)

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

`visible:true` opens the compose window for user confirmation (recommended
default). The user clicks Send — the script never sends on its own.

## Check for New Mail (AppleScript)

```
exec: osascript -e '
tell application "Mail"
    check for new mail
    return "Checking..."
end tell
'
```
