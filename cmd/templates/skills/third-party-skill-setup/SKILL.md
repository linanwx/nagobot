---
name: third-party-skill-setup
description: Use when user wants to set up browser automation tools, install playwright-cli, or prepare dependencies for browser-based skills. Also use when a tool reports missing dependencies that need installation.
tags: [setup, dependencies, playwright, browser]
---
# Third-Party Skill Setup

Guide the user through installing third-party tool dependencies.

## Playwright CLI

### Step 1: Check Prerequisites

Verify Node.js 18+ is available:
```
exec: node --version
```

If not installed, **stop and tell the user** to install Node.js first:
- macOS: `brew install node`
- Linux: `curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash - && sudo apt-get install -y nodejs`
- Or download from https://nodejs.org

### Step 2: Install Playwright CLI

```
exec: npm install -g @playwright/cli@latest
```

### Step 3: Install Skill

```
exec: nagobot skill install --source=skills.sh microsoft/playwright-cli
```

### Step 4: Load and Verify

Load the skill and try reading example.com to confirm it works:
```
use_skill("playwright-cli")
```

Then follow the skill's instructions to open example.com and verify the browser works.
