# Channels

nagobot supports multiple communication channels. By default, `nagobot serve` starts all configured channels.

```bash
nagobot serve              # Start all configured channels (default)
nagobot serve --cli        # Start with CLI channel only
nagobot serve --telegram   # Start with Telegram bot only
nagobot serve --discord    # Start with Discord bot only
nagobot serve --web        # Start Web chat channel only
```

## Session Agents

Use `sessionAgents` to assign a specific agent to any session. The key is the full session key:

```yaml
channels:
  sessionAgents:
    "telegram:1234567890": "assistant"         # Telegram user → agent
    "discord:555666777888": "gamemaster"        # Discord channel → agent
    "cli": "default"                            # CLI session → agent
```

## Telegram

The interactive `nagobot onboard` wizard can configure Telegram for you. To configure manually, edit `~/.nagobot/config.yaml`:

```yaml
channels:
  adminUserID: "1234567890"
  telegram:
    token: "1234567890:AA***************"
    allowedIds:
      - 1234567890
```

- **token**: Open [@BotFather](https://t.me/BotFather) on Telegram, run `/newbot`, and paste the generated token here.
- **adminUserID**: Open [@userinfobot](https://t.me/userinfobot) on Telegram, send `/start`, and paste your numeric user ID here. Messages from this ID share the `main` session.
- **allowedIds**: Open [@userinfobot](https://t.me/userinfobot) for each user, paste their numeric IDs here. Leave empty to allow all.

## Discord

Discord bot channel for DMs and guild text channels. Group chats share a session per channel, making it ideal for multi-player scenarios (TRPG, murder mystery, etc.).

### Setup

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications) and create a new application.
2. Navigate to **Bot** and click **Reset Token** to generate a bot token.
3. Under **Privileged Gateway Intents**, enable **MESSAGE CONTENT INTENT**.
4. Navigate to **OAuth2 → URL Generator**, select the `bot` scope with `Send Messages` and `Add Reactions` permissions, then use the generated URL to invite the bot to your server.

### Configuration

The interactive `nagobot onboard` wizard can configure Discord for you. To configure manually, edit `~/.nagobot/config.yaml`:

```yaml
channels:
  discord:
    token: "your-bot-token"
    allowedGuildIds:
      - "1234567890"       # guild IDs to allow (empty = allow all)
    allowedUserIds:
      - "9876543210"       # user IDs to allow (empty = allow all)
```

The token can also be set via the `DISCORD_BOT_TOKEN` environment variable, which takes precedence over the config file.

To assign a game agent to a specific channel, see [Session Agents](#session-agents) above.

## Web

Browser chat UI served over HTTP + WebSocket.

```yaml
channels:
  web:
    addr: "127.0.0.1:8080"
```
