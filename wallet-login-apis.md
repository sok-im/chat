# 钱包登录接口文档（`getSignKey` / `checkWalletAddress` / `appLogin`）

> 来源：Java 项目 `sok-api` 的 `AppWalletController` + `UserLoginWalletServiceImpl`  
> 用途：供后续用 Go 重新实现时对齐契约与算法  
> 前缀：`POST /sok/app/appWallet`  
> 鉴权：该路径在拦截器中放行，**无需 token**（见 `InterceptorsConfiguration` 中 `/sok/app/appWallet/**`）

---

## 1. 总览

### 1.1 业务流程

```
客户端                         sok-api                              Redis / Mongo / Chat
   |                              |                                        |
   |-- POST /getSignKey --------->|                                        |
   |                              |-- SET walletTraceId:{traceId}=str ---->|
   |<-- {str, traceId} -----------|                                        |
   |                              |                                        |
   |  本地对 str 多链签名                                                    |
   |                              |                                        |
   |-- POST /checkWalletAddress ->|                                        |
   |   {traceId, evm/tron/...}    |-- GET walletTraceId:{traceId} -------->|
   |                              |-- 验签各链                              |
   |                              |-- 查 user_login_wallet ---------------->|
   |<-- {isRegister: 0|1} --------|                                        |
   |                              |                                        |
   |-- POST /appLogin ----------->|                                        |
   |   {traceId, deviceID, ...}   |-- GET + 验签（同上）                     |
   |                              |-- 查库                                 |
   |                              |   已注册: POST Chat /account/login     |
   |                              |   未注册: POST Chat /account/register  |
   |                              |-- 写/更新 user_login_wallet ----------->|
   |<-- {imToken,chatToken,userID}|                                        |
```

### 1.2 通用响应包装 `R<T>`

| 字段 | 类型 | 说明 |
|------|------|------|
| `code` | int | `200` 成功，`500` 失败（及业务错误） |
| `data` | T / null | 业务数据 |
| `msg` | string | 提示文案（支持 i18n key） |

成功示例：

```json
{"code":200,"data":{...},"msg":"success"}
```

失败示例：

```json
{"code":500,"data":null,"msg":"sign key error"}
```

### 1.3 本接口相关错误 key（Java 侧 `R.fail(...)`）

| key / 文案 | 场景 |
|------------|------|
| `sign.key.error` | Redis 中找不到 `traceId` 对应的签名串（过期或不存在） |
| `least.transmit.one.piece.of.data` | `evm/tron/bitcoin/solana` 全为空 |
| `wallet.address.check.error` | 任一已传链验签失败 |
| Chat 返回的 `errMsg` | 下游登录/注册失败时透传 |
| `common.fail` | 下游 HTTP 空响应等通用失败 |

### 1.4 Redis / 存储

| 项 | 值 |
|----|-----|
| Redis Key | `walletTraceId:{traceId}` |
| Redis Value | 随机签名串 `str`（10 位字母数字） |
| TTL | **10 分钟** |
| 单次消费 | **当前 Java 实现验签后不删除 key**（仅依赖 TTL；客户端按「每次重新 getSignKey」使用） |
| Mongo 集合 | `user_login_wallet` |

### 1.5 下游 Chat Auth（配置项）

```yaml
business:
  app-user-register: http://13.215.203.29:10008/account/register
  app-user-login: http://13.215.203.29:10008/account/login
```

请求头：`operationID` = 当前 Unix 秒级时间戳字符串。

下游成功判定：`errCode == 0`，业务数据在 `data` 字段。

---

## 2. `POST /sok/app/appWallet/getSignKey`

### 2.1 功能

生成一次性（按客户端约定）待签名随机串，并写入 Redis，供后续 `checkWalletAddress` / `appLogin` 验签使用。

### 2.2 请求

- Method：`POST`
- Body：无（可空 JSON `{}`）
- 必填字段：无

### 2.3 响应 `data: GetSignKeyRet`

| 字段 | 类型 | 说明 |
|------|------|------|
| `str` | string | 随机签名串，长度 10，10 分钟内有效 |
| `traceId` | string | UUID，后续接口原样带回 |

示例：

```json
{
  "code": 200,
  "data": {
    "str": "3xbly4t54i",
    "traceId": "a2ce4d2e-3e2b-4bf9-992d-c22bd8d9c5e5"
  },
  "msg": "success"
}
```

### 2.4 实现要点（Go 对齐）

