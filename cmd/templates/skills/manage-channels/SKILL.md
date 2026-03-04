---
name: manage-channels
description: Configure messaging channel settings (Telegram token, allowed users). Use when the user wants to set up, modify, or check the status of their Telegram bot connection, change who is allowed to interact with the bot, or troubleshoot channel connectivity issues.
---
# Manage Channels

## Telegram

### View Current Status

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram
```

### Set Bot Token

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --token <BOT_TOKEN>
```

Get the token from [@BotFather](https://t.me/BotFather) on Telegram (`/newbot` command).

### Set Allowed User/Chat IDs

Restrict who can interact with the bot. Provide a comma-separated list of Telegram user or chat IDs:

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --allowed "123456,789012"
```

To allow all users (no restriction):

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --allowed ""
```

### Set Token and Allowed IDs Together

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --token <BOT_TOKEN> --allowed "123456"
```

### Clear All Telegram Configuration

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --clear
```

## Notes

- **Hot-reload**: Token changes are detected every 10 seconds. Adding a token auto-starts the Telegram channel without restarting the server.
- **AllowedIDs hot-reload**: Changes to allowed IDs take effect within 10 seconds on a running server.
- Changes persist across server restarts (saved to config.yaml).
- When allowed IDs is empty, all users can interact with the bot.
- To find a user's Telegram ID, ask them to message [@userinfobot](https://t.me/userinfobot).
