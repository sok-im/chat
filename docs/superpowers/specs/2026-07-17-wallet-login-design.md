# Wallet Login Design (`getSignKey` / `checkWalletAddress` / `appLogin`)

Date: 2026-07-17  
Source contract: `wallet-login-apis.md` (Java `sok-api` AppWallet)  
Status: Approved for implementation planning

## 1. Goal

在本仓库 OpenIM Chat（Go）中实现与 Java `sok-api` 对齐的三个钱包登录 HTTP 接口，供现有客户端无感迁移：

- `POST /sok/app/appWallet/getSignKey`
- `POST /sok/app/appWallet/checkWalletAddress`
- `POST /sok/app/appWallet/appLogin`

成功响应格式保持 Java `R<T>`：`{code:200, data, msg:"success"}`；业务失败：`{code:500, data:null, msg:<key>}`。  
路径无需 token。

## 2. Decisions (locked)

| 决策 | 选择 |
|------|------|
| 部署位置 | 本仓库 `chat-api` HTTP + `chat-rpc` 业务 |
| HTTP 契约 | 路径与 `{code,data,msg}` 对齐 Java |
| 登录/注册 | 进程内直接调用现有 Chat RPC（`Login` / `RegisterUser`），不 HTTP 自调用 |
| 分层 | API 薄路由；RPC 持 Redis/Mongo/验签/登录注册 |
| Redis 消费 | 验签成功后**不删** key，仅 TTL 10 分钟 |

## 3. Architecture

```
Client
  |  POST /sok/app/appWallet/*
  v
chat-api (Gin)
  |  解析 JSON → 调 gRPC → 将结果包装为 {code,data,msg}
  v
chat-rpc
  |-- Redis: walletTraceId:{traceId} = str (TTL 600s)
  |-- Mongo: user_login_wallet
  |-- crypto: EVM / TRON / Bitcoin / Solana 验签
  +-- 已有 RegisterUser / Login（uid / autoLogin）
```

### 3.1 API 层职责

- 注册路由组 `/sok/app/appWallet`，三接口均 **无** `CheckToken`。
- HTTP 层使用与 Java 一致的 JSON 请求/响应结构体（camelCase），再映射到 proto；**不**把 proto JSON 直接暴露给客户端。
- 自定义响应写入：HTTP 状态码固定 `200`；body 成功 `code=200`，失败 `code=500` 且 `msg` 为约定错误 key/文案。
- **不**使用现有 `apiresp`（其输出为 `errCode/errMsg`），避免破坏客户端契约。
- `appLogin` 从 Gin 取客户端 IP，传入 RPC，供内部 `RegisterUser`/`Login` 使用。
- 从 RPC error / 业务结果映射到 Java 风格错误 key。

### 3.2 RPC 层职责

在 `chat.proto` 的 `Chat` service 新增三个 RPC：

- `GetWalletSignKey`
- `CheckWalletAddress`
- `WalletAppLogin`

实现放在 `internal/rpc/chat/`（如 `wallet.go` + `wallet_verify.go`）。

`WalletAppLogin` 内部直接调用本服务已有逻辑（同进程方法调用 `RegisterUser` / `Login`），而不是再开 gRPC 自调用。

## 4. Data Model

### 4.1 Redis

| 项 | 值 |
|----|-----|
| Key | `walletTraceId:{traceId}` |
| Value | 10 位字母数字随机串 `str` |
| TTL | 600 秒 |
| 删除 | 不主动删除 |

### 4.2 Mongo `user_login_wallet`

| 字段 | BSON | 说明 |
|------|------|------|
| id | `_id` | string UUID（与 Java/客户端一致，不用 ObjectID） |
| userId | `userId` | Chat/IM userID |
| evmAddress | `evmAddress` | 可选 |
| tronAddress | `tronAddress` | 可选 |
| bitcoinAddress | `bitcoinAddress` | 可选 |
| solanaAddress | `solanaAddress` | 可选 |
| updatedTime | `updatedTime` | datetime |

查询顺序：对非空地址依次按 `evmAddress` → `tronAddress` → `bitcoinAddress` → `solanaAddress` 等值查询，**先命中先返回**。

建议索引（非唯一，允许多链绑定同一用户文档）：各地址字段 sparse 索引，便于查找。

新增：

- `pkg/common/db/table/chat/user_login_wallet.go`
- `pkg/common/db/model/chat/user_login_wallet.go`
- `ChatDatabaseInterface` 扩展：`SelectWalletByAddress` / `CreateWallet` / `UpdateWalletAddresses`

## 5. API Contracts

### 5.1 `getSignKey`

- Request: 空 body 可接受
- Response data: `{str, traceId}`
- Steps: `randomString(10)` + UUID → Redis SET EX 600 → 返回

