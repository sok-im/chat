# MongoDB 集合说明（openim/chat）

本文档根据仓库内 `pkg/common/db/model/**` 中 `db.Collection(...)` 与 `pkg/common/db/table/**` 的 BSON 标签整理。**实际库名**由部署配置中的 Mongo 连接串决定；Chat RPC 与 Admin RPC 通常指向**同一 Mongo 数据库**，下列集合共存于该库中。

> 说明：表中「类型」为 Go 侧语义类型；落库时 `time.Time` 为 BSON Date，`bool` 为布尔，数字为对应 BSON 整数。

---

## 总览

| 集合名 | 服务域 | 主要职责 |
|--------|--------|----------|
| `account` | Chat | 用户登录密码（按 user_id） |
| `attribute` | Chat | 用户资料与账号展示信息 |
| `credential` | Chat | 登录凭证（账号 / 手机 / 邮箱与 user_id 映射） |
| `register` | Chat | 用户注册审计（设备、IP、平台等） |
| `user_login_record` | Chat | 用户登录流水 |
| `verify_code` | Chat | 手机/邮箱验证码记录 |
| `forbidden_account` | Chat（库表定义在 admin 包） | 封禁用户 |
| `admin` | Admin | 管理员账号 |
| `application` | Admin | 客户端应用版本发布信息 |
| `applet` | Admin | 小程序/热更新包元数据 |
| `client_config` | Admin | 客户端键值配置 |
| `invitation_register` | Admin | 邀请码及使用记录 |
| `ip_forbidden` | Admin | IP 黑名单（注册/登录限制） |
| `limit_user_login_ip` | Admin | 用户允许登录的 IP 白名单式限制 |
| `register_add_friend` | Admin | 新用户注册后默认添加的好友 user_id |
| `register_add_group` | Admin | 新用户注册后默认加入的群组 ID |

---

## Chat 域集合

### `account`

**用途**：存储终端用户的密码哈希及变更信息。

**索引**

- `user_id`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键；结构体未显式声明时由驱动生成 |
| `user_id` | string | OpenIM 用户 ID，业务主键 |
| `password` | string | 密码（存储形式由业务决定，一般为哈希） |
| `create_time` | date | 创建时间 |
| `change_time` | date | 最近修改密码时间 |
| `operator_user_id` | string | 操作者（如管理员改密场景） |

**代码参考**：`pkg/common/db/table/chat/account.go`，`pkg/common/db/model/chat/account.go`

---

### `attribute`

**用途**：用户对外资料（昵称、头像、手机、邮箱等）及消息相关偏好。

**索引**

- `user_id`：唯一
- `account`
- `email`
- 复合：`area_code` + `phone_number`

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `user_id` | string | 用户 ID |
| `account` | string | 登录用账号名（若使用账号注册） |
| `phone_number` | string | 手机号（不含区号部分，与区号字段组合使用） |
| `area_code` | string | 手机区号（如 `+86`） |
| `email` | string | 邮箱 |
| `nickname` | string | 昵称 |
| `first_name` | string | 名 |
| `last_name` | string | 姓 |
| `remark` | string | 备注 |
| `face_url` | string | 头像 URL |
| `gender` | int32 | 性别等业务枚举 |
| `create_time` | date | 创建时间 |
| `change_time` | date | 资料变更时间 |
| `birth_time` | date | 生日 |
| `level` | int32 | 用户等级 |
| `allow_vibration` | int32 | 是否允许振动等通知偏好 |
| `allow_beep` | int32 | 是否允许提示音 |
| `allow_add_friend` | int32 | 加好友策略相关 |
| `global_recv_msg_opt` | int32 | 全局接收消息选项（与 OpenIM 协议一致） |
| `register_type` | int32 | 注册类型 |
| `use_sn_code` | bool | 是否使用 SN 等扩展逻辑 |

**代码参考**：`pkg/common/db/table/chat/attribute.go`

---

### `credential`

**用途**：将「可登录标识」（账号/手机/邮箱）映射到 `user_id`；同一用户可多条（按 `type` 区分）。

**索引**

