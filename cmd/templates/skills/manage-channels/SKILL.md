---
name: manage-channels
description: Configure messaging channels (Telegram, Discord, Feishu). Use when the user wants to set up a bot, change bot tokens, manage allowed users/chats, or troubleshoot channel connectivity.
---
# Manage Channels

## Telegram

### View Status

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram
```

### Set Bot Token

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --token <BOT_TOKEN>
```

Get token from [@BotFather](https://t.me/BotFather) (`/newbot` command).

### Set Allowed User/Chat IDs

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --allowed "123456,789012"
```

Allow all (no restriction):
```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --allowed ""
```

### Clear Telegram Config

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --clear
```

**Hot-reload**: Token and allowed ID changes are detected every 10 seconds. No restart needed.