### 5.2 `checkWalletAddress`

- Request: `traceId` 必填；`evm`/`tron`/`bitcoin`/`solana` 至少一条；链对象内 `address`+`sign` 必填，`msgHash` 可选
- Response data: `{isRegister: 0|1}`
- Errors:
  - Redis miss → `sign.key.error`
  - 四链全空 → `least.transmit.one.piece.of.data`
  - 任一已传链验签失败 → `wallet.address.check.error`

### 5.3 `appLogin`

- Request: `deviceID`、`platform`(≥1)、`traceId` 必填；链至少一条；可选 `invitationCode`/`firstName`/`lastName`/`language`/`gender`
- Response data: `{imToken, chatToken, userID}`
- Flow:
  1. 同 check 取 Redis + 验签
  2. `SelectWalletByAddress`
  3. **已注册**：`Login(platform, deviceID, uid=wallet.userId)` → 更新非空链地址 + `updatedTime`
  4. **未注册**：`RegisterUser(..., autoLogin=true, invitationCode, user{firstName,lastName,language,gender})` → insert `user_login_wallet`
  5. 下游失败：将错误信息放入 `msg`（对齐 Java 透传 `errMsg` / `common.fail`）

注册时若用户未传姓名，可用现有 Chat 注册默认昵称逻辑；不额外发明字段。

## 6. Signature Verification (must match Java)

客户端对 `str` 原文签名。

### 6.1 EVM

1. `sign` hex（可带 `0x`）
2. `hash = keccak256("\x19Ethereum Signed Message:\n" + len(str) + str)`
3. recovery id 尝试 `0..3`，ecrecover → 地址
4. 与 `address` **忽略大小写**比较
5. 入参 `msgHash` **忽略**（与 Java 一致）

### 6.2 TRON

1. 提取 r/s，依次尝试前缀：
   - `"\x19TRON Signed Message:\n" + len + str`
   - `"\x19Ethereum Signed Message:\n" + len + str`
2. keccak256 + recovery `0..3`
3. 公钥 → 20 字节 → 前缀 `0x41` → Base58Check → 与 `address` **精确相等**

### 6.3 Bitcoin

1. `sign`：BIP-137 base64 消息签名
2. 恢复公钥后比对：主网/测试网 Legacy P2PKH、主网/测试网 SegWit bech32，任一匹配即通过

### 6.4 Solana

1. `address`：Ed25519 公钥 base58
2. message：`msgHash` 非空则 `hexDecode(msgHash)`，否则 `utf8(str)`
3. `sign` hex → Ed25519 Verify

### 6.5 `validChainAddress` 语义

- 链参数 nil/空 → 返回地址 `""`（跳过）
- 验签通过 → 返回 `address`
- 验签失败 → 上层报 `wallet.address.check.error`

## 7. Error Mapping

| msg | 场景 |
|-----|------|
| `sign.key.error` | Redis 无 traceId |
| `least.transmit.one.piece.of.data` | 四链全空 |
| `wallet.address.check.error` | 验签失败 |
| `<RegisterUser/Login err message>` | 下游业务失败 |
| `common.fail` | 未预期空结果等 |

HTTP 状态码固定 `200`；成败只看 body `code`（对齐 Java `R` 包装）。

## 8. File / Module Layout

```
pkg/protocol/chat/chat.proto          # +3 RPCs + messages
internal/api/chat/start.go            # 注册 /sok/app/appWallet 路由
internal/api/chat/wallet.go           # HTTP handlers + R 包装
internal/rpc/chat/wallet.go           # GetWalletSignKey / Check / AppLogin
internal/rpc/chat/wallet_verify.go    # 四链验签
pkg/common/db/table/chat/user_login_wallet.go
pkg/common/db/model/chat/user_login_wallet.go
pkg/common/db/database/chat.go        # 接口扩展
pkg/common/db/cache/wallet.go         # Redis get/set trace（可选独立文件）
internal/rpc/chat/start.go            # 注入 Redis wallet cache + wallet model
```

依赖库（按需）：`go-ethereum`（EVM/TRON keccak/ecrecover）、`btcsuite` 或等价 BIP-137、`ed25519` + base58（Solana）。

## 9. Testing

- 单元测试：四链验签正反例（含 TRON 双前缀、BTC 多地址格式、Solana msgHash 有无）
- Redis TTL / miss → `sign.key.error`
- 至少一条链校验；空链失败
- `SelectWalletByAddress` 顺序与命中
- `appLogin`：未注册 insert；已注册 update 地址并返回 token（可 mock Database + 抽离 Login/Register 调用）

## 10. Out of Scope

- 验签后删除 Redis key
- i18n 翻译层（直接返回 key / 下游文案）
- 改造现有 `/account/*` 响应格式
- 独立 sok-api 微服务

