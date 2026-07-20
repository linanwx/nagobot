---
name: web-login
description: Mint a one-time login link for the nagobot web UI. Use when the user asks for web access, a login link, wants to open the dashboard from a browser, lost their passkey, or wants to invite someone to the web UI.
---
# Web Login Link

The web UI requires login. Access is bootstrapped with a **one-time link**
(30 minutes, single use) and then secured by a passkey the browser registers.

## Mint a link

```
exec: {{WORKSPACE}}/bin/nagobot login-link
```

Output is the URL plus its expiry time. Send it to the user who asked.

## Rules

- **Deliver the link only to the person who asked, in the conversation where
  they asked.** Never post a login link into a group/public channel — if the
  request came from a group, tell the user to ask in a DM instead.
- One link per person: it is single-use, so two people cannot share one.
  Mint a fresh link for each person who needs access.
- Do not mint links proactively. Only on explicit request.

## What the user does with it

Opening the link shows everyone the system knows — existing web accounts
AND chat identities (Discord/Telegram/... users who have talked to the
bot) — plus a create-new option. Explain briefly when handing it over:

- **Pick your chat identity** (e.g. your Discord name): creates the web
  account around it, username prefilled, then register a passkey.
- **Pick your web account**: the lost-passkey recovery path — claim your
  existing username, register a new passkey.
- **Create a new user**: for someone the bot has never seen.

After setup, the passkey is the durable login for that browser — the link is
spent. A user who loses their passkey simply asks for a new link and claims
their existing username again.

## Troubleshooting

- Link expired / already used → mint a new one.
- "Passkeys are not available" in the browser → the page is not a secure
  context. Passkeys need `http://localhost:...` or HTTPS on a real hostname;
  a raw `http://<ip>:<port>` origin cannot use them.