- 复合唯一：`user_id` + `type`
- `account`：唯一（**部分索引**：仅当 `type` 为账号或邮箱类型时生效，见代码常量）

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `user_id` | string | 用户 ID |
| `account` | string | 该凭证下的登录名/规范化手机/邮箱等 |
| `type` | int | 凭证类型：`0` CredentialAccount（账号）、`1` CredentialPhone（手机）、`2` CredentialEmail（邮箱），定义见 `pkg/common/constant/constant.go` |
| `allow_change` | bool | 是否允许用户自行更换该凭证 |

**代码参考**：`pkg/common/db/table/chat/credential.go`，`pkg/common/db/model/chat/credential.go`

---

### `register`

**用途**：用户注册行为记录，用于统计等。

**索引**

- `user_id`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `user_id` | string | 新注册用户 ID |
| `device_id` | string | 设备 ID |
| `ip` | string | 注册 IP |
| `platform` | string | 平台名称（由平台 ID 转换） |
| `account_type` | string | 账号类型，如 `phone` / `email` / `account`（见 `pkg/common/constant`） |
| `mode` | string | `user` / `admin` 等模式 |
| `create_time` | date | 注册时间 |

**代码参考**：`pkg/common/db/table/chat/register.go`

---

### `user_login_record`

**用途**：用户每次成功登录一条记录，用于登录统计、按日聚合等。

**索引**

- `create_time`：普通索引（模型层创建；**当前写入逻辑仅设置 `login_time` 等字段**，若依赖 `create_time` 统计需与实现保持一致）

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `user_id` | string | 用户 ID |
| `login_time` | date | 登录时间 |
| `ip` | string | 登录 IP |
| `device_id` | string | 设备 ID |
| `platform` | string | 平台名称 |

**代码参考**：`pkg/common/db/table/chat/user_login_record.go`，`internal/rpc/chat/login.go`（写入）

---

### `verify_code`

**用途**：短信/邮件验证码发送记录；校验次数、是否已用、有效期等与业务配置相关。

**索引**

- `account`：普通索引（按账号查最近一条、计数等）

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId | 验证码记录 ID；业务层对外为 hex 字符串 |
| `account` | string | 接收方标识：邮箱字符串，或手机侧 `areaCode + " " + phoneNumber` 拼接形式（见登录逻辑） |
| `platform` | string | 请求端平台描述 |
| `code` | string | 验证码内容 |
| `duration` | uint | 有效时长（秒） |
| `count` | int | 校验错误累计等 |
| `used` | bool | 是否已使用 |
| `create_time` | date | 创建时间 |

**代码参考**：`pkg/common/db/model/chat/verify_code.go`（落库结构 `mongoVerifyCode`）

---

### `forbidden_account`

**用途**：被封禁用户列表；Chat 侧搜索「正常用户」等会排除这些 `user_id`。

**索引**

- `user_id`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `user_id` | string | 被封用户 ID |
| `reason` | string | 封禁原因 |
| `operator_user_id` | string | 操作管理员 |
| `create_time` | date | 封禁时间 |

**代码参考**：`pkg/common/db/table/admin/forbidden_account.go`（表定义），`pkg/common/db/model/admin/forbidden_account.go`

---

## Admin 域集合

### `admin`

**用途**：后台管理员账号与资料。

**索引**

- `account`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `account` | string | 管理员登录名 |
| `password` | string | 密码 |
| `face_url` | string | 头像 |
| `nickname` | string | 昵称 |
| `user_id` | string | 管理员在 IM 体系中的 user_id |
| `level` | int32 | 管理员级别 |
| `create_time` | date | 创建时间 |

**代码参考**：`pkg/common/db/table/admin/admin.go`

---

### `application`

**用途**：各平台客户端安装包/升级包版本管理（最新版、是否强更等）。

**索引**

- 复合唯一：`platform` + `version`
- `latest`：降序，配合查询最新版本

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId | 记录主键 |
| `platform` | string | 平台标识（如 iOS/Android） |
| `hot` | bool | 是否热更新包等语义 |
| `version` | string | 版本号 |
| `url` | string | 下载地址 |
| `text` | string | 更新说明等文案 |
| `force` | bool | 是否强制升级 |
| `latest` | bool | 是否为当前最新发布条目（业务维护） |
| `create_time` | date | 创建时间 |