## 11. Success Criteria

- 客户端可按现有 Flutter/Java 流程调用三接口完成钱包登录
- 响应字段与错误 key 与 `wallet-login-apis.md` 一致
- 不破坏现有 OpenIM Chat `errCode` API
- 登录/注册复用现有 `uid` / `autoLogin` 能力，无 HTTP 环回

## 12. Java 源码对齐清单（实现必须逐项满足）

> 对照 `wallet-login-apis.md` §8 Java 原文；Go 行为与 Java **代码**一致，不以文档描述性文字为准。

### 12.1 Controller 行为

| Java | Go 对齐 |
|------|---------|
| `RandomUtil.randomString(10)` | 字符集 `a-z0-9`（Hutool `BASE_CHAR_NUMBER`，小写+数字） |
| `UUID.randomUUID().toString()` | `uuid.New().String()` |
| `redis SET walletTraceId:{traceId} str EX 10 MINUTES` | key 前缀 `walletTraceId:`，TTL 600s |
| `StringUtils.isEmpty(str)` → `R.fail("sign.key.error")` | Redis miss / 空串 |
| 四链 `ObjectUtils.isEmpty` 全 true → `least.transmit.one.piece.of.data` | 仅 **nil** 链跳过；JSON `null` 字段 = 未传 |
| `validChainAddress` null → `wallet.address.check.error` | 验签失败返回 null 语义 |
| `validChainAddress` empty → `""` | 链参数 nil 时不验签 |
| `isRegister` = `BooleanEnum.yes/no` | `1` / `0` |
| 登录 `userID` = `userLoginWallet.getUserId()` | **不用** Login 响应里的 userID |
| 登录 update 仅 `StringUtils.isNotEmpty(addr)` 的字段 | 条件更新非空链地址 + `updatedTime` |
| 注册 insert 写入四条地址字段（可为空串） | 全字段写入 |
| 下游空响应 `R.fail()` | `msg` 对齐通用失败（`common.fail`） |
| 下游 `errCode != 0` → `R.fail(errMsg)` | 透传错误文案 |
| 成功 `R.data(...)` | `{code:200, data, msg:"success"}` |

### 12.2 `validChainAddress`（逐字对齐）

```java
if (ObjectUtils.isEmpty(chainParams)) return "";
return checker.getAsBoolean() ? chainParams.getAddress() : null;
```

- `checkEvmAddress(addr, str, msgHash, sign)` — **msgHash 未使用**
- `checkTronAddress(addr, str, sign)` — 无 msgHash 参数
- `checkBitcoinAddress(addr, str, sign)`
- `checkSolanaAddress(addr, str, msgHash, sign)`

### 12.3 验签算法（移植 `UserLoginWalletServiceImpl`）

**EVM**：`Numeric.hexStringToByteArray(sign)`；prefix=`\x19Ethereum Signed Message:\n`+UTF-8字节长度；`Hash.sha3`；r/s 各 32 字节；recovery `0..3`；`Keys.getAddress`；`equalsIgnoreCase`。

**TRON**：r/s 同上；双前缀 TRON/Ethereum；`publicKeyToTronAddress`：`0x41`+20字节 → 双 SHA256 校验 → Base58；**精确相等**。

**Bitcoin**：`ECKey.signedMessageToKey(str, sign)` 等价；比对 Legacy 主网/测试网 + SegWit bech32 主网/测试网。

**Solana**：`Base58.decode(address)`；X509 前缀 `302a300506032b6570032100`；`msgHash` 非空则 hex 解码为 message，否则 `utf8(str)`；`hex` 解码 sign；Ed25519 verify。

### 12.4 Mongo `user_login_wallet`

BSON 字段名 camelCase：`userId`, `evmAddress`, `tronAddress`, `bitcoinAddress`, `solanaAddress`, `updatedTime`（与 Java `@CollectionName` entity 一致）。

`selectInfoByAddress` 顺序：EVM → TRON → Bitcoin → Solana；`StringUtils.isEmpty(address)` 跳过。

### 12.5 `appLogin` 与 Chat 集成

| Java HTTP body | Go RPC 等价 |
|----------------|-------------|
| 登录 `{platform, deviceID, uid}` | `LoginReq{platform, deviceID, uid}` |
| 注册 `{platform, deviceID, invitationCode, autoLogin:true, user:{firstName,lastName,language,gender}}` | `RegisterUserReq` 同字段 |

进程内 RPC 后，**API 层**补齐 Java HTTP 下游副作用：新用户 OpenIM `RegisterUser` + 默认好友/群；`imToken` 经 `imApiCaller.GetUserToken`（与 `/account/login`、`/account/register` 一致）。

### 12.6 不在范围

- 验签后 DEL Redis key
- i18n 翻译 `msg` key
