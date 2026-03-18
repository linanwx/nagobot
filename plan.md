# Plan: Discord & Telegram 消息回复内容解析

## 问题

当用户在 Discord/Telegram 中回复某条消息时，bot 完全看不到被引用消息的内容。两个库都直接提供了被引用消息的完整对象，但当前代码只取了 ID 用于路由回复，内容被丢弃。

## 方案

在 channel 层提取被引用消息的内容，通过 `Metadata["reply_context"]` 传递给 Dispatcher，由 `preprocessMessage()` 拼接到用户消息前面。

**为什么用 Metadata 而不是改 Message struct？**
- `Metadata` 就是为 channel-specific 扩展数据设计的
- 不需要改动 `Message` 结构体，影响面最小
- 与现有的 `media_summary` 模式一致

## 改动

### 1. Discord: `channel/discord.go` ~L355

**现状：**
```go
if m.MessageReference != nil {
    msg.ReplyTo = m.MessageReference.MessageID
}
```

**改为：**
```go
if m.MessageReference != nil {
    msg.ReplyTo = m.MessageReference.MessageID
    if ref := m.ReferencedMessage; ref != nil && ref.Content != "" {
        author := ref.Author.Username
        if ref.Author.GlobalName != "" {
            author = ref.Author.GlobalName
        }
        metadata["reply_context"] = "[Reply to " + author + "]: " + ref.Content
    }
}
```

### 2. Telegram: `channel/telegram_update.go` ~L179

**现状：**
```go
if msg.ReplyToMessage != nil {
    channelMsg.ReplyTo = strconv.Itoa(msg.ReplyToMessage.ID)
}
```

**改为：**
```go
if msg.ReplyToMessage != nil {
    channelMsg.ReplyTo = strconv.Itoa(msg.ReplyToMessage.ID)
    if replyText := extractText(msg.ReplyToMessage); replyText != "" {
        replyAuthor := extractUsername(msg.ReplyToMessage.From)
        metadata["reply_context"] = "[Reply to " + replyAuthor + "]: " + replyText
    }
}
```

需要增加两个小辅助函数（提取 Telegram 回复消息的 text 和 username），避免在主流程中写太多细节逻辑。Telegram 的 text 可能在 `.Text` 或 `.Caption` 中。

### 3. Dispatcher: `cmd/dispatcher.go` `preprocessMessage()` ~L280

在 `media_summary` 处理之后、sender name 处理之前，加入 reply_context：

```go
if rc := msg.Metadata["reply_context"]; rc != "" {
    text = rc + "\n\n" + text
}
```

### 4. 引用内容截断

被引用消息可能很长，需要截断以避免占用过多 token。使用已有的 `truncate()` 函数（`cmd/dispatcher.go:306`），限制在 500 字符。

在 Discord 和 Telegram 中构造 `reply_context` 时对内容做截断：
```go
content := truncate(ref.Content, 500)
```

因为 `truncate` 在 `cmd/dispatcher.go` 中而非 `channel` 包中，可以在 channel 层内联一个简单截断，或在 dispatcher 层的 `preprocessMessage` 中截断。**选择在 dispatcher 层截断**，保持 channel 层的数据原始性。

### 5. 测试

- 在 `channel/discord_test.go` 中增加测试用例，验证 reply_context 的提取
- 在 `cmd/dispatcher.go` 对应增加 `preprocessMessage` 的测试

**注意**：Discord handler (`handleMessageCreate`) 依赖 discordgo session，无法直接单元测试消息构建逻辑。因此将 reply_context 构建逻辑提取为纯函数 `buildReplyContext(author, content string) string` 便于测试。Telegram 同理。

实际测试策略：
- 测试 `preprocessMessage` 正确拼接 `reply_context`（这是最核心的行为）
- 测试截断逻辑

## 不改动的部分

- `channel.Message` 结构体 — 不变
- `WakeMessage` 结构体 — 不变
- `thread/run.go` — 不变
- `thread/wake.go` — 不变
- Feishu / Web / CLI — 本次不涉及（它们各自的回复机制不同，后续可扩展）

## 改动文件清单

| 文件 | 改动 |
|------|------|
| `channel/discord.go` | 提取 `ReferencedMessage` 内容写入 metadata |
| `channel/telegram_update.go` | 提取 `ReplyToMessage` 内容写入 metadata |
| `cmd/dispatcher.go` | `preprocessMessage()` 拼接 reply_context |
| `channel/discord_test.go` | 新增 reply context 相关测试 |

## 风险评估

- **低风险**：所有改动都在数据提取/拼接层，不影响路由、session、线程调度
- **向后兼容**：没有 reply 时 metadata 中不会有 `reply_context` key，行为与现在完全一致
- **性能**：无额外 API 调用，数据已在事件对象中
