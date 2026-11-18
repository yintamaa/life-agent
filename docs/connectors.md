# 渠道接入说明

每个连接器只负责“把消息搬进来 / 把回复发出去”，实现 `internal/connector.Connector`。新增渠道 = 新写一个连接器，UI 和分拣不用动。

---

## 飞书（已在真实账号上验证）

**方式**：驱动官方 `lark-cli`，以**你本人（user）身份**调用 OpenAPI。登录与 token 刷新由 lark-cli 负责，本项目不碰凭据。

```bash
lark-cli auth login --scope "im:chat:read im:message:readonly im:message.p2p_msg:get_as_user im:message.group_msg:get_as_user im:chat.members:read contact:user.base:readonly"
# 需要在平台内回复时再加 im:message，并把 feishu.send 设为 true
```

**同步策略**：按活跃度列出前 `chat_limit` 个会话；每轮只拉头部 `hot_chats` 个和“排名上升”的会话（有新消息的会话会跳到顶部），每 `sweep_every` 轮全量扫一遍兜底。4 路并发。

**发送者名字**：先查通讯录；查不到（大租户里很常见）就拉群成员列表；大群成员接口只返回前 100 人，剩下的名字在后续从 @ 提及里学到后，会**回填到历史消息**上。如果额外授权 `im:message.reactions:read`，以后可以改用 `+messages-mget` 拿到全部名字。

**刷新令牌**：refresh token 有效期 7 天，hub 常驻轮询期间会持续刷新；hub 停机超过 7 天就要重新 `lark-cli auth login`。

> ⚠️ **合规**：如果是公司租户，把公司聊天记录同步到个人设备或服务器上存储，可能违反公司的数据安全规范。请先确认公司政策再启用；数据库默认只在本机 `data/` 下。

---

## 微信（只读）

**方式**：读取微信 4.x 本地数据库的**已解密副本**（`session.db` / `contact.db` / `message_N.db`）。本连接器**不做**取密钥和解密，只读普通 SQLite：

```yaml
wechat:
  enabled: true
  db_dir: /Users/you/wechat-decrypted/db_storage
  self_wxid: wxid_xxx
```

**怎么得到解密镜像**由你自行选择工具并承担风险。现状（2026-11）：

- 原 `sjzar/chatlog` 已于 2025-10 在收到微信官方函件后**删除全部代码**。
- 社区仍有分支和同类工具（如 `chatlog-bot`、`wechat-db-decrypt-macos`）：取密钥时需要临时关闭 SIP，且只适配特定微信版本。
- **建议关闭微信自动更新、固定版本**，否则升级后取密钥可能失效。
- 工具要能在数据库变化时自动重新解密（fsnotify），本连接器每 10s 读一次增量（按每个会话的 `sort_seq` 游标）。

**为什么只读**：发消息只有两条路，要么模拟点击（脆弱），要么 hook / 协议（有封号风险）。第一阶段先在平台里看、跳回微信回复。

**已支持的消息类型**：文本、图片、语音、视频、表情、文件、链接、小程序、引用回复、聊天记录、红包/转账、系统消息；群成员显示名取“你的备注 > 群昵称 > 微信昵称”。

---

## QQ

**方式**：[NapCatQQ](https://github.com/NapNeko/NapCatQQ) 暴露 OneBot v11 正向 WebSocket，连接器订阅实时消息（私聊、群聊、自己发出的 `message_sent`）。

```yaml
qq:
  enabled: true
  ws_url: ws://127.0.0.1:3001
  access_token: "与 NapCat 配置一致"
  send: false
```

**必须知道的取舍**：

1. **桌面端互斥**：NapCat 以桌面端身份登录，而 QQ 同一账号只允许一个桌面端在线，所以它会把你 Mac 上正在用的 QQ 挤下线。建议跑在常开的 Mac 或虚拟机上，日常用手机 QQ；或者用小号。
2. **风控**：社区普遍反馈非官方客户端可能掉线、被要求重新扫码，严重时封号。只收不发风险最低：保持 `send: false`，出口 IP 固定，手机 QQ 保持在线。
3. **没有历史回填**：OneBot 只推实时消息，hub 停机期间的 QQ 消息会缺失。如果需要历史，可以后续加一个类似微信的“读已解密本地库”的连接器。

---

## 邮件（任意多个）

每个账号是一个独立的 source（`mail:<id>`），状态栏里各自显示。按 References / In-Reply-To 归并成线程，一个线程就是一个会话。同步时用只读 SELECT 加 `BODY.PEEK`，**不会把服务器上的邮件标成已读**；服务器上已读（`\Seen`）的邮件也不计未读。

密码三选一，推荐钥匙串：

```bash
security add-generic-password -s life-agent-gmail -a you@gmail.com -w '应用专用密码'
```
```yaml
mail:
  - id: gmail
    name: 个人 Gmail
    host: imap.gmail.com
    username: you@gmail.com
    password_cmd: security find-generic-password -w -s life-agent-gmail
```

| 邮箱 | host | 说明 |
|---|---|---|
| Gmail | imap.gmail.com | 开两步验证后生成“应用专用密码” |
| QQ 邮箱 | imap.qq.com | 设置里开启 IMAP，使用“授权码” |
| 163 / 126 | imap.163.com / imap.126.com | 授权码；已自动发送 IMAP ID，避免 Unsafe Login |
| 腾讯企业邮 | imap.exmail.qq.com | 客户端专用密码 |
| Outlook / Microsoft 365 | outlook.office365.com | 微软已关闭基本认证，需要 OAuth2，**暂不支持**（TODO） |
| 飞书邮箱 | 见飞书邮箱设置 | 开启 IMAP 后使用专用密码 |

默认 `mail_needs_attention: false`：普通邮件计未读，但不进“需要看”，除非是 VIP 发送者。

---

## 后续渠道（接口已就绪）

- **iMessage**：读 `~/Library/Messages/chat.db`（需要“完全磁盘访问”权限），用 AppleScript 发送。
- **Telegram**：gotd/td 用户身份客户端，可读可发，官方允许。
- **企业微信**：自建应用回调，或会话存档（需要管理员开通）。