1. `str = randomString(10)`（字母数字）
2. `traceId = uuid.New().String()`
3. `SET walletTraceId:{traceId} = str EX 600`
4. 返回 `{str, traceId}`

---

## 3. `POST /sok/app/appWallet/checkWalletAddress`

### 3.1 功能

用 `traceId` 取出 Redis 中的 `str`，对提交的链地址做签名校验，再按地址查询是否已在 `user_login_wallet` 注册，返回 `isRegister`。

**注意：本接口只检查，不登录/不注册、不写库。**

### 3.2 请求 `CheckWalletAddressParams`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `traceId` | string | **是** (`@NotBlank`) | `getSignKey` 返回的记录 ID |
| `evm` | `AppLoginChainParams` | 否 | EVM 链；没有则不传/null |
| `tron` | `AppLoginChainParams` | 否 | TRON 链 |
| `bitcoin` | `AppLoginChainParams` | 否 | Bitcoin 链 |
| `solana` | `AppLoginChainParams` | 否 | Solana 链 |

约束：四条链**至少传一条**。

#### `AppLoginChainParams`（链参数，传了该链时内部字段必填）

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `address` | string | **是** | 链上地址 |
| `sign` | string | **是** | 对 `str` 的签名 |
| `msgHash` | string | 否 | 消息 hash；没有传空。EVM/Solana 可能使用 |

示例：

```json
{
  "traceId": "a2ce4d2e-3e2b-4bf9-992d-c22bd8d9c5e5",
  "evm": {
    "address": "0xAbc...",
    "sign": "0x...",
    "msgHash": "0x..."
  },
  "tron": {
    "address": "T...",
    "sign": "0x...",
    "msgHash": ""
  },
  "bitcoin": {
    "address": "1...",
    "sign": "<base64 BIP-137 signature>",
    "msgHash": ""
  },
  "solana": {
    "address": "<base58 pubkey>",
    "sign": "<hex signature>",
    "msgHash": "<hex of utf8(str) or empty>"
  }
}
```

### 3.3 响应 `data: CheckWalletAddressRet`

| 字段 | 类型 | 说明 |
|------|------|------|
| `isRegister` | int | `1`=已注册，`0`=未注册 |

### 3.4 处理步骤

1. `GET walletTraceId:{traceId}` → 空则 `sign.key.error`
2. 四链全空 → `least.transmit.one.piece.of.data`
3. 对每个非空链调用对应 `checkXxxAddress`；失败 → `wallet.address.check.error`
4. `selectInfoByAddress(evm, tron, bitcoin, solana)`：按顺序用非空地址查库（先匹配到即返回）
5. 有记录 → `isRegister=1`，否则 `0`

`validChainAddress` 语义：

- 链参数为 null/空 → 返回地址 `""`（跳过验签）
- 验签通过 → 返回 `address`
- 验签失败 → 返回 `null`（上层报错）

---

## 4. `POST /sok/app/appWallet/appLogin`

### 4.1 功能

验签后：

- **已注册**：调用 Chat `/account/login`（按 `uid` 登录），更新钱包地址
- **未注册**：调用 Chat `/account/register`（`autoLogin=true`），写入 `user_login_wallet`

返回 IM/Chat token 与 `userID`。

### 4.2 请求 `AppLoginParams`

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `deviceID` | string | **是** | 设备 ID |
| `platform` | int | **是** (`@Min(1)`) | 平台（OpenIM 平台号） |
| `traceId` | string | **是** | 签名记录 ID |
| `invitationCode` | string | 否 | 邀请码（注册用） |
| `firstName` | string | 否 | 姓（注册用） |
| `lastName` | string | 否 | 名（注册用） |
| `language` | string | 否 | 语言（注册用） |
| `gender` | int | 否 | 性别，默认 0 |
| `evm` / `tron` / `bitcoin` / `solana` | `AppLoginChainParams` | 否 | 同 check；至少一条 |

### 4.3 响应 `data: AppLoginRet`

| 字段 | 类型 | 说明 |
|------|------|------|
| `imToken` | string | OpenIM token |
| `chatToken` | string | Chat 业务 token |
| `userID` | string | 用户 ID |

### 4.4 下游请求体

#### 登录（已存在 `UserLoginWallet`）

`POST {business.app-user-login}`

```json
{
  "platform": 2,
  "deviceID": "...",
  "uid": "<userLoginWallet.userId>"
}
```

成功 `data` 取：`chatToken`、`imToken`；`userID` 用库里的 `userId`。

然后更新 Mongo：

