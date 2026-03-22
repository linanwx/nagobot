# Issue #44: 废弃 contextWarnRatio，改用 contextWarnToken

## 概述

当前 context pressure 使用比例阈值 `contextWarnRatio`（默认 0.8），但压缩所需的剩余空间是固定的，不随模型 context window 大小缩放。对 1M 模型来说，80% 触发意味着剩余 200K token 才压缩，白白浪费大量可用空间。

改为固定 token 阈值 `contextWarnToken`，当 `已用 > contextWindow - contextWarnToken` 时触发。

## 改动范围

- `config/config.go`: 字段 `ContextWarnRatio` → `ContextWarnToken`
- `config/defaults.go`: 默认值、校验逻辑
- `config/provider.go`: getter 函数
- `thread/types.go`: `ThreadConfig.ContextWarnRatio` → `ContextWarnToken`
- `thread/context_pressure.go`: `contextBudget()` 返回值、`contextPressureHook` 阈值计算、`PressureStatus` 函数签名
- `thread/run.go`: 引用处同步
- `thread/hooks.go`: `turnContext.ContextWarnRatio` → `ContextWarnToken`
- `thread/compress.go`: Tier 2 的 `tier2TokenRatio` 是否同步改
- `channel/web.go`: `contextBudgetFn` 返回值
- `cmd/thread_runtime.go`: 构建 ThreadConfig
- `cmd/session_stats.go`: 使用 warnRatio 的地方

## 需要决策的点

### 1. contextWarnToken 默认值

Issue 建议 30000。需要确认：

- **30000** 对小模型（128K）是否合理？128K - 30K = 98K 触发，剩余 23%，与当前 80% 触发点接近
- **30000** 对 1M 模型意味着 970K 才触发，最大化利用空间
- 是否需要更保守（如 50000）以留出更多缓冲？

**建议**: 30000，与当前 128K 模型下的行为基本一致。

### 2. tier2TokenRatio 是否同步改为固定 token

当前 Tier 2 用 `tier2TokenRatio = 0.65`，同样存在大模型下过早触发的问题（1M 模型在 650K 就触发 AI 压缩）。

- **选项 A**: 同步改为 `tier2WarnToken`，例如 60000（Tier 2 比 Tier 3 早触发）
- **选项 B**: 本次只改 contextWarnRatio，tier2 单独处理
- **选项 C**: tier2 改为基于 contextWarnToken 的偏移（如 `contextWarnToken * 2`）

**建议**: 选项 A，一并修改，保持一致性。

### 3. 旧配置兼容

用户的 `config.yaml` 中可能已有 `contextWarnRatio: 0.85` 这样的自定义值。

- **选项 A**: 代码中同时识别旧字段，自动转换（`contextWarnRatio * contextWindow` 近似转为 token 数），加 deprecation 日志
- **选项 B**: 直接删除旧字段，`applyDefaults` 中发现旧字段打 warning，使用新默认值
- **选项 C**: 忽略兼容，旧字段被 YAML 解析忽略即可

**建议**: 选项 C。nagobot 用户量极小，且 ratio 本身很少被自定义，直接切换即可。

### 4. PressureStatus 函数签名变更

当前 `PressureStatus(usageRatio, warnRatio float64)` 基于比例。改为固定 token 后：

- **选项 A**: 改为 `PressureStatus(usedTokens, contextWindow, warnToken int)`，内部计算
- **选项 B**: 保持比例接口，调用方自行计算 `threshold = contextWindow - warnToken` 后传比例

**建议**: 选项 A，让函数签名反映新的语义。

### 5. Web channel contextBudgetFn 返回值

`channel/web.go` 的 `contextBudgetFn` 当前返回 `(int, float64, bool)` 即 `(tokens, warnRatio, ok)`。需要决定新签名。

**建议**: 改为 `(contextWindow int, warnToken int, ok bool)`，与新语义对齐。

# Feedback 

1. 默认值：先按照 80 计算，如果超过50000就按照50000，这样小大的都兼顾了
2. tier2TokenRatio 是否同步改为固定：按照Tier 3 的0.8比例来计算吧 之前 0.8*0.8 = 0.64, 差不多比例
3. 旧配置兼容: 不再允许用户自定义配置压缩比例, 直接忽略即可，没啥人用
4. 让函数签名反映新的语义，尽管改动大 
5. 前端同样需要对齐

---

# 第二轮决策（基于 Feedback）

综合 feedback，核心设计变更：**不再是"把 ratio 字段换成 token 字段"，而是移除用户可配置字段，改为纯运行时计算。**

## 已确定（无需决策）

| # | Feedback | 实施方式 |
|---|----------|---------|
| 1 | 默认值按 80% 算，超 50000 按 50000 | `warnToken = min(contextWindow * 0.2, 50000)` |
| 3 | 不允许用户自定义，直接忽略旧字段 | 删除 `config.ContextWarnRatio` 字段，不加新字段 |
| 4 | 函数签名反映新语义 | `PressureStatus(usedTokens, contextWindow, warnToken int)` |
| 5 | 前端对齐 | `contextBudgetFn` 返回 `(contextWindow, warnToken int, ok bool)` |

## 需要确认的决策

### 6. tier2WarnToken 精确公式

Feedback: "按照 Tier 3 的 0.8 比例来计算，之前 0.8×0.8=0.64"。

旧系统：Tier 3 剩余 20%，Tier 2 剩余 36%（1-0.64）。Tier2/Tier3 剩余比 = 1.8。

