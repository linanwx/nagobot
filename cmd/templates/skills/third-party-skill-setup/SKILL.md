---
name: third-party-skill-setup
description: Use when user wants to set up browser automation tools, install playwright-cli, or prepare dependencies for browser-based skills. Also use when a tool reports missing dependencies that need installation.
tags: [setup, dependencies, playwright, browser]
---
# Third-Party Skill Setup

Guide the user through installing third-party tool dependencies. Currently supports: **playwright-cli**.

---

## Playwright CLI Setup

### Step 1: Check Prerequisites

Verify Node.js and npx are available:
```
exec: node --version && npx --version
```

If either command fails, **stop and tell the user** they need to install Node.js first:
- macOS: `brew install node`
- Linux: `curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash - && sudo apt-get install -y nodejs`
- Or download from https://nodejs.org

Do NOT proceed until Node.js is confirmed working.

### Step 2: Check if Playwright CLI is Already Installed

```
exec: npx @playwright/cli --version 2>/dev/null && echo "ALREADY_INSTALLED" || echo "NOT_INSTALLED"
```

If already installed, skip to Step 4 (sanity test).

### Step 3: Install Playwright CLI and Browsers

Install the CLI globally:
```
exec: npm install -g @playwright/cli
```

Install browser binaries (Chromium, Firefox, WebKit):
```
exec: npx playwright install
```

This downloads ~500MB of browser binaries. It may take a few minutes.

### Step 4: Sanity Test

Verify the installation works by taking a snapshot of a test page:
```
exec: npx @playwright/cli screenshot --browser chromium https://example.com /tmp/playwright-test.png 2>&1 && echo "SUCCESS" || echo "FAILED"
```

If SUCCESS, confirm to the user that playwright-cli is ready. Clean up the test file:
```
exec: rm -f /tmp/playwright-test.png
```

If FAILED, check the error output and troubleshoot:
- Missing browsers → re-run `npx playwright install`
- Permission errors → may need `sudo` for global install
- Display errors → headless mode should work without a display server

### Step 5: Report Result

Tell the user:
- Whether installation succeeded
- Which browsers are available
- That browser-based skills (e.g., web scraping, automated testing) are now ready to use

## Notes

- Playwright browsers are installed to `~/Library/Caches/ms-playwright/` (macOS) or `~/.cache/ms-playwright/` (Linux).
- To update browsers later: `npx playwright install`
- To check installed browsers: `npx playwright install --dry-run`
- All commands run in headless mode by default — no GUI required.