- 按 `_id` 更新非空链地址 + `updatedTime`

#### 注册（不存在）

`POST {business.app-user-register}`

```json
{
  "platform": 2,
  "deviceID": "...",
  "invitationCode": "...",
  "autoLogin": true,
  "user": {
    "firstName": "...",
    "lastName": "...",
    "language": "...",
    "gender": 0
  }
}
```

成功 `data` 取：`chatToken`、`imToken`、`userID`。

然后 insert：

```json
{
  "userId": "<userID>",
  "evmAddress": "...",
  "tronAddress": "...",
  "bitcoinAddress": "...",
  "solanaAddress": "...",
  "updatedTime": "<now>"
}
```

下游响应约定：

```json
{
  "errCode": 0,
  "errMsg": "",
  "data": {
    "chatToken": "...",
    "imToken": "...",
    "userID": "..."
  }
}
```

`errCode != 0` 时透传 `errMsg` 给客户端。

---

## 5. 数据模型 `user_login_wallet`

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | Mongo `_id` |
| `userId` | string | Chat/IM 用户 ID |
| `evmAddress` | string | EVM 地址 |
| `tronAddress` | string | TRON 地址 |
| `bitcoinAddress` | string | Bitcoin 地址 |
| `solanaAddress` | string | Solana 地址 |
| `updatedTime` | datetime | 更新时间 |

查询：对四条地址依次 `eq` 查询，**先命中先返回**（EVM → TRON → Bitcoin → Solana）；空地址跳过。

---

## 6. 多链验签算法（Go 必须对齐）

客户端对 `getSignKey` 返回的 **`str` 原文** 签名（非对 hash 再签，除非注明）。

### 6.1 EVM (`checkEvmAddress`)

1. `sign`：hex（可带 `0x`）
2. `messageHash = keccak256("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(str)) + str)`
3. 从签名取 `r`（前 32 字节）、`s`（次 32 字节）
4. `recovery id` 尝试 `0..3`，`ecrecover` 得公钥 → 地址
5. 与 `address` **忽略大小写**比较

> Java 入参有 `strHash`，但当前实现**未使用** `strHash`，只用 `str` 计算 personal_sign hash。

### 6.2 TRON (`checkTronAddress`)

1. 同 EVM 提取 `r/s`
2. 依次尝试前缀：
   - `"\x19TRON Signed Message:\n" + len + str`
   - `"\x19Ethereum Signed Message:\n" + len + str`
3. 各自 keccak256 后 recovery `0..3`
4. 公钥 → Ethereum 20 字节地址 → 前缀 `0x41` → Base58Check → 与 `address` **精确相等**

### 6.3 Bitcoin (`checkBitcoinAddress`)

1. `sign`：BIP-137 **base64** 消息签名（bitcoinj `signedMessageToKey`）
2. 从签名恢复公钥后，分别生成并比对：
   - 主网 Legacy P2PKH
   - 测试网 Legacy P2PKH
   - 主网 SegWit bech32
   - 测试网 SegWit bech32
3. 任一匹配即通过

### 6.4 Solana (`checkSolanaAddress`)

1. `address`：Ed25519 公钥的 **base58**
2. 若 `msgHash` 非空：消息字节 = `hexDecode(msgHash)`；否则 = `utf8(str)`
3. `sign`：hex 解码为签名字节
4. Ed25519 `Verify(pubkey, message, signature)`

> 客户端常见：`msgHash = hex(utf8(str))`，`sign = hex(ed25519Sign(utf8(str)))`。若走 `msgHash` 分支，验的是对 **msgHash 字节本身** 的签名（不是对 hash 的二次哈希）。

---

## 7. Go 实现检查清单

- [ ] Redis key / TTL 对齐：`walletTraceId:` + 600s
- [ ] 统一响应 `{code,data,msg}`，成功码 `200`
- [ ] 验签失败 / 缺链 / 缺 trace 错误语义对齐
- [ ] 四链可选、至少一条；未传链视为跳过
- [ ] 地址查库顺序：EVM → TRON → BTC → SOL
- [ ] 登录/注册分别调 Chat `/account/login` 与 `/account/register`
- [ ] Header `operationID` 为秒级时间戳
- [ ] 注册写库、登录更新地址
- [ ] EVM/TRON recovery 尝试 4 个 v；TRON 双前缀兼容
- [ ] BTC 多地址格式兼容；Solana Ed25519 + 可选 msgHash

**可选增强（当前 Java 未做，若产品要求“单次有效”）**：验签成功后 `DEL walletTraceId:{traceId}`。

