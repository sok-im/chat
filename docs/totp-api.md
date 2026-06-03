# TOTP / Google Authenticator API

本文档描述 Chat API 中 `/totp` 路由组的 HTTP 接口，对应路由注册见 `internal/api/chat/start.go`。

## 通用约定

| 项目 | 说明 |
|------|------|
| 方法 | 均为 `POST` |
| Content-Type | `application/json` |
| 请求头 `operationID` | 必填，链路追踪 ID（与 OpenIM 其它接口一致） |
| 请求头 `token` | 需登录的接口必填，值为 `chatToken`（由 `mw.CheckToken` 校验） |
| 响应包装 | `{ "errCode": 0, "errMsg": "", "data": { ... } }` |
| 时间字段 | Unix 时间戳（秒，`int64`） |

**成功响应示例（结构）：**

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {}
}
```

**失败响应示例：**

```json
{
  "errCode": 20052,
  "errMsg": "TotpCodeInvalid",
  "data": null
}
```

## 接口一览

| 路径 | 需要 `token` | 说明 |
|------|--------------|------|
| `POST /totp/secret` | 是 | 生成临时绑定密钥（展示二维码） |
| `POST /totp/bind` | 是 | 校验首个动态码，完成绑定 |
| `POST /totp/verify` | 否 | 登录第二步：校验 TOTP / 恢复码 |
| `POST /totp/status` | 是 | 查询当前用户绑定状态 |
| `POST /totp/unbind` | 是 | 解绑（需校验动态码或恢复码） |

## 典型流程

### 绑定流程

```
1. POST /totp/secret   （已登录）→ 获取 secret / otpAuthUrl
2. 用户在 Authenticator 扫码
3. POST /totp/bind     （已登录，提交 6 位 totpCode）→ 返回 recoveryCodes（仅一次）
```

### 登录 + MFA 流程

```
1. POST /account/login → 若已绑定 TOTP，data 含 mfaRequired、mfaToken
2. POST /totp/verify   （无需 token）→ 提交 mfaToken + totpCode → 返回 imToken、chatToken、userID
```

---

## 1. 生成绑定密钥

`POST /totp/secret`

为当前登录用户生成临时 TOTP 密钥，写入 Redis，有效期 **10 分钟**。已绑定用户会报错。

### 请求

**Headers**

```
Content-Type: application/json
operationID: bind-totp-secret-001
token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

**Body**

```json
{}
```

> `userID` 由服务端从 `token` 解析，客户端无需传入。

### 响应（成功）

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "secret": "JBSWY3DPEHPK3PXP",
    "otpAuthUrl": "otpauth://totp/SOK-IM:%2B8613800138000?secret=JBSWY3DPEHPK3PXP&issuer=SOK-IM&algorithm=SHA1&digits=6&period=30",
    "expireAt": 1748930400
  }
}
```

### 响应字段（`data`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `secret` | string | Base32 共享密钥，可手动输入 Authenticator |
| `otpAuthUrl` | string | `otpauth://` URI，用于生成二维码；Issuer 为 `SOK-IM` |
| `expireAt` | int64 | 临时密钥过期时间（Unix 秒），约 10 分钟后失效 |

### 错误码

| errCode | errMsg | 说明 |
|---------|--------|------|
| 20051 | TotpAlreadyBound | 用户已绑定 TOTP，需先解绑 |

### cURL 示例

```bash
curl -X POST 'http://{host}:{port}/totp/secret' \
  -H 'Content-Type: application/json' \
  -H 'operationID: bind-totp-secret-001' \
  -H 'token: <chatToken>' \
  -d '{}'
```

---

## 2. 确认绑定

`POST /totp/bind`

使用 `/totp/secret` 返回的临时密钥，校验用户输入的 **6 位**动态码后持久化绑定，并下发 **8 个**一次性恢复码。

### 请求

**Headers**

```
Content-Type: application/json
operationID: bind-totp-001
token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

**Body**

```json
{
  "totpCode": "123456"
}
```

### 请求字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `totpCode` | string | 是 | Authenticator 当前 6 位动态码（允许 ±1 个时间窗口） |

### 响应（成功）

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

### 响应字段（`data`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `recoveryCodes` | string[] | 8 个恢复码，格式 `XXXX-XXXX`（大写字母 + 数字），**仅此次响应返回明文**，请提示用户妥善保存 |

### 错误码

| errCode | errMsg | 说明 |
|---------|--------|------|
| 20052 | TotpCodeInvalid | 动态码错误 |
| 20053 | TotpSecretNotFound | 未调用 `/totp/secret` 或临时密钥已过期 |

### cURL 示例

```bash
curl -X POST 'http://{host}:{port}/totp/bind' \
  -H 'Content-Type: application/json' \
  -H 'operationID: bind-totp-001' \
  -H 'token: <chatToken>' \
  -d '{"totpCode":"123456"}'
