---
name: send-message
description: Send a message to Telegram.
---
# Send Message

## Workflow

Send a message to Telegram:
```
exec: {{WORKSPACE}}/bin/nagobot send --to <chat-id> --text "<message>"
```

Send to admin (default, no --to needed):
```
exec: {{WORKSPACE}}/bin/nagobot send --text "<message>"
```

## Flag Reference

- `--to`: Telegram chat/user ID. Defaults to admin user ID from config.
- `--text`: message content (required). Wrap in double quotes; escape inner quotes with `\"`.

## Notes

- Long messages are automatically split at ~4096 characters.
- Markdown is converted to Telegram HTML format.