---

## 8. 相关源码（Java 原文）

### 8.1 Controller：三接口 + 辅助方法

```java
// AppWalletController.java — 路径前缀 /sok/app/appWallet

@Operation(description = "APP钱包获取签名字符串", method = "POST", summary = "APP钱包获取签名字符串")
@RequestMapping(value = "/getSignKey", method = RequestMethod.POST)
public R<GetSignKeyRet> getSignKey() {

    GetSignKeyRet result = new GetSignKeyRet();

    String str = RandomUtil.randomString(10);
    String traceId = UUID.randomUUID().toString();

    redisTemplate.opsForValue().set(CacheConstants.WALLET_TRACE_ID + traceId, str, 10, TimeUnit.MINUTES);

    result.setStr(str);
    result.setTraceId(traceId);

    return R.data(result);
}


@Operation(description = "检查钱包地址是否已经注册过", method = "POST", summary = "检查钱包地址是否已经注册过")
@RequestMapping(value = "/checkWalletAddress", method = RequestMethod.POST)
public R<CheckWalletAddressRet> checkWalletAddress(@RequestBody @Valid CheckWalletAddressParams params) {

    CheckWalletAddressRet result = new CheckWalletAddressRet();

    //查询签名的key
    String str = redisTemplate.opsForValue().get(CacheConstants.WALLET_TRACE_ID + params.getTraceId());

    if (StringUtils.isEmpty(str)) {
        return R.fail("sign.key.error");
    }

    if (ObjectUtils.isEmpty(params.getEvm()) && ObjectUtils.isEmpty(params.getTron()) && ObjectUtils.isEmpty(params.getBitcoin()) && ObjectUtils.isEmpty(params.getSolana())) {
        return R.fail("least.transmit.one.piece.of.data");
    }

    //验证钱包地址
    String evmAddress = validChainAddress(params.getEvm(),
            () -> iUserLoginWalletService.checkEvmAddress(params.getEvm().getAddress(), str, params.getEvm().getMsgHash(), params.getEvm().getSign()));
    if (evmAddress == null) return R.fail("wallet.address.check.error");

    String tronAddress = validChainAddress(params.getTron(),
            () -> iUserLoginWalletService.checkTronAddress(params.getTron().getAddress(), str, params.getTron().getSign()));
    if (tronAddress == null) return R.fail("wallet.address.check.error");

    String bitcoinAddress = validChainAddress(params.getBitcoin(),
            () -> iUserLoginWalletService.checkBitcoinAddress(params.getBitcoin().getAddress(), str, params.getBitcoin().getSign()));
    if (bitcoinAddress == null) return R.fail("wallet.address.check.error");

    String solanaAddress = validChainAddress(params.getSolana(),
            () -> iUserLoginWalletService.checkSolanaAddress(params.getSolana().getAddress(), str, params.getSolana().getMsgHash(), params.getSolana().getSign()));
    if (solanaAddress == null) return R.fail("wallet.address.check.error");

    //查询对应链是否已存在数据
    UserLoginWallet userLoginWallet = iUserLoginWalletService.selectInfoByAddress(evmAddress, tronAddress, bitcoinAddress, solanaAddress);

    result.setIsRegister(ObjectUtils.isNotEmpty(userLoginWallet) ? BooleanEnum.yes.getValue() : BooleanEnum.no.getValue());

    return R.data(result);

}


@Operation(description = "APP登录/注册", method = "POST", summary = "APP登录/注册")
@RequestMapping(value = "/appLogin", method = RequestMethod.POST)
public R<AppLoginRet> appLogin(@RequestBody @Valid AppLoginParams params) {

    AppLoginRet result = new AppLoginRet();

    //查询签名的key
    String str = redisTemplate.opsForValue().get(CacheConstants.WALLET_TRACE_ID + params.getTraceId());

    if (StringUtils.isEmpty(str)) {
        return R.fail("sign.key.error");
    }

    if (ObjectUtils.isEmpty(params.getEvm()) && ObjectUtils.isEmpty(params.getTron()) && ObjectUtils.isEmpty(params.getBitcoin()) && ObjectUtils.isEmpty(params.getSolana())) {
        return R.fail("least.transmit.one.piece.of.data");
    }

    //验证钱包地址
    String evmAddress = validChainAddress(params.getEvm(),
            () -> iUserLoginWalletService.checkEvmAddress(params.getEvm().getAddress(), str, params.getEvm().getMsgHash(), params.getEvm().getSign()));
    if (evmAddress == null) return R.fail("wallet.address.check.error");

    String tronAddress = validChainAddress(params.getTron(),
            () -> iUserLoginWalletService.checkTronAddress(params.getTron().getAddress(), str, params.getTron().getSign()));
    if (tronAddress == null) return R.fail("wallet.address.check.error");

    String bitcoinAddress = validChainAddress(params.getBitcoin(),
            () -> iUserLoginWalletService.checkBitcoinAddress(params.getBitcoin().getAddress(), str, params.getBitcoin().getSign()));
    if (bitcoinAddress == null) return R.fail("wallet.address.check.error");

    String solanaAddress = validChainAddress(params.getSolana(),
            () -> iUserLoginWalletService.checkSolanaAddress(params.getSolana().getAddress(), str, params.getSolana().getMsgHash(), params.getSolana().getSign()));
    if (solanaAddress == null) return R.fail("wallet.address.check.error");

    //查询对应链是否已存在数据
    UserLoginWallet userLoginWallet = iUserLoginWalletService.selectInfoByAddress(evmAddress, tronAddress, bitcoinAddress, solanaAddress);

    JSONObject body = new JSONObject();
    body.put("platform", params.getPlatform());
    body.put("deviceID", params.getDeviceID());

    if (ObjectUtils.isNotEmpty(userLoginWallet)) {
        //走登录逻辑
        body.put("uid", userLoginWallet.getUserId());
        String httpResult = HttpRequest.post(businessConfig.getAppUserLogin())
                .header("operationID", Instant.now().getEpochSecond() + "")
                .body(body.toJSONString())
                .execute().body();
        if (StringUtils.isEmpty(httpResult)) {
            return R.fail();
        }

        JSONObject httpJson = JSONObject.parseObject(httpResult);
        if (httpJson.getIntValue("errCode") != 0) {
            return R.fail(httpJson.getString("errMsg"));
        }

        result.setChatToken(httpJson.getJSONObject("data").getString("chatToken"));
        result.setImToken(httpJson.getJSONObject("data").getString("imToken"));
        result.setUserID(userLoginWallet.getUserId());

        //更新钱包地址到数据库
        iUserLoginWalletService.lambdaUpdate()
                .eq(UserLoginWallet::getId, userLoginWallet.getId())
                .set(StringUtils.isNotEmpty(evmAddress), UserLoginWallet::getEvmAddress, evmAddress)
                .set(StringUtils.isNotEmpty(tronAddress), UserLoginWallet::getTronAddress, tronAddress)
                .set(StringUtils.isNotEmpty(bitcoinAddress), UserLoginWallet::getBitcoinAddress, bitcoinAddress)
                .set(StringUtils.isNotEmpty(solanaAddress), UserLoginWallet::getSolanaAddress, solanaAddress)
                .set(UserLoginWallet::getUpdatedTime, LocalDateTime.now())
                .update();

        log.info("登录成功");

    } else {
        //走注册逻辑
        body.put("invitationCode", params.getInvitationCode());
        body.put("autoLogin", true);
        body.put("user", new JSONObject() {{
            put("firstName", params.getFirstName());
            put("lastName", params.getLastName());
            put("language", params.getLanguage());
            put("gender", params.getGender());
        }});

        String httpResult = HttpRequest.post(businessConfig.getAppUserRegister())
                .header("operationID", Instant.now().getEpochSecond() + "")
                .body(body.toJSONString())
                .execute().body();
        if (StringUtils.isEmpty(httpResult)) {
            return R.fail();
        }

        JSONObject httpJson = JSONObject.parseObject(httpResult);
        if (httpJson.getIntValue("errCode") != 0) {
            return R.fail(httpJson.getString("errMsg"));
        }

        result.setChatToken(httpJson.getJSONObject("data").getString("chatToken"));
        result.setImToken(httpJson.getJSONObject("data").getString("imToken"));
        result.setUserID(httpJson.getJSONObject("data").getString("userID"));

        //写入钱包地址数据
        UserLoginWallet insertData = new UserLoginWallet();
        insertData.setUserId(httpJson.getJSONObject("data").getString("userID"));
        insertData.setEvmAddress(evmAddress);
        insertData.setTronAddress(tronAddress);
        insertData.setBitcoinAddress(bitcoinAddress);
        insertData.setSolanaAddress(solanaAddress);
        insertData.setUpdatedTime(LocalDateTime.now());
        iUserLoginWalletService.save(insertData);

        log.info("注册成功");

    }

    return R.data(result);
}


/**
 * 验证链上地址签名，返回地址（验证通过）或 null（验证失败）
 */
private String validChainAddress(AppLoginChainParams chainParams, BooleanSupplier checker) {
    if (ObjectUtils.isEmpty(chainParams)) {
        return "";
    }
    return checker.getAsBoolean() ? chainParams.getAddress() : null;
}
```