**建议**: `tier2WarnToken = int(warnToken * 1.8)`，等价于保持旧的剩余空间比例关系。

验算：
- 128K：warnToken=25600，tier2=46080（剩 36%，≈旧 0.65 ✓）
- 256K：warnToken=50000，tier2=90000（剩 35% ✓）
- 1M：warnToken=50000，tier2=90000（剩 9%，大模型不过早压缩 ✓）

### 7. contextBudget() 返回值变更

当前 `contextBudget() (tokens int, warnRatio float64)` 同时被 `run.go` 和 `manager.go` 调用。

因为 warnToken 现在从 contextWindow 计算得出，不再需要从 config 传入。

**建议**: 改为 `contextBudget() (contextWindow int, warnToken int)`，内部计算 `min(contextWindow*0.2, 50000)`。调用方不再接触 ratio。

### 8. compress.go 中 tier2TokenRatio 常量处理

当前 `tier2TokenRatio = 0.65` 是 `types.go` 中的包级常量，在 `compress.go:124` 使用：`threshold := int(float64(effectiveWindow) * tier2TokenRatio)`。

**建议**: 删除 `tier2TokenRatio` 常量，改为调用同一个计算函数。新建 `func contextThresholds(contextWindow int) (warnToken, tier2Token int)` 统一计算，contextPressureHook 和 tryTier2Compress 共用。

## 改动清单（更新）

删除/修改：
- `config/config.go`: 删除 `ContextWarnRatio` 字段
- `config/defaults.go`: 删除相关默认值和校验
- `config/provider.go`: 删除 getter
- `thread/types.go`: 删除 `ThreadConfig.ContextWarnRatio`、删除 `tier2TokenRatio` 常量
- `cmd/thread_runtime.go`: 删除构建 `ContextWarnRatio` 的代码

新增/修改：
- `thread/context_pressure.go`: 新增 `contextThresholds(contextWindow int) (warnToken, tier2Token int)`；重写 `contextBudget()` 返回 `(contextWindow, warnToken int)`；重写 `PressureStatus`
- `thread/compress.go`: `tryTier2Compress` 改用 `contextThresholds()` 计算 tier2Token
- `thread/run.go`: 适配新 `contextBudget()` 返回值
- `thread/hooks.go`: `turnContext` 中 `ContextWarnRatio` → `ContextWarnToken int`
- `channel/web.go`: `contextBudgetFn` 签名改为 `(contextWindow, warnToken int, ok bool)`
- `cmd/session_stats.go`: 适配新签名

如果 6/7/8 无异议，所有实施细节已明确，可转入实施阶段。

# Feedback

6. 可以按照 tier2WarnToken = int(warnToken * 1.8) 记得提取全局变量
7. 调用方不再接触 ratio
8. 改为返回结构体，避免返回结构多次修改，方便扩展

再看下是否还有需要决策的点

---

# 第三轮决策（最终审查）

逐一验证 feedback #6/#7/#8，结合代码调用链检查是否还有遗漏。

## Feedback 确认落实

| # | Feedback | 落实 |
|---|----------|------|
| 6 | `tier2WarnToken = int(warnToken * 1.8)`，提取全局变量 | 新增 `tier2Multiplier = 1.8` 包级常量（替代旧 `tier2TokenRatio`） |
| 7 | 调用方不再接触 ratio | `contextBudget()` 返回结构体，调用方只用字段 |
| 8 | 返回结构体，方便扩展 | 新增 `ContextThresholds` 结构体 |

## 结构体定义

```go
type ContextThresholds struct {
    ContextWindow int // effective context window (tokens)
    WarnToken     int // Tier 3: context pressure threshold (remaining tokens)
    Tier2Token    int // Tier 2: AI compression threshold (remaining tokens)
}
```

`contextThresholds(contextWindow int) ContextThresholds` 计算逻辑：
- `WarnToken = min(contextWindow/5, 50000)`
- `Tier2Token = int(float64(WarnToken) * tier2Multiplier)` where `tier2Multiplier = 1.8`

`contextBudget() ContextThresholds` 内部获取 contextWindow 后调用 `contextThresholds()`。

## 无决策需要做

审查了所有调用路径（`run.go`、`compress.go`、`hooks.go`、`web.go`、`session_stats.go`），以下均为机械适配，无歧义：

1. **`turnContext`**：`ContextWarnRatio float64` → `WarnToken int`（从 `ContextThresholds.WarnToken` 赋值）
2. **`contextPressureHook`**：`threshold = contextWindow * warnRatio` → `threshold = contextWindow - warnToken`
3. **`PressureStatus`**：新签名 `PressureStatus(usedTokens, contextWindow, warnToken int)`。"warning" 级别旧逻辑 `usageRatio >= warnRatio*0.8` 等价于 `remaining < warnToken/0.8`，新系统直接用 `remaining < tier2Token`（tier2Token ≈ warnToken*1.8，与旧 0.8*0.8=0.64 一致）
4. **`contextBudgetFn`**：闭包签名改为 `func(string) (int, int, bool)` 返回 `(contextWindow, warnToken, ok)`，不引入跨包结构体
5. **`session_stats.go`**：`cfg.GetContextWarnRatio()` 删除，改为 `thread.ComputeContextThresholds(contextWindow)` 获取 warnToken
6. **JSON API**：`sessionStatsResponse.WarnRatio float64` → `WarnToken int`，前端同步更新

**建议：无决策需要我做。所有实施细节已明确，可转入实施阶段。**
