---
name: third-party-skill-setup
description: Use when user wants to set up browser automation tools, install playwright-cli, or prepare dependencies for browser-based skills. Also use when browser automation reports a missing binary, a wrong Node.js version, or a missing browser, or when a tool reports missing dependencies that need installation.
---
# Third-Party Skill Setup

Guide the user through installing third-party tool dependencies.

## Playwright CLI

### Step 1: Check whether it is already installed

```
exec: playwright-cli --version
```

**If this prints a version number, everything is already in place. Stop here —
do not install, and do not upgrade.** Container images ship playwright-cli and
a matching chromium preinstalled; go straight to Step 5 to verify.

Upgrading is not a harmless "stay current" move here. Each playwright release
is bound to one exact chromium build, so `npm install -g @playwright/cli@latest`
leaves the preinstalled browser unusable and re-downloads ~350MB into a
container layer that the next image upgrade throws away. Only upgrade if the
user asks for it explicitly, and tell them the browser will be re-downloaded.

### Step 2: Check Node.js

Only if Step 1 printed no version.

```
exec: node --version
```

**Node.js 20 or newer is required** — playwright-core has refused to start on
older versions since 1.62. Note that Debian/Ubuntu's default `nodejs` package
is often 18, which passes a casual "is node installed?" check and then fails at
the first browser command. If node is missing or older than 20, **stop and tell
the user** rather than installing anyway:

- macOS: `brew install node`
- Linux: `curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash - && sudo apt-get install -y nodejs`
- Or download from https://nodejs.org

### Step 3: Install Playwright CLI

```
exec: npm install -g @playwright/cli@latest
```

### Step 4: Install the browser

```
exec: playwright-cli install-browser --with-deps chromium
```

Skip this if the machine has Google Chrome or Edge installed — playwright-cli
uses the system browser by default and needs no download.

### Step 5: Install the skill and verify

```
exec: nagobot skill install --source=skills.sh microsoft/playwright-cli --force
```

`--force` refreshes an already-installed copy; without it a stale skill from an
older CLI keeps describing commands that no longer exist.

Then load it and confirm the browser actually opens:
```
use_skill("playwright-cli")
```

Follow the skill's instructions to open example.com and check the page title
comes back.