### 8.2 请求 / 响应 DTO

```java
// AppLoginChainParams.java
@Data
public class AppLoginChainParams {
    @NotBlank
    private String address;
    @NotBlank
    private String sign;
    private String msgHash; // 没有传空
}

// CheckWalletAddressParams.java
@Data
public class CheckWalletAddressParams {
    private AppLoginChainParams evm;
    private AppLoginChainParams tron;
    private AppLoginChainParams bitcoin;
    private AppLoginChainParams solana;
    @NotBlank
    private String traceId;
}

// AppLoginParams.java
@Data
public class AppLoginParams {
    @NotBlank
    private String deviceID;
    @Min(1)
    private int platform;
    private String invitationCode;
    private String firstName;
    private String lastName;
    private String language;
    private int gender;
    private AppLoginChainParams evm;
    private AppLoginChainParams tron;
    private AppLoginChainParams bitcoin;
    private AppLoginChainParams solana;
    @NotBlank
    private String traceId;
}

// GetSignKeyRet.java
@Data
public class GetSignKeyRet {
    private String str;      // 10分钟内有效
    private String traceId;
}

// CheckWalletAddressRet.java
@Data
public class CheckWalletAddressRet {
    private int isRegister = 0; // 1=是，0=否
}

// AppLoginRet.java（appWallet 包）
@Data
public class AppLoginRet {
    private String imToken;
    private String chatToken;
    private String userID;
}
```