**代码参考**：`pkg/common/db/table/admin/application.go`

---

### `applet`

**用途**：小程序或内置 H5 包等资源管理（上架、版本、大小等）。

**索引**

- `id`：唯一（业务侧字符串 ID，非 Mongo 默认 `_id` 字段名）

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | 若插入时未指定则自动生成 |
| `id` | string | 业务主键（与索引一致） |
| `name` | string | 名称 |
| `app_id` | string | 应用 ID |
| `icon` | string | 图标 |
| `url` | string | 资源地址 |
| `md5` | string | 文件校验 |
| `size` | int64 | 大小（字节） |
| `version` | string | 版本 |
| `priority` | uint32 | 排序优先级 |
| `status` | uint8 | 上架状态（如 `StatusOnShelf` / `StatusUnShelf`，见常量包） |
| `create_time` | date | 创建时间 |

**代码参考**：`pkg/common/db/table/admin/applet.go`

---

### `client_config`

**用途**：键值型客户端配置（如功能开关、文案、后台下发的业务参数）。

**索引**

- `key`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `key` | string | 配置键 |
| `value` | string | 配置值（多为字符串化存储） |

**代码参考**：`pkg/common/db/table/admin/client_config.go`

---

### `invitation_register`

**用途**：邀请码及被哪个用户使用；用于「需邀请码注册」等策略。

**索引**

- `invitation_code`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `invitation_code` | string | 邀请码 |
| `used_by_user_id` | string | 使用者用户 ID（未使用可为空，视业务写入） |
| `create_time` | date | 创建时间 |

**代码参考**：`pkg/common/db/table/admin/invitation_register.go`

---

### `ip_forbidden`

**用途**：对指定 IP 限制注册、登录或二者。

**索引**

- `ip`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `ip` | string | IPv4/IPv6 字符串 |
| `limit_register` | bool | 是否限制注册 |
| `limit_login` | bool | 是否限制登录 |
| `create_time` | date | 创建时间 |

**代码参考**：`pkg/common/db/table/admin/ip_forbidden.go`

---

### `limit_user_login_ip`

**用途**：限制指定用户只能从某些 IP 登录（白名单式多条记录）。

**索引**

- 复合唯一：`user_id` + `ip`

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `user_id` | string | 用户 ID |
| `ip` | string | 允许的 IP |
| `create_time` | date | 添加时间 |

**代码参考**：`pkg/common/db/table/admin/limit_user_login_ip.go`

---

### `register_add_friend`

**用途**：新用户注册成功后，系统为其自动发起添加好友的**目标用户 ID** 列表（默认好友）。

**索引**

- `user_id`：唯一（每个「待自动添加的好友」用户 ID 一条）

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `user_id` | string | 要被自动添加为好友的对方 user_id |
| `create_time` | date | 配置创建时间 |

**代码参考**：`pkg/common/db/table/admin/register_add_friend.go`，`internal/rpc/admin/register_add_friend.go`

---

### `register_add_group`

**用途**：新用户注册成功后自动加入的群组 ID 列表。

**索引**

- `group_id`：唯一

**字段**

| BSON 字段 | 类型 | 说明 |
|-----------|------|------|
| `_id` | ObjectId（可选） | Mongo 默认主键 |
| `group_id` | string | OpenIM 群组 ID |
| `create_time` | date | 配置创建时间 |

**代码参考**：`pkg/common/db/table/admin/register_add_group.go`

---

## 维护说明

1. **集合名以 `pkg/common/db/model/**` 中 `db.Collection("...")` 为准**；`TableName()` 部分返回的复数英文（如 `accounts`）在 Mongo 实现中**未用作集合名**，仅作接口命名习惯。
2. 新增集合或字段时，请同步更新本文件与相关 `Indexes().Create*` 逻辑。
3. 凭证类型、账号类型、平台名等枚举以 `pkg/common/constant` 及协议层常量为准。
