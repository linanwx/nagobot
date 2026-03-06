---
name: monitoring
description: Check LLM provider account balances, query performance metrics, and view compression stats. Use when the user asks about provider credits/balance, response times, token usage, error rates, session compression history, or wants to see how different models/agents/sessions are performing over time windows (1h, 1d, 7d).
---
# Monitoring

## Check Provider Balances

Query account balance/credits for configured providers. Currently supports OpenRouter and DeepSeek.

Check all providers:
```
exec: {{WORKSPACE}}/bin/nagobot monitor --balance
```

Check a specific provider:
```
exec: {{WORKSPACE}}/bin/nagobot monitor --balance --provider openrouter
exec: {{WORKSPACE}}/bin/nagobot monitor --balance --provider deepseek
```

## Query Performance Metrics

View aggregated performance metrics grouped by provider, model, agent, and session.

Last hour:
```
exec: {{WORKSPACE}}/bin/nagobot monitor --metrics --window 1h
```

Last 24 hours (default):
```
exec: {{WORKSPACE}}/bin/nagobot monitor --metrics --window 1d
```

Last 7 days:
```
exec: {{WORKSPACE}}/bin/nagobot monitor --metrics --window 7d
```

Both at once:
```
exec: {{WORKSPACE}}/bin/nagobot monitor --balance --metrics --window 1d
```

## Compression Stats

View session compression history — how often sessions get compressed, message counts, token estimates, and longest messages.

Last 24 hours:
```
exec: {{WORKSPACE}}/bin/nagobot monitor --compression
```

Last 7 days:
```
exec: {{WORKSPACE}}/bin/nagobot monitor --compression --window 7d
```

### What metrics are tracked

- **Duration**: Total turn time from receiving message to sending response (includes all tool calls)
- **Tokens**: Prompt tokens, completion tokens, total tokens per turn
- **Iterations**: Number of LLM call iterations in the agentic loop
- **Tool calls**: Number of tools invoked per turn
- **Error rate**: Percentage of turns that resulted in errors
- **Grouping**: All metrics are broken down by provider, model, agent name, and session key