### 8.3 Entity / Cache / Config

```java
// UserLoginWallet.java — Mongo collection: user_login_wallet
@Data
@CollectionName("user_login_wallet")
public class UserLoginWallet {
    @ID
    private String id;
    private String userId;
    private String evmAddress;
    private String tronAddress;
    private String bitcoinAddress;
    private String solanaAddress;
    private LocalDateTime updatedTime;
}

// CacheConstants.java
public static final String WALLET_TRACE_ID = "walletTraceId:";

// BusinessConfig.java
@Value("${business.app-user-register:}")
private String appUserRegister;
@Value("${business.app-user-login:}")
private String appUserLogin;
```

### 8.4 验签 Service 全文

```java
// UserLoginWalletServiceImpl.java
package cn.com.sok.common.model.service.impl;

import cn.com.sok.common.model.entity.UserLoginWallet;
import cn.com.sok.common.model.service.IUserLoginWalletService;
import cn.hutool.core.codec.Base58;
import cn.hutool.core.util.HexUtil;
import cn.hutool.crypto.digest.DigestUtil;
import com.mongoplus.service.impl.ServiceImpl;
import lombok.extern.slf4j.Slf4j;
import org.apache.commons.lang3.StringUtils;
import org.bitcoinj.core.ECKey;
import org.bitcoinj.core.LegacyAddress;
import org.bitcoinj.core.SegwitAddress;
import org.bitcoinj.params.MainNetParams;
import org.bitcoinj.params.TestNet3Params;
import org.springframework.stereotype.Service;
import org.web3j.crypto.Hash;
import org.web3j.crypto.Keys;
import org.web3j.crypto.Sign;
import org.web3j.utils.Numeric;

import java.math.BigInteger;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.PublicKey;
import java.security.spec.X509EncodedKeySpec;
import java.util.Arrays;

@Slf4j
@Service
public class UserLoginWalletServiceImpl extends ServiceImpl<UserLoginWallet> implements IUserLoginWalletService {

    private static final byte[] ED25519_X509_PREFIX = HexUtil.decodeHex("302a300506032b6570032100");

    @Override
    public boolean checkEvmAddress(String address, String str, String strHash, String sign) {
        try {
            byte[] signBytes = Numeric.hexStringToByteArray(sign);
            byte[] msgBytes = str.getBytes(StandardCharsets.UTF_8);

            byte[] ethPrefix = ("\u0019Ethereum Signed Message:\n" + msgBytes.length).getBytes(StandardCharsets.UTF_8);
            byte[] fullMsg = Arrays.copyOf(ethPrefix, ethPrefix.length + msgBytes.length);
            System.arraycopy(msgBytes, 0, fullMsg, ethPrefix.length, msgBytes.length);
            byte[] messageHash = Hash.sha3(fullMsg);

            BigInteger r = new BigInteger(1, Arrays.copyOfRange(signBytes, 0, 32));
            BigInteger s = new BigInteger(1, Arrays.copyOfRange(signBytes, 32, 64));

            for (int i = 0; i < 4; i++) {
                BigInteger publicKey = Sign.recoverFromSignature(i,
                        new org.web3j.crypto.ECDSASignature(r, s), messageHash);
                if (publicKey != null) {
                    String recoveredAddress = "0x" + Keys.getAddress(publicKey);
                    if (recoveredAddress.equalsIgnoreCase(address)) {
                        return true;
                    }
                }
            }
            return false;
        } catch (Exception e) {
            log.error("EVM address verification failed: address={}, str={}", address, str, e);
            return false;
        }
    }

    @Override
    public boolean checkSolanaAddress(String address, String str, String strHash, String sign) {
        try {
            byte[] pubKeyBytes = Base58.decode(address);

            byte[] x509PubKey = new byte[ED25519_X509_PREFIX.length + pubKeyBytes.length];
            System.arraycopy(ED25519_X509_PREFIX, 0, x509PubKey, 0, ED25519_X509_PREFIX.length);
            System.arraycopy(pubKeyBytes, 0, x509PubKey, ED25519_X509_PREFIX.length, pubKeyBytes.length);

            KeyFactory keyFactory = KeyFactory.getInstance("Ed25519");
            PublicKey publicKey = keyFactory.generatePublic(new X509EncodedKeySpec(x509PubKey));

            byte[] messageBytes;
            byte[] signatureBytes;
            if (strHash != null && !strHash.isEmpty()) {
                messageBytes = HexUtil.decodeHex(strHash);
            } else {
                messageBytes = str.getBytes(StandardCharsets.UTF_8);
            }
            signatureBytes = HexUtil.decodeHex(sign);

            java.security.Signature sig = java.security.Signature.getInstance("Ed25519");
            sig.initVerify(publicKey);
            sig.update(messageBytes);
            return sig.verify(signatureBytes);
        } catch (Exception e) {
            log.error("Solana address verification failed: address={}, str={}", address, str, e);
            return false;
        }
    }

    @Override
    public boolean checkTronAddress(String address, String str, String sign) {
        try {
            byte[] signBytes = Numeric.hexStringToByteArray(sign);
            byte[] msgBytes = str.getBytes(StandardCharsets.UTF_8);

            BigInteger r = new BigInteger(1, Arrays.copyOfRange(signBytes, 0, 32));
            BigInteger s = new BigInteger(1, Arrays.copyOfRange(signBytes, 32, 64));
            org.web3j.crypto.ECDSASignature ecSig = new org.web3j.crypto.ECDSASignature(r, s);

            String[] prefixes = {
                    "\u0019TRON Signed Message:\n",
                    "\u0019Ethereum Signed Message:\n"
            };

            for (String prefix : prefixes) {
                byte[] prefixWithLen = (prefix + msgBytes.length).getBytes(StandardCharsets.UTF_8);
                byte[] fullMsg = Arrays.copyOf(prefixWithLen, prefixWithLen.length + msgBytes.length);
                System.arraycopy(msgBytes, 0, fullMsg, prefixWithLen.length, msgBytes.length);
                byte[] messageHash = Hash.sha3(fullMsg);

                for (int i = 0; i < 4; i++) {
                    BigInteger publicKey = Sign.recoverFromSignature(i, ecSig, messageHash);
                    if (publicKey != null) {
                        String recoveredAddress = publicKeyToTronAddress(publicKey);
                        if (recoveredAddress.equals(address)) {
                            return true;
                        }
                    }
                }
            }
            return false;
        } catch (Exception e) {
            log.error("Tron address verification failed: address={}, str={}", address, str, e);
            return false;
        }
    }

    @Override
    public boolean checkBitcoinAddress(String address, String str, String sign) {
        try {
            ECKey ecKey = ECKey.signedMessageToKey(str, sign);

            String legacyMainnet = LegacyAddress.fromKey(MainNetParams.get(), ecKey).toString();
            if (legacyMainnet.equals(address)) {
                return true;
            }

            String legacyTestnet = LegacyAddress.fromKey(TestNet3Params.get(), ecKey).toString();
            if (legacyTestnet.equals(address)) {
                return true;
            }

            try {
                String segwitMainnet = SegwitAddress.fromKey(MainNetParams.get(), ecKey).toBech32();
                if (segwitMainnet.equals(address)) {
                    return true;
                }
            } catch (Exception ignored) {
            }

            try {
                String segwitTestnet = SegwitAddress.fromKey(TestNet3Params.get(), ecKey).toBech32();
                if (segwitTestnet.equals(address)) {
                    return true;
                }
            } catch (Exception ignored) {
            }

            return false;
        } catch (Exception e) {
            log.error("Bitcoin address verification failed: address={}, str={}", address, str, e);
            return false;
        }
    }

    private String publicKeyToTronAddress(BigInteger publicKey) {
        String ethAddress = Keys.getAddress(publicKey);

        byte[] addressBytes = new byte[21];
        addressBytes[0] = (byte) 0x41;
        byte[] ethAddressBytes = Numeric.hexStringToByteArray(ethAddress);
        System.arraycopy(ethAddressBytes, 0, addressBytes, 1, 20);

        byte[] hash1 = DigestUtil.sha256(addressBytes);
        byte[] hash2 = DigestUtil.sha256(hash1);
        byte[] checksum = Arrays.copyOfRange(hash2, 0, 4);

        byte[] tronAddressBytes = new byte[addressBytes.length + checksum.length];
        System.arraycopy(addressBytes, 0, tronAddressBytes, 0, addressBytes.length);
        System.arraycopy(checksum, 0, tronAddressBytes, addressBytes.length, checksum.length);

        return Base58.encode(tronAddressBytes);
    }

    @Override
    public UserLoginWallet selectInfoByAddress(String evmAddress, String tronAddress, String bitcoinAddress, String solanaAddress) {
        UserLoginWallet info = findByAddress(UserLoginWallet::getEvmAddress, evmAddress);
        if (info != null) return info;
        info = findByAddress(UserLoginWallet::getTronAddress, tronAddress);
        if (info != null) return info;
        info = findByAddress(UserLoginWallet::getBitcoinAddress, bitcoinAddress);
        if (info != null) return info;
        return findByAddress(UserLoginWallet::getSolanaAddress, solanaAddress);
    }

    private UserLoginWallet findByAddress(com.mongoplus.support.SFunction<UserLoginWallet, Object> field, String address) {
        if (StringUtils.isEmpty(address)) {
            return null;
        }
        return lambdaQuery().eq(field, address).one();
    }
}
```