```

---

## 3. 登录第二步校验（Verify）

`POST /totp/verify`

用户在 `POST /account/login` 收到 `mfaRequired: true` 后调用。**不需要** `token` 请求头。校验通过后返回完整登录凭证（与正常登录成功结构一致）。

### 前置：登录接口 MFA 响应

`POST /account/login` 在账号已绑定 TOTP 时示例：

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "mfaRequired": true,
    "mfaToken": "a1b2c3d4e5f6789012345678abcdef01",
    "mfaTokenExpireAt": 1748921000
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `mfaRequired` | bool | 为 `true` 时需继续调用 `/totp/verify` |
| `mfaToken` | string | 临时 MFA 会话标识，传给本接口 |
| `mfaTokenExpireAt` | int64 | `mfaToken` 过期时间（Unix 秒），约 **5 分钟**有效，一次性使用 |

### 请求

**Headers**

```
Content-Type: application/json
operationID: totp-verify-001
```

**Body**

```json
{
  "mfaToken": "a1b2c3d4e5f6789012345678abcdef01",
  "totpCode": "654321",
  "platform": 2
}
```

### 请求字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `mfaToken` | string | 是 | 登录接口返回的临时 MFA Token |
| `totpCode` | string | 是 | 6 位 TOTP 动态码；或 **8 位**恢复码（可带 `-`，如 `A1B2-C3D4`） |
| `platform` | int32 | 否 | 平台 ID（1:iOS, 2:Android, 3:Windows, 4:macOS, 5:Web 等）；省略时使用登录阶段记录的平台 |

> 恢复码判断规则：`totpCode` 长度 **大于 6** 时按恢复码处理；否则按 6 位 TOTP 处理。

### 响应（成功）

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "imToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "chatToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "userID": "u_1234567890"
  }
}
```

### 响应字段（`data`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `imToken` | string | OpenIM IM 服务 Token |
| `chatToken` | string | Chat 业务 Token，后续需登录接口放在请求头 `token` |
| `userID` | string | 用户 ID |

### 错误码

| errCode | errMsg | 说明 |
|---------|--------|------|
| 20052 | TotpCodeInvalid | TOTP 或恢复码错误 |
| 20054 | MfaTokenExpired | `mfaToken` 无效或已过期（含已使用） |
| 20055 | TotpNotBound | 用户未绑定 TOTP |
| 20056 | TotpRecoveryCodesExhausted | 恢复码已全部用完 |
| 20057 | TotpVerifyTooManyAttempts | 同一 `mfaToken` 连续失败超过 5 次，会话作废 |

### cURL 示例

```bash
curl -X POST 'http://{host}:{port}/totp/verify' \
  -H 'Content-Type: application/json' \
  -H 'operationID: totp-verify-001' \
  -d '{
    "mfaToken": "a1b2c3d4e5f6789012345678abcdef01",
    "totpCode": "654321",
    "platform": 2
  }'
```

---

## 4. 查询绑定状态

`POST /totp/status`

查询当前登录用户是否已启用 TOTP 及恢复码余量。

### 请求

**Headers**

```
Content-Type: application/json
operationID: totp-status-001
token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

**Body**

```json
{}
```

### 响应（已绑定）

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

### 响应（未绑定）

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "enabled": false,
    "boundAt": 0,
    "recoveryCodesRemaining": 0
  }
}
```

### 响应字段（`data`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否已绑定并启用 TOTP |
| `boundAt` | int64 | 绑定时间（Unix 秒）；未绑定时为 `0` |
| `recoveryCodesRemaining` | int64 | 剩余可用恢复码数量；未绑定时为 `0` |

### cURL 示例

```bash
curl -X POST 'http://{host}:{port}/totp/status' \
  -H 'Content-Type: application/json' \
  -H 'operationID: totp-status-001' \
  -H 'token: <chatToken>' \
  -d '{}'
```

---

## 5. 解绑

`POST /totp/unbind`

验证当前 **6 位**动态码或恢复码后，删除用户的 TOTP 配置及全部恢复码记录。

### 请求

**Headers**

```
Content-Type: application/json
operationID: totp-unbind-001
token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

**Body**

```json
{
  "totpCode": "123456"
}
```

或使用恢复码：

```json
{
  "totpCode": "A1B2-C3D4"
}
```

### 请求字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `totpCode` | string | 是 | 6 位动态码，或 8 位恢复码（可含 `-`） |

### 响应（成功）

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {}
}
```

### 错误码

| errCode | errMsg | 说明 |
|---------|--------|------|
| 20052 | TotpCodeInvalid | 验证码错误 |
| 20055 | TotpNotBound | 用户未绑定 TOTP |

### cURL 示例

```bash
curl -X POST 'http://{host}:{port}/totp/unbind' \
  -H 'Content-Type: application/json' \
  -H 'operationID: totp-unbind-001' \
  -H 'token: <chatToken>' \
  -d '{"totpCode":"123456"}'
```

---

## TOTP 相关错误码汇总

定义见 `pkg/eerrs/predefine.go`（20051–20057）。

| errCode | errMsg | 典型场景 |
|---------|--------|----------|
| 20051 | TotpAlreadyBound | `/totp/secret` 时用户已绑定 |
| 20052 | TotpCodeInvalid | 绑定、验证、解绑时码不正确 |
| 20053 | TotpSecretNotFound | `/totp/bind` 时无有效临时密钥 |
| 20054 | MfaTokenExpired | `/totp/verify` 时 `mfaToken` 无效或过期 |
| 20055 | TotpNotBound | 未绑定却执行需绑定态的操作 |
| 20056 | TotpRecoveryCodesExhausted | 登录验证时恢复码已用尽 |
| 20057 | TotpVerifyTooManyAttempts | MFA 验证失败次数过多 |

---

## 实现参考

| 层级 | 路径 |
|------|------|
| 路由 | `internal/api/chat/start.go` |
| HTTP Handler | `internal/api/chat/totp.go` |
| RPC 逻辑 | `internal/rpc/chat/totp.go` |
| Proto 定义 | `pkg/protocol/chat/chat.proto` |
| 设计说明 | `google-authenticator-design.md` |
