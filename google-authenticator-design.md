# Google Authenticator（TOTP）接入设计方案

> 版本：v1.0  
> 日期：2026-06-03  
> 适用项目：sok-im-flutter（基于 OpenIM Chat 后端）

---

## 目录

1. [背景与目标](#1-背景与目标)
2. [技术原理](#2-技术原理)
3. [整体流程设计](#3-整体流程设计)
   - 3.1 [绑定流程](#31-绑定流程)
   - 3.2 [登录校验流程](#32-登录校验流程)
   - 3.3 [解绑流程](#33-解绑流程)
4. [后端接口设计](#4-后端接口设计)
   - 4.1 [通用约定](#41-通用约定)
   - 4.2 [生成绑定密钥（GET Secret）](#42-生成绑定密钥get-secret)
   - 4.3 [确认绑定（Bind TOTP）](#43-确认绑定bind-totp)
   - 4.4 [校验 TOTP 码（Verify TOTP）](#44-校验-totp-码verify-totp)
   - 4.5 [查询 TOTP 绑定状态（Get Status）](#45-查询-totp-绑定状态get-status)
   - 4.6 [解绑（Unbind TOTP）](#46-解绑unbind-totp)
   - 4.7 [登录接口扩展](#47-登录接口扩展)
5. [数据库设计](#5-数据库设计)
6. [Flutter 客户端改造要点](#6-flutter-客户端改造要点)
7. [安全考量](#7-安全考量)
8. [错误码扩展](#8-错误码扩展)

---

## 1. 背景与目标

当前登录体系为**手机号 + 短信验证码 / 密码**。为提升账户安全性，引入基于 TOTP（Time-based One-Time Password）协议的 **Google Authenticator** 作为第二因素认证（2FA）。

**目标：**

- 用户在设置中心可选择绑定 Google Authenticator。
- 绑定后，每次登录需额外输入 Authenticator 生成的 6 位动态码。
- 支持解绑（需二次验证身份）。
- 兼容现有 OpenIM Chat 后端认证体系，最小化改造范围。

---

## 2. 技术原理

Google Authenticator 使用 **TOTP（RFC 6238）** 协议：

```
TOTP(K, T) = HOTP(K, T)
T = floor((current_unix_time - T0) / X)
```

- `K`：服务端生成的共享密钥（Base32 编码，通常 20 字节）
- `T0`：Unix 纪元（0）
- `X`：时间步长，标准为 **30 秒**
- 输出：6 位数字动态码，每 30 秒刷新一次

密钥通过 **otpauth URI** 传递给客户端：

```
otpauth://totp/{issuer}:{accountName}?secret={secret}&issuer={issuer}&algorithm=SHA1&digits=6&period=30
```

客户端（Google Authenticator / 任意 TOTP 应用）扫描二维码或手动输入密钥后即可生成动态码。

---

## 3. 整体流程设计

### 3.1 绑定流程

```
用户（App）                        后端
   │                                 │
   │── POST /totp/secret ──────────►│  ① 生成并返回 secret + otpauth URI
   │◄─ { secret, otpAuthUrl } ──────│
   │                                 │
   │  [展示二维码，用户扫码]           │
   │                                 │
   │── POST /totp/bind ─────────────►│  ② 用户输入 6 位码，后端验证后持久化
   │   { totpCode }                  │
   │◄─ { success: true } ───────────│
   │                                 │
   │  [绑定成功，提示用户保存恢复码]   │
```

**关键点：**
- `secret` 在步骤 ①② 之间为**临时态**，存储在 Redis（TTL = 10 分钟）。
- 步骤 ② 验证通过后，将 secret 写入 MongoDB，Redis 缓存删除。
- 同时生成 **8 组恢复码**（每组 8 位字母数字），供用户在丢失设备时使用。

### 3.2 登录校验流程

```
用户（App）                        后端
   │                                 │
   │── POST /account/login ─────────►│  ① 正常密码/短信验证码登录
   │   { phone, password/verifyCode }│
   │◄─ { mfaRequired: true,          │  ② 若已绑定 TOTP，返回 mfaRequired
   │      mfaToken: "xxx" } ─────────│     及临时 mfaToken（5 分钟有效）
   │                                 │
   │  [弹出 TOTP 输入界面]            │
   │                                 │
   │── POST /totp/verify ───────────►│  ③ 提交 6 位动态码 + mfaToken
   │   { mfaToken, totpCode }        │
   │◄─ { imToken, chatToken, ... } ──│  ④ 验证通过，返回完整登录凭证
```

**关键点：**
- `mfaToken` 用于关联本次登录会话，防止 TOTP 验证与登录请求脱钩。
- `mfaToken` 存储于 Redis，TTL = 5 分钟，使用一次后即删除（防重放）。

### 3.3 解绑流程

```
用户（App）                        后端
   │                                 │
   │── POST /totp/unbind ───────────►│  验证 TOTP 码 + 登录 Token
   │   { totpCode }                  │     或恢复码
   │◄─ { success: true } ───────────│  删除 MongoDB 中的 secret，返回成功
```

---

## 4. 后端接口设计

### 4.1 通用约定

| 项目 | 说明 |
|------|------|
| Base URL | `/chat/v1` （与现有 Chat 服务保持一致） |
| Content-Type | `application/json` |
| 认证方式 | 请求头 `token: <chatToken>`（绑定/解绑/状态查询需要登录态） |
| 响应格式 | `{ "errCode": 0, "errMsg": "", "data": { ... } }` |
| 时间格式 | Unix 时间戳（秒，int64） |

**通用响应结构：**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": { }
}
```

---

### 4.2 生成绑定密钥（GET Secret）

> 为已登录用户生成 TOTP 共享密钥，用于展示二维码。

**请求**

```
POST /totp/secret
```

请求头：

```
token: <chatToken>
```

请求体：

```json
{}
```

**响应**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "secret": "JBSWY3DPEHPK3PXP",
    "otpAuthUrl": "otpauth://totp/SOK-IM:+8613800138000?secret=JBSWY3DPEHPK3PXP&issuer=SOK-IM&algorithm=SHA1&digits=6&period=30",
    "expireAt": 1748930400
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `secret` | string | Base32 编码的共享密钥（20 字节） |
| `otpAuthUrl` | string | otpauth URI，客户端转为二维码展示 |
| `expireAt` | int64 | 临时 secret 过期时间（Unix 秒），10 分钟内有效 |

**业务规则：**
- 如果用户已绑定 TOTP，返回 `errCode: 20001`（已绑定，请先解绑）。
- 若 10 分钟内重复调用，覆盖旧的临时 secret。

---

### 4.3 确认绑定（Bind TOTP）

> 用户扫码后，输入第一个动态码完成绑定。

**请求**

```
POST /totp/bind
```

请求头：

```
token: <chatToken>
```

请求体：

```json
{
  "totpCode": "123456"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `totpCode` | string | 是 | 用户从 Authenticator 读取的 6 位动态码 |

**响应**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "recoveryCodes": [
      "A1B2-C3D4",
      "E5F6-G7H8",
      "I9J0-K1L2",
      "M3N4-O5P6",
      "Q7R8-S9T0",
      "U1V2-W3X4",
      "Y5Z6-A7B8",
      "C9D0-E1F2"
    ]
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `recoveryCodes` | string[] | 8 组一次性恢复码，**仅返回一次**，客户端应提示用户抄写保存 |

**业务规则：**
- 后端使用用户的临时 secret（Redis）校验 `totpCode`，允许 ±1 个时间步长的偏差（防止时钟偏移）。
- 验证通过后：将 secret 写入 MongoDB `user_totp` 集合；将恢复码哈希后存入 `user_totp_recovery` 集合；删除 Redis 临时 secret。
- `totpCode` 不正确：返回 `errCode: 20002`。
- 无临时 secret（未调用或已过期）：返回 `errCode: 20003`。

---

### 4.4 校验 TOTP 码（Verify TOTP）

> 登录第二步：校验 TOTP 动态码，成功后返回完整登录凭证。

**请求**

```
POST /totp/verify
```

请求头：无需登录 Token（此时用户尚未完成登录）

请求体：

```json
{
  "mfaToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "totpCode": "654321",
  "platform": 2
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `mfaToken` | string | 是 | 第一步登录接口返回的临时 MFA Token |
| `totpCode` | string | 是 | 6 位动态码；或 8 位恢复码（`A1B2C3D4` 格式） |
| `platform` | int | 是 | 平台标识（1: iOS, 2: Android, 3: Windows, 4: macOS, 5: Web） |

**响应（成功）**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "imToken": "xxx_im_token",
    "chatToken": "xxx_chat_token",
    "userID": "u_1234567890",
    "expireTimeSeconds": 604800
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `imToken` | string | OpenIM IM 服务 Token |
| `chatToken` | string | Chat 业务服务 Token |
| `userID` | string | 用户 ID |
| `expireTimeSeconds` | int64 | Token 有效期（秒） |

**业务规则：**
- `mfaToken` 从 Redis 取出对应 `userID`，验证 TOTP 后即删除（一次性）。
- 若使用恢复码登录，对应恢复码作废（标记已使用），剩余数量低于 3 时在响应中添加警告。
- `mfaToken` 过期（>5 分钟）：返回 `errCode: 20004`。
- TOTP 码错误：返回 `errCode: 20002`。

---

### 4.5 查询 TOTP 绑定状态（Get Status）

> 客户端查询当前用户是否已绑定 TOTP。

**请求**

```
POST /totp/status
```

请求头：

```
token: <chatToken>
```

请求体：

```json
{}
```

**响应**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "enabled": true,
    "boundAt": 1748920000,
    "recoveryCodesRemaining": 6
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否已绑定 TOTP |
| `boundAt` | int64 | 绑定时间（Unix 秒）；未绑定时为 0 |
| `recoveryCodesRemaining` | int | 剩余可用恢复码数量；未绑定时为 0 |

---

### 4.6 解绑（Unbind TOTP）

> 已绑定用户主动解绑，需验证当前 TOTP 码或恢复码。

**请求**

```
POST /totp/unbind
```

请求头：

```
token: <chatToken>
```

请求体：

```json
{
  "totpCode": "123456"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `totpCode` | string | 是 | 6 位动态码或 8 位恢复码，用于二次验证身份 |

**响应**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {}
}
```

**业务规则：**
- 验证通过后删除 MongoDB 中该用户的 `user_totp` 文档及所有 `user_totp_recovery` 文档。
- 若用户未绑定 TOTP：返回 `errCode: 20005`。

---

### 4.7 登录接口扩展

现有登录接口 `POST /account/login` 需扩展以支持 MFA 二步跳转：

**请求（不变）**

```json
{
  "areaCode": "86",
  "phoneNumber": "13800138000",
  "password": "md5_of_password",
  "platform": 2
}
```

**响应（未绑定 TOTP，行为不变）**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "imToken": "xxx",
    "chatToken": "xxx",
    "userID": "u_xxx",
    "expireTimeSeconds": 604800
  }
}
```

**响应（已绑定 TOTP，新增 mfaRequired 分支）**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "mfaRequired": true,
    "mfaToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "mfaTokenExpireAt": 1748921000
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `mfaRequired` | bool | `true` 表示需要进行 TOTP 二步验证 |
| `mfaToken` | string | 临时 MFA Token，传递给 `/totp/verify` |
| `mfaTokenExpireAt` | int64 | mfaToken 过期时间（Unix 秒），5 分钟 |

> 客户端判断响应中 `mfaRequired == true` 时，跳转 TOTP 验证界面，不持久化 Token。

---

## 5. 数据库设计（MongoDB）

持久化存储使用 **MongoDB**，与 OpenIM Server 现有 `mongoutil` 体系一致。建议在 `pkg/common/storage/model` 定义文档结构，在 `pkg/common/storage/database/mgo` 实现 CRUD，集合名注册于 `pkg/common/storage/database/name.go`。

### 集合：`user_totp`

每个用户至多一条记录（`user_id` 唯一）。

**文档字段：**

| 字段 | BSON 类型 | 说明 |
|------|-----------|------|
| `_id` | ObjectId | MongoDB 主键，插入时自动生成 |
| `user_id` | string | 用户 ID，业务唯一键 |
| `secret` | string | Base32 编码的 TOTP 共享密钥（AES-256-GCM 加密后存储） |
| `enabled` | bool | 是否启用，默认 `true` |
| `bound_at` | int64 | 绑定时间（Unix 秒） |
| `create_time` | time / Date | 创建时间 |
| `update_time` | time / Date | 更新时间 |

**索引：**

```javascript
db.user_totp.createIndex({ user_id: 1 }, { unique: true })
```

**Go Model 示例：**

```go
type UserTotp struct {
    ID         primitive.ObjectID `bson:"_id,omitempty"`
    UserID     string             `bson:"user_id"`
    Secret     string             `bson:"secret"`
    Enabled    bool               `bson:"enabled"`
    BoundAt    int64              `bson:"bound_at"`
    CreateTime time.Time          `bson:"create_time"`
    UpdateTime time.Time          `bson:"update_time"`
}
```

**常用操作：**

- 绑定：`InsertOne` / `ReplaceOne`（`upsert: true`，filter `{ user_id }`）
- 查询状态：`FindOne`，filter `{ user_id, enabled: true }`
- 解绑：`DeleteOne`，filter `{ user_id }`

### 集合：`user_totp_recovery`

每个用户 8 条恢复码，每条一条文档。

**文档字段：**

| 字段 | BSON 类型 | 说明 |
|------|-----------|------|
| `_id` | ObjectId | MongoDB 主键 |
| `user_id` | string | 用户 ID |
| `code_hash` | string | 恢复码的 bcrypt 哈希 |
| `used` | bool | 是否已使用，默认 `false` |
| `used_at` | int64 | 使用时间（Unix 秒）；未使用时为 `0` 或省略 |
| `create_time` | time / Date | 创建时间 |

**索引：**

```javascript
db.user_totp_recovery.createIndex({ user_id: 1 })
db.user_totp_recovery.createIndex({ user_id: 1, used: 1 })
```

**Go Model 示例：**

```go
type UserTotpRecovery struct {
    ID         primitive.ObjectID `bson:"_id,omitempty"`
    UserID     string             `bson:"user_id"`
    CodeHash   string             `bson:"code_hash"`
    Used       bool               `bson:"used"`
    UsedAt     int64              `bson:"used_at,omitempty"`
    CreateTime time.Time          `bson:"create_time"`
}
```

**常用操作：**

- 绑定：`InsertMany` 批量写入 8 条文档
- 校验恢复码：按 `user_id` + `used: false` 查询，逐条 `bcrypt.CompareHashAndPassword`
- 标记已用：`UpdateOne`，filter `{ _id }`，`$set: { used: true, used_at: now }`
- 统计剩余：`CountDocuments`，filter `{ user_id, used: false }`
- 解绑：`DeleteMany`，filter `{ user_id }`

### Redis Key 规范

| Key | Value | TTL | 说明 |
|-----|-------|-----|------|
| `totp:pending:{userID}` | Base32 secret | 600s | 绑定流程中的临时密钥 |
| `totp:mfa:{mfaToken}` | userID | 300s | 登录第一步的临时会话 |

---

## 6. Flutter 客户端改造要点

### 6.1 依赖包

在 `pubspec.yaml` 添加：

```yaml
dependencies:
  otp: ^3.1.4          # TOTP 生成（可用于本地测试/展示）
  qr_flutter: ^4.1.0   # 将 otpAuthUrl 渲染为二维码
```

### 6.2 登录流程改造（`apis.dart`）

在 `Apis.login()` 中，解析响应后判断 `mfaRequired`：

```dart
static Future<LoginCertificate> login({ ... }) async {
  final data = await HttpUtil.post(Urls.login, data: { ... });
  
  // 需要 TOTP 二步验证
  if (data['mfaRequired'] == true) {
    throw MfaRequiredException(
      mfaToken: data['mfaToken'] as String,
      expireAt: data['mfaTokenExpireAt'] as int,
    );
  }
  
  return LoginCertificate.fromJson(data!);
}

/// 新增：提交 TOTP 验证码，返回完整登录凭证
static Future<LoginCertificate> verifyTotp({
  required String mfaToken,
  required String totpCode,
}) async {
  final data = await HttpUtil.post(Urls.verifyTotp, data: {
    'mfaToken': mfaToken,
    'totpCode': totpCode,
    'platform': IMUtils.getPlatform(),
  });
  return LoginCertificate.fromJson(data!);
}
```

### 6.3 新增 URL 常量（`Urls` 类）

```dart
static const String totpSecret  = '/totp/secret';
static const String totpBind    = '/totp/bind';
static const String totpVerify  = '/totp/verify';
static const String totpStatus  = '/totp/status';
static const String totpUnbind  = '/totp/unbind';
```

### 6.4 新增页面

| 页面 | 路径 | 说明 |
|------|------|------|
| TOTP 绑定页 | `lib/login/totp_bind_page.dart` | 展示二维码 + 输入验证码确认绑定 |
| TOTP 验证页 | `lib/login/totp_verify_page.dart` | 登录第二步，输入 6 位动态码 |
| 恢复码展示页 | `lib/login/totp_recovery_page.dart` | 绑定成功后展示 8 组恢复码，提示用户抄写 |
| TOTP 设置项 | `lib/settings/security_page.dart` | 入口：开启 / 关闭 TOTP，查看剩余恢复码 |

### 6.5 登录跳转逻辑（`phone_auth_after_verification.dart`）

```dart
try {
  final cert = await Apis.login(...);
  // 正常登录成功
  await _saveAndNavigate(cert);
} on MfaRequiredException catch (e) {
  // 跳转 TOTP 验证页
  Get.to(() => TotpVerifyPage(mfaToken: e.mfaToken));
}
```

---

## 7. 安全考量

| 风险 | 缓解措施 |
|------|----------|
| 共享密钥泄露 | MongoDB 中使用 AES-256-GCM 加密存储 secret，密钥由 KMS 管理 |
| TOTP 码重放攻击 | 服务端记录最近一个时间步长内已使用的 TOTP 码（Redis Set，TTL 60s） |
| 暴力破解 TOTP | 同一 `mfaToken` 错误次数超过 5 次，立即使 `mfaToken` 失效，需重新登录 |
| MFA Token 泄露 | `mfaToken` 使用一次后立即从 Redis 删除；TTL 仅 5 分钟 |
| 时钟偏移 | 允许 ±1 个时间步长（±30 秒）的容错 |
| 设备丢失 | 提供 8 组一次性恢复码；恢复码使用 bcrypt 哈希存储 |
| 恢复码暴力破解 | 恢复码验证与 TOTP 码共用同一错误次数限制 |
| 传输安全 | 全程强制 HTTPS / TLS 1.2+ |

---

## 8. 错误码扩展

在现有 Chat 服务错误码体系上新增（建议范围 `20001 ~ 20099`）：

| errCode | 含义 | HTTP 状态码 |
|---------|------|-------------|
| `20001` | 用户已绑定 TOTP，请先解绑 | 400 |
| `20002` | TOTP 验证码错误 | 400 |
| `20003` | 临时密钥不存在或已过期，请重新发起绑定 | 400 |
| `20004` | MFA Token 不存在或已过期，请重新登录 | 401 |
| `20005` | 用户未绑定 TOTP | 400 |
| `20006` | 恢复码已全部使用完，请联系客服 | 403 |
| `20007` | 验证失败次数过多，请稍后重试 | 429 |

---

## 附录：接口汇总

| 方法 | 路径 | 是否需要登录 Token | 功能 |
|------|------|--------------------|------|
| POST | `/totp/secret` | 是 | 生成绑定密钥 |
| POST | `/totp/bind` | 是 | 确认绑定 |
| POST | `/totp/verify` | 否 | 登录第二步验证 |
| POST | `/totp/status` | 是 | 查询绑定状态 |
| POST | `/totp/unbind` | 是 | 解绑 TOTP |
| POST | `/account/login` | 否 | 登录（扩展 mfaRequired 响应） |
