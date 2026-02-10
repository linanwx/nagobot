# Channels

nagobot supports multiple communication channels. By default, `nagobot serve` starts all configured channels.

```bash
nagobot serve              # Start all configured channels (default)
nagobot serve --cli        # Start with CLI channel only
nagobot serve --telegram   # Start with Telegram bot only
nagobot serve --web        # Start Web chat channel only
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

## Web

Browser chat UI served over HTTP + WebSocket.

```yaml
channels:
  web:
    addr: "127.0.0.1:8080"
```
