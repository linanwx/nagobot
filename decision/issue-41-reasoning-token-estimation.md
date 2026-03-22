# Issue #41: Token 估计不精确 — ReasoningDetails 粗糙计算

## 概述

`thread/context_pressure.go:90-92` 中，`ReasoningDetails`（`json.RawMessage` 类型）的 token 估算用 `len(message.ReasoningDetails) / 3`，即按字节长度除以 3 粗算。偏差可达 20-30%，影响 Context Pressure Hook 和 Tier 2 压缩的触发时机。

## 现状分析

- `ReasoningDetails` 是 `json.RawMessage`（即 `[]byte`），存储各 provider 的 opaque reasoning 数据（Gemini thought_signature、Anthropic thinking blocks、OpenRouter reasoning_details 等）
- `len()` 返回 JSON 字节数，不是字符数也不是 token 数
- 各 provider 在 API 响应中**已经返回了精确的 reasoning tokens 数**（`CompletionTokensDetails.ReasoningTokens`），但目前只是日志打印，没有存入 `Usage` 结构体
- `Usage` 结构体当前只有 `PromptTokens`、`CompletionTokens`、`TotalTokens`、`CachedTokens`，没有 reasoning 相关字段

## 需要决策的点

### 1. 方案选择：provider 回传精确值 vs 改进估算算法

- **方案 A（推荐）：provider 回传精确 reasoning token 数**
  - 在 `Usage` 结构体新增 `ReasoningTokens int` 字段
  - 6 个 provider 都已解析 `reasoningTokens`（目前只 log），改为同时填充 `Usage.ReasoningTokens`
  - `EstimateMessageTokens()` 优先使用精确值，fallback 到估算
  - 优点：零偏差，改动集中在 provider 层，估算逻辑只需加一个 if 判断
  - 缺点：精确值只在首次从 API 返回时可用，从 session.jsonl 反序列化的历史消息没有此字段（除非持久化）

- **方案 B：改用 tiktoken 估算**
  - 先 `json.Unmarshal` 解析出文本部分，再用 tiktoken 估算
  - 优点：不依赖 provider，对历史消息也适用
  - 缺点：需要知道各 provider ReasoningDetails 的 JSON 结构才能提取纯文本（Gemini 是 parts 数组、Anthropic 是 thinking blocks、OpenRouter 又不同），增加耦合；tiktoken 本身对中文也不够精确

- **方案 C：仅调整估算系数**
  - `len() / 3` 改为根据 JSON 编码开销调整（如 `len() / 4`）
  - 优点：最小改动
  - 缺点：仍然是猜测，不同 provider 的 JSON 结构差异大

**建议**：方案 A。provider 已经有精确数据，只是没存下来。

### 2. 是否在 Message 上持久化 ReasoningTokens

方案 A 中，精确值来自 API 响应的 `Usage`，但 `Usage` 不存入 `session.jsonl`——只有 `Message` 被持久化。

- **选项 A：在 Message 上新增 `ReasoningTokens int` 字段**
  - `runner.go` 构造 assistant message 时从 `Response.Usage` 填充
  - `EstimateMessageTokens()` 发现 `ReasoningTokens > 0` 直接用，跳过估算
  - 历史消息（字段缺失）自动 fallback 到 `len() / 3` 估算
  - 优点：一劳永逸，重启后也有精确值
  - 缺点：Message 结构体多一个字段，session.jsonl 体积略增

- **选项 B：不持久化，仅内存中使用**
  - `Usage.ReasoningTokens` 只在当前 turn 的 token 计算中使用
  - 重启后、历史消息仍用 `len() / 3`
  - 优点：不改 Message 结构
  - 缺点：精确值只有当前 turn 有效，重启后回到粗估

**建议**：选项 A。Message 字段多一个 int 影响极小，但收益是所有未来消息永久精确。

### 3. CompletionTokens 是否已包含 ReasoningTokens

各 provider 的 `CompletionTokens` 是否已经包含 reasoning tokens？这影响 `EstimateMessageTokens` 的计算逻辑——如果精确 `ReasoningTokens` 已被 `CompletionTokens` 包含，就不能重复计算。

但 `EstimateMessageTokens` 是对**单条 Message 内容**做估算，不涉及 `Usage`。它估算的是 `ReasoningDetails` 这块 JSON 数据占多少 token——这个值和 `CompletionTokens` 无关。

**建议**：无需额外处理。`EstimateMessageTokens` 是内容级估算，`Usage` 是 API 级计数，两个层面独立。只需确保 `ReasoningTokens` 字段在 Message 上赋值正确即可。

### 4. 改动范围确认

预计改动文件：

| 文件 | 改动 |
|------|------|
| `provider/provider.go` | `Usage` 新增 `ReasoningTokens`；`Message` 新增 `ReasoningTokens` |
| `provider/{openrouter,deepseek,moonshot,zhipu,minimax,anthropic,gemini}.go` | 填充 `Usage.ReasoningTokens`（已有解析逻辑，加一行赋值） |
| `thread/runner.go` | 构造 assistant message 时从 `resp.Usage.ReasoningTokens` 填充 `Message.ReasoningTokens` |
| `thread/context_pressure.go` | `EstimateMessageTokens` 优先用 `message.ReasoningTokens`，fallback `len(ReasoningDetails)/3` |

**需确认**：是否还有其他消费 `EstimateMessageTokens` 的地方需要关注？
