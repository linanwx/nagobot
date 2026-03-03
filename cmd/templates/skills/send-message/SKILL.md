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

## Flag Reference

- `--to`: Telegram chat/user ID (required).
- `--text`: message content (required). Wrap in double quotes; escape inner quotes with `\"`.

## Notes

- Long messages are automatically split at ~4096 characters.
- Markdown is converted to Telegram HTML format.