### 8.5 源文件路径索引

| 内容 | 路径 |
|------|------|
| Controller | `app-api-module/src/main/java/cn/com/sok/app/controller/AppWalletController.java` |
| 验签 Service | `common-module/src/main/java/cn/com/sok/common/model/service/impl/UserLoginWalletServiceImpl.java` |
| Service 接口 | `common-module/src/main/java/cn/com/sok/common/model/service/IUserLoginWalletService.java` |
| Entity | `common-module/src/main/java/cn/com/sok/common/model/entity/UserLoginWallet.java` |
| 请求 DTO | `app-api-module/src/main/java/cn/com/sok/app/request/appWallet/*.java` |
| 响应 DTO | `app-api-module/src/main/java/cn/com/sok/app/response/appWallet/{GetSignKeyRet,CheckWalletAddressRet,AppLoginRet}.java` |
| Redis Key | `common-module/src/main/java/cn/com/sok/common/constants/CacheConstants.java` |
| Chat URL 配置 | `config/application-*.yml` → `business.app-user-login` / `app-user-register` |

---

## 9. 客户端签名约定（参考 Flutter）

来源：`sok-im-flutter/lib/login/wallet_mnemonic_signer.dart`

| 链 | 派生路径 | 签名算法 | `sign` 编码 | `msgHash` |
|----|----------|----------|-------------|-----------|
| EVM | `m/44'/60'/0'/0/0` | personal_sign (EIP-191) | `0x` hex | `0x` + keccak256(prefix+msg) |
| TRON | `m/44'/195'/0'/0/0` | TronSigner personal message | `0x` hex | 通常空 |
| Bitcoin | `m/44'/0'/0'/0/0` Legacy | BIP-137 | base64 | 通常空 |
| Solana | mnemonic 默认 Ed25519 路径 | Ed25519 签 utf8(str) | hex（无 0x） | hex(utf8(str)) |

`checkWalletAddress` 与 `appLogin` 各需一次**全新** `getSignKey` + 签名（客户端约定；Java 侧未强制删 key）。
