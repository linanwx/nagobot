---
name: manage-providers
description: Configure LLM provider API keys and model routing. Use when adding a new provider API key, switching which provider/model an agent uses, checking provider status, or troubleshooting model routing issues. IMPORTANT - you must configure a provider's API key before routing any model to it.
---
# Manage Providers & Model Routing

## Provider API Keys

### Add or Update a Provider Key

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name> --api-key <api_key>
```

With custom API base URL:
```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name> --api-key <api_key> --api-base <url>
```

Supported providers: `openai`, `openrouter`, `anthropic`, `deepseek`, `gemini`, `moonshot-cn`, `moonshot-global`, `zhipu-cn`, `zhipu-global`, `minimax-cn`, `minimax-global`.

### List All Provider Key Status

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --list
```

### Check a Specific Provider

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name>
```

### Remove a Provider Key

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name> --clear
```

## Model Routing

### Set Default Provider/Model

Set the default provider and model used by all agents without a model type override:
```
exec: {{WORKSPACE}}/bin/nagobot set-model --default --provider <name> --model <model>
```

### Set Per-Type Routing

Agent templates declare a `specialty` in their frontmatter (e.g. `specialty: chat`, `specialty: toolcall`). Per-type routing maps these specialties to a specific provider and model, overriding the default.

### Set Model Routing

```
exec: {{WORKSPACE}}/bin/nagobot set-model --type <model_type> --provider <name> --model <model>
```

Example: route all "chat" agents to OpenAI GPT-4o:
```
exec: {{WORKSPACE}}/bin/nagobot set-model --type chat --provider openai --model gpt-4o
```

### List Current Routing, Agent Usage, and Available Models

```
exec: {{WORKSPACE}}/bin/nagobot set-model --list
```

### Clear Model Routing (Revert to Default)

```
exec: {{WORKSPACE}}/bin/nagobot set-model --type <model_type> --clear
```

## Notes

- **Dependency**: You must configure a provider's API key BEFORE routing models to it. The `set-model` command will reject routing to a provider without a key.
- Changes take effect immediately (no server restart required).
- Changes persist across server restarts (saved to config.yaml).
- Agents without a `specialty` field in their frontmatter always use the default provider.
