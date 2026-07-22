# FinClip 小程序容器集成设计

日期：2026-07-22  
范围：在 SOK Flutter App（IM + Wallet）中接入 [FinClip](https://finclip.com/) 小程序运行时，替换/扩展当前二楼「WebView 伪小程序」能力，并明确**客户端、业务后端、FinClip 平台**三方职责。

---

## 0. 评审结论与决策记录

本轮 review 后，建议以「**FinClip 负责包分发与运行；SOK 只负责业务目录、访问控制和 SOK 会话桥**」作为 v1 边界。以下结论已合并到本文，实施前不得绕过。

| 编号 | 结论 | 原因与行动 |
|------|------|------------|
| D1 | 采用“FinClip 运行时 + SOK 统一目录” | 现有二楼已经同时承载 DApp；用统一 `entryType` 才能避免两套入口、收藏与埋点。 |
| D2 | SDK KEY/SECRET **不是服务端秘密** | 任何编译进 iOS/Android 安装包的值（包括 `dart-define`）均可能被逆向取得。CI 注入只防止泄露到 Git，不能作为运行时保密措施；安全边界必须依赖 BundleID/签名校验、FinClip 控制台权限、AppID 白名单和密钥轮换。 |
| D3 | v1 不允许小程序取得 IM 长期 token、钱包私钥或助记词 | 小程序只能取得绑定 `appId`、短 TTL、可撤销的业务 ticket。钱包签名/支付另立方案，不随本设计上线。 |
| D4 | 目录“可见”不等于“可打开” | 客户端缓存可过期；每次打开和签发会话均须由后端再次检查条目状态、版本、灰度和用户权限。 |
| D5 | 所有 `mop` 高级接口先做 PoC | 已确认当前插件文档列出 `initSDK`、`openApplet`、`closeAllApplets`、`clearApplets`、`registerAppletHandler`、`registerExtensionApi`；事件载荷、关闭回调、动态更新 `userId` 的具体语义须按**锁定版本**真机验证后再写死实现。 |

### 0.1 实施前置条件（Go / No-Go）

1. FinClip 商务/运维确认 Flutter SDK 的许可证、iOS/Android 最低系统版本、包体增量、隐私清单和目标架构兼容当前应用。
2. 分别用**生产签名真机**验证 iOS、Android：初始化、打开、关闭、网络失败、后台恢复及 SDK 升级后缓存行为。
3. 确认小程序的服务端、数据落地地区、日志留存与 SOK 隐私政策、数据分类要求一致。
4. 业务后端确认可对小程序业务后端开放服务到服务认证；禁止把 `introspect` 作为匿名公网接口。

---

## 1. 背景与目标

### 1.1 背景

- [FinClip](https://finclip.com/) 提供小程序容器 SDK：App 集成后可在沙箱中运行兼容微信语法的小程序，业务与 App 发版解耦。
- Flutter 侧官方插件为 [`mop`](https://pub.dev/packages/mop)（[mop-flutter-sdk](https://github.com/finogeeks/mop-flutter-sdk)），文档见 [Flutter 集成](https://www.finclip.com/mop/document/zh/runtime-sdk/flutter/flutter-integrate.html)。
- 本仓库现状：
  - 会话页二楼 `SecondFloorPanel` 展示 **DApp 列表**（`SecondFloorDappData` mock），点击走 `AppWebViewPage`（`conversation_view.dart` → `_openSecondFloorDapp`）。
  - 另有遗留 `lib/im/pages/second_floor/mini_program.dart`：本地硬编码目录 + `webUrl`，**未接入 FinClip**，也未与当前二楼主路径打通。
- App 包名 / Bundle ID：`com.dcb.sok.tgplTeam`（Android `applicationId`、iOS `PRODUCT_BUNDLE_IDENTIFIER`）。
- 业务 API 基址：`AppConfig.SOK_BASE_URL`（当前测试域 `https://test.api.sok-app.com`）。

### 1.2 目标

1. App 内可**安全、可控**地打开 FinClip 托管的小程序（启动、传参、关闭、清缓存）。
2. 二楼/发现页支持 **DApp（H5）与小程序（FinClip）统一目录**，由后端下发，支持分类、搜索、最近使用、收藏。
3. 小程序可调用受控宿主能力（登录态、用户基础信息、关闭）；经**扩展 API + 后端鉴权**完成，且不暴露长期凭据或钱包敏感数据。
4. 运营可在 FinClip 控制台 + 业务后台完成：上架、下架、灰度、绑定到 App 应用。

### 1.3 非目标（v1）

1. 不自研小程序 IDE / 不替代 FinClip Studio 与开放平台。
2. 不做完整「微信小程序商店」级审核流水线（可后续接运营后台）。
3. 不在 v1 强制私有化部署 FinClip 服务端（默认 SaaS `https://api.finclip.com`；私有化仅预留配置项）。
4. 不把现有全部 DApp 迁移为小程序；二者长期并存。
5. 不在本阶段改造 HarmonyOS / 桌面端（仅 iOS + Android）。

### 1.4 设计假设（待确认）

| # | 假设 | 若否需调整 |
|---|------|------------|
| A1 | 使用 FinClip **SaaS**，凭据在 FinClip 控制台按 iOS/Android 应用分别申请 | 私有化则改 `apiServer`、网络边界与运维流程 |
| A2 | 小程序目录、权限、灰度由 **SOK 业务后端**权威下发；FinClip 负责运行时与包分发 | 仅用 FinClip 市场则可弱化业务目录 API |
| A3 | 用户打开小程序前需 **已登录 IM**（可拿到 `userID`）；游客策略与钱包白名单解耦，v1 默认禁止游客开小程序 | 若允许游客，需单独匿名 `userId` 策略 |
| A4 | 首期扩展 API 以「登录态 / 用户基础信息 / 关闭小程序」为主；支付、扫码、分享为 Phase 2 | — |
| A5 | 目录 API 认证与错误信封沿用 SOK 已有 IM API 客户端约定 | App 接口建议使用现有 `/sok/app/appIm` 前缀；运营后台与内部服务前缀须由后端确认 |

---

## 2. 总体架构

系统按四个模块划分。FinClip 管理平台与 SOK 运营平台是配置与发布侧；SOK IM 后端是运行时权威；SOK Flutter App 是唯一面向用户的执行端。

```
┌──────────────────────────────┐     ┌──────────────────────────────────┐
│     FinClip 管理平台           │     │         SOK 运营平台               │
│  - 应用 / BundleID / 凭据     │     │  - 入口 / 分类 / 排序 / 文案        │
│  - 小程序包上传与版本发布       │     │  - SOK 用户灰度 / minAppVersion    │
│  - 包分发、平台侧灰度与回滚     │     │  - 上架 / 紧急下架 / 回滚 / 审计   │
│  - 开发者与数据管理中心         │     │  - 发布 revision 与配置快照         │
└──────────────┬───────────────┘     └────────────────┬─────────────────┘
               │ finclipAppId / 包状态                  │ 发布事件 / 配置读模型
               │                                       │
               ▼                                       ▼
┌──────────────────────────────────────────────────────────────────────┐
│                           SOK IM 后端                                 │
│  App API：catalog / launch / session.issue / recent / favorites       │
│  内部 API：session.introspect / session.revoke                        │
│  - 消费运营平台发布 revision，执行用户权限与灰度                         │
│  - 签发 / 校验 / 吊销短期业务 ticket                                   │
│  - 维护用户收藏、最近与运行时开关                                       │
└──────────────────────────────────┬───────────────────────────────────┘
                                   │ HTTPS App API
                                   ▼
┌──────────────────────────────────────────────────────────────────────┐
│                         SOK Flutter App                               │
│  二楼 / 发现 / 消息卡片入口                                            │
│       │                                                               │
│       ▼                                                               │
│  MiniProgramFacade（统一打开：type=dapp|finclip）                       │
│       │                                                               │
│       ├─ dapp  ──► AppWebViewPage（现有）                               │
│       └─ finclip ──► mop SDK ──► FinClip 小程序沙箱                     │
│              ▲                                                        │
│              │ initSDK(KEY/SECRET/apiServer)  [构建期注入，非 IM API]   │
│              │ registerExtensionApi → IM session.issue                 │
└──────────────────────────────────────────────────────────────────────┘
```

数据与调用边界：

1. FinClip 管理平台只对运维/开发者与 `mop` SDK 暴露能力；不直接服务 SOK App 业务 HTTP。
2. SOK 运营平台只对运营控制台暴露管理 API；不向 Flutter App 开放。
3. SOK IM 后端是 App 的唯一业务后端入口，同时提供受保护的内部服务接口。
4. SOK Flutter App 只调用 IM App API，并通过 `mop` 运行时直连 FinClip；不得调用运营平台或 FinClip 管理 API。

### 2.1 四模块职责划分

| 模块 | 权威数据 | 主要职责 | 对外接口 | 禁止事项 |
|------|----------|----------|----------|----------|
| **FinClip 管理平台** | 应用、BundleID、SDK 凭据、小程序包、版本与平台发布状态 | 小程序生命周期、包分发、平台侧灰度/回滚、平台数据 | FinClip 控制台 / 官方管理 API；`mop` 运行时 `apiServer` | 不签发 SOK ticket；不决定 SOK 入口排序与用户灰度 |
| **SOK 运营平台** | 入口条目、分类、展示配置、SOK 用户灰度、运营 `revision` | 管理“在 SOK App 中向谁展示什么”；发布、紧急下架、回滚、审计 | `/sok/admin/miniProgram/**` | 不上传/托管小程序包；不向 App 签发 ticket；不承接 App 流量 |
| **SOK IM 后端** | 运行时目录快照、用户收藏/最近、业务 ticket | 目录、打开授权、短期会话、用户态同步；对业务后端提供 introspect/revoke | `/sok/app/appIm/miniProgram/**`；`/sok/internal/miniProgram/**` | 不编辑运营草稿；不调用 FinClip 控制台管理小程序；不下发 SDK SECRET |
| **SOK Flutter App** | SDK 初始化状态、本地目录缓存、UI 状态 | 渲染入口、调用 IM API、运行 FinClip SDK、宿主扩展 API | 仅作为调用方；对内使用 `mop` 与本地门面 | 不调用运营平台 / IM 内部 API / FinClip 管理 API；不替服务端做授权判断 |

协作关系（发布 → 运行）：

```text
FinClip 管理平台：发布小程序包，产出 finclipAppId / 可用版本
        │
        ▼
SOK 运营平台：登记 AppID，配置入口与 SOK 用户灰度，发布 revision
        │
        ▼
SOK IM 后端：消费 revision，对 App 强制执行 catalog / launch / ticket
        │
        ▼
SOK Flutter App：展示入口 → launch → mop.openApplet → FinClip 沙箱
```

可打开条件：`FinClip 包已发布可分发` **且** `SOK 运营平台条目 online` **且** `IM 后端判定当前用户满足权限/灰度/版本`。

### 2.2 推荐方案（相对另两种）

| 方案 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| **A. 四模块混合（推荐）** | FinClip 管包；SOK 运营平台管入口；IM 后端管运行时授权；Flutter 执行 | 与现有二楼一致，灰度与安全边界清晰 | 需维护 SOK 业务目录 |
| B. 纯 FinClip 市场 | App 只 init + 打开 FinClip 市场/固定 AppID | 后端改动小 | 与现有 DApp 二楼难统一、SOK 用户运营弱 |
| C. 全部 H5 WebView | 继续现有方式 | 无 SDK 成本 | 无真正小程序能力、非目标 |

**推荐 A**：与当前二楼 DApp 模型对齐，仅增加 `entryType=finclip`，并严格按四模块边界落地。

### 2.3 模块功能与接口明细

本文按以下四个模块展开。每项数据只允许一个权威来源；模块之间只能通过声明的接口或发布事件协作。

| 模块 | 权威数据 / 状态 | 核心职责 | 不负责 |
|------|-----------------|----------|--------|
| **FinClip 管理平台** | FinClip 应用、BundleID、SDK 凭据、小程序包、版本与发布状态 | 管理小程序生命周期与包分发 | SOK 用户权限、SOK 入口排序、SOK ticket |
| **SOK 运营平台** | SOK 入口条目、分类、展示配置、SOK 用户灰度、运营发布 revision | 管理“在 SOK App 中向谁展示什么” | 上传或托管小程序包、签发用户 ticket |
| **SOK IM 后端** | 用户收藏/最近、SOK ticket、运行时目录快照 | 向 App 提供目录和打开授权；执行运营配置与用户权限 | 编辑运营配置、调用 FinClip 控制台管理小程序 |
| **SOK Flutter App** | SDK 初始化状态、本地目录缓存、UI 状态 | 渲染入口、调用 IM API、运行 FinClip SDK、实现宿主扩展 API | 调用运营后台、持有 FinClip 控制台凭据、替服务端做授权判断 |

#### 2.3.1 模块 A：FinClip 管理平台

| 需实现 / 配置功能 | 输入 | 输出 / 对接对象 | 接口设计与约束 |
|-------------------|------|-----------------|----------------|
| 创建应用并绑定 iOS/Android BundleID | SOK 包名、环境 | 分端 SDK KEY/SECRET、`apiServer` | 通过 FinClip 控制台或其官方服务端 API 操作；SOK Flutter App 不调用管理 API。 |
| 小程序开发者、AppID 与包版本管理 | 小程序代码、版本说明、审核资料 | `finclipAppId`、已发布版本、分发状态 | FinClip 是包版本权威；SOK 运营平台仅登记 AppID 和展示配置。 |
| 小程序发布、回滚、平台侧灰度 | 小程序版本、目标分发规则 | 可由 SDK 下载/运行的小程序包 | 使用 FinClip 平台能力，具体灰度字段和 API 以购买版本/官方文档为准。 |
| 运行时包下载、校验与启动 | SDK 初始化配置、`finclipAppId` | 小程序原生容器页面 | `mop.initSDK` / `mop.openApplet` 由 Flutter 触发，SDK 与 `apiServer` 通信；不经 SOK IM HTTP API 代理。 |
| 平台数据与操作审计 | SDK/分发运行数据 | FinClip 数据管理中心、平台审计 | 与 SOK 业务埋点互补，不混作 SOK 用户行为的唯一来源。 |

**对外接口**

1. **管理接口**：FinClip 控制台或官方服务端 API，仅 SOK 运维自动化服务/授权运营人员使用。
2. **运行时接口**：Flutter `mop` SDK 使用 `SDK KEY/SECRET + apiServer` 初始化并以 `finclipAppId` 打开小程序。
3. **向 SOK 交付的数据**：`finclipAppId`、可用环境、发布状态、版本信息；不向 SOK App 或 SOK 运营平台下发 FinClip 控制台账号凭据。

#### 2.3.2 模块 B：SOK 运营平台

> 运营平台是**配置与发布权威**：定义入口、灰度规则、上下架；不承接 App 流量，不签发 ticket。

| 需实现功能 | 权威角色 | 核心数据 | 运营平台 API（建议） | 发布给 IM 后端的数据 |
|------------|----------|----------|----------------------|----------------------|
| 小程序 / DApp 入口管理 | 配置权威 | `entryId`、`entryType`、名称、图标、描述、分类、排序、`finclipAppId` 登记 | `POST/GET/PATCH /sok/admin/miniProgram/entries` | 已发布 `SecondFloorEntry`、`revision` |
| FinClip AppID 登记校验 | 配置权威 | 环境、`finclipAppId`、备注；不托管包文件 | 入口创建/发布时校验 | 可供 IM 运行时校验的 AppID allowlist |
| SOK 用户灰度与版本门槛 | 规则权威 | 白名单/百分比、地区、角色、`minAppVersion`；不含 `status` | `PUT /sok/admin/miniProgram/entries/{id}/rollout` | 可见性规则、版本门槛与生效时间 |
| 上架、紧急下架、恢复、回滚 | 生命周期权威 | `status`、草稿 revision、操作人、原因、审批记录 | `publish` / `suspend` / `resume` / `rollback` | 当前线上 revision、`status` 变更事件 |
| 分类与运营展示 | 配置权威 | 分类、Banner/角标（后续） | `/sok/admin/miniProgram/categories` | 分类和排序快照 |
| 产品运行开关（可选） | 配置权威 | `enabled`、`catalogEnabled` 等产品开关 | 运营配置项，经发布同步 | 供 IM `runtimeConfig` 下发的产品开关快照 |
| 操作审计与处置 | 审计权威 | 操作日志、发布历史、下架原因 | `/sok/admin/miniProgram/auditLogs` | 审计记录不下发给 App；失败率等告警信号来自 IM，运营据此人工 `suspend` |

**发布接口设计**

```text
运营平台 publish(entryId, draftRevision)
  → 校验 FinClip AppID 已登记且在目标环境可用
  → 写入不可变发布快照与 audit log
  → 发送 MiniProgramCatalogPublished(revision, affectedEntryIds) 事件
  → IM 后端失效目录缓存并按新 revision 提供运行时数据
```

运营平台**不能**直接为 App 签发 ticket，也不能仅通过隐藏 UI 来实现安全下架；下架事件必须使 IM 后端的 `launch` / `session/issue` 校验立即拒绝。

#### 2.3.3 模块 C：SOK IM 后端

> IM 后端是**运行时执行权威**：消费运营已发布配置，按当前用户强制执行可见性/打开授权，并维护用户态与 ticket。不编辑运营草稿。

| 需实现功能 | 权威角色 | App API / 内部 API | 关键输入 | 返回 / 行为 |
|------------|----------|-------------------|----------|-------------|
| 目录查询与搜索 | 执行权威 | `GET /sok/app/appIm/miniProgram/catalog` | 用户登录态、分类、关键词、ETag | **执行**运营已发布 revision 与灰度规则后，返回当前用户可见入口；规则本身不由 IM 编辑。 |
| 打开前授权 | 执行权威 | `POST /sok/app/appIm/miniProgram/entries/{entryId}/launch` | 登录态、客户端版本、平台、source | 再次检查在线、灰度、版本、权限，返回短寿命 FinClip/DApp launch 配置。 |
| 小程序业务会话 | ticket 权威 | `POST /sok/app/appIm/miniProgram/session/issue` | 登录态、`entryId`、`finclipAppId`、installId | 返回短 TTL、绑定 AppID/entry 的 ticket；不返回 IM 长期 token。 |
| ticket 校验 / 吊销 | ticket 权威 | `POST /sok/internal/miniProgram/session/introspect`、`revoke` | 服务凭据、ticket 或吊销维度 | 仅服务间调用，返回伪匿名 subject 与最小 scope；登出/下架时吊销。 |
| 用户收藏与最近 | 用户态权威 | `/sok/app/appIm/miniProgram/favorites`、`recent` | 登录态、entry 集合或打开记录 | 用户维度持久化、revision 冲突处理、最多 50 条最近记录。 |
| 运行时配置下发 | 下发执行 | `GET .../runtimeConfig` | App 版本、登录态 | 向 App 下发开关：产品开关来自运营同步快照；技术开关（如 `cacheTtlSeconds`）可由 IM 维护。 |
| 用户行为埋点采集 | 遥测采集 | `POST .../events` | 事件白名单、平台、耗时、标准化错误码 | 异步写入 SOK 埋点，不阻塞打开；运营看板只读聚合结果，不经本接口写配置。 |
| 订阅运营发布（内部） | 配置消费者 | 内部订阅 / 读模型（非 App API、非运营 API） | 运营平台发布事件 | 原子更新运行时目录读模型、清理缓存；不允许 IM 修改运营草稿。 |

**对外接口边界**

- Flutter 只调用 `/sok/app/appIm/miniProgram/**`。
- 小程序业务后端只能经 mTLS/OAuth2 调用 IM 后端的 `/sok/internal/miniProgram/**`，不能访问 App API 以绕过用户授权。
- IM 后端不保存 FinClip 控制台密钥，也不负责上传/发布小程序包。

#### 2.3.4 模块 D：SOK Flutter App

| 需实现功能 | 代码模块（建议） | 调用接口 | 结果 / 约束 |
|------------|------------------|----------|-------------|
| FinClip SDK 生命周期 | `FinClipSdkBootstrap` | `Mop.instance.initSDK` | 单例状态机、并发初始化合并、登录切换隔离；SDK 错误映射为领域错误。 |
| 统一入口打开 | `MiniProgramFacade` | IM `launch` → `Mop.openApplet` 或既有 `AppWebViewPage` | 仅使用 IM 后端返回的 launch 参数；不直接读取运营后台配置。 |
| 二楼 / 发现 UI | `SecondFloorPanel`、`SecondFloorEntry` | IM `catalog`、favorites、recent | 缓存 + ETag；展示不作为授权依据。 |
| 宿主扩展 API | `MiniProgramExtensionApi` | IM `session/issue` | 校验发起 `finclipAppId`，只返回短期 ticket/脱敏资料，敏感能力必须宿主确认。 |
| 用户生命周期 | 登录/登出协调器 | IM `revoke` 由后端内部触发 | 清理用户本地缓存和 ticket；按 PoC 结论重新初始化或冷启动 SDK。 |
| 可观测性 | 埋点适配器 | IM `events` 或可用 SOK 埋点通道 | 不上传 ticket、完整 URL/query、用户输入或原始异常。 |

**禁止调用**：Flutter App 不调用 `/sok/admin/miniProgram/**`、`/sok/internal/miniProgram/**` 或 FinClip 控制台/管理 API。

#### 2.3.5 跨模块接口与责任链

```text
FinClip 管理平台：小程序包发布完成
        │ 交付 finclipAppId / 状态
        ▼
SOK 运营平台：配置入口、灰度、排序并发布 revision
        │ 发布事件 / 配置读模型
        ▼
SOK IM 后端：catalog / launch / ticket 强制校验
        │ App API
        ▼
SOK Flutter App：渲染入口 → launch → mop.openApplet
        │ SDK runtime
        ▼
FinClip 小程序容器
```

---

## 3. 领域模型

### 3.1 统一入口条目（App / 后端共用概念）

```json
{
  "id": "mp_cloud_disk",
  "entryType": "finclip",
  "name": "云盘",
  "description": "企业云盘",
  "iconUrl": "https://cdn.sok.com/...",
  "categoryIds": ["tools"],
  "finclip": {
    "appId": "FinClip小程序AppID",
    "path": "/pages/index/index",
    "query": "from=second_floor",
    "sequence": 0
  },
  "dapp": null,
  "permissions": ["login_required"],
  "status": "online",
  "minAppVersion": "1.0.0",
  "revision": 42,
  "updatedAt": "2026-07-22T04:00:00Z",
  "sort": 100
}
```

DApp 条目示例：

```json
{
  "id": "dapp_uniswap",
  "entryType": "dapp",
  "name": "Uniswap",
  "description": "...",
  "iconUrl": "...",
  "categoryIds": ["defi"],
  "dapp": { "url": "https://app.uniswap.org" },
  "finclip": null,
  "permissions": [],
  "status": "online",
  "minAppVersion": null,
  "sort": 50
}
```

### 3.2 与现有代码映射

| 现有 | 演进 |
|------|------|
| `SecondFloorDappItem`（id/name/link/…） | 扩展为 `SecondFloorEntry`：增加 `entryType`、`finclip`；`link` 仅用于 dapp |
| `MiniProgram` + `webUrl` | 废弃硬编码 catalog；收藏/最近 key 可复用 `MiniProgramStorage` 思路，改为按统一 `id` 持久化，或改为服务端同步 |
| `_openSecondFloorDapp` | 改为 `openSecondFloorEntry`：按 `entryType` 分发 |

### 3.3 字段、校验与兼容规则

| 字段 | 规则 |
|------|------|
| `id` | SOK 服务端稳定 ID；不得以 `finclip.appId` 代替，避免一次小程序迁移造成收藏、最近和埋点断裂。 |
| `entryType` | 枚举仅允许 `dapp`、`finclip`；客户端遇到未知值必须忽略并记录非敏感诊断，不能尝试 WebView 打开。 |
| `finclip.appId` | 仅允许后端已登记、`status=online` 的 AppID；格式与存在性由后端校验，客户端仅做非空校验。 |
| `path` | 必须以 `/` 开头；不包含 scheme、host、`..` 或控制字符。未配置时由小程序默认首页决定。 |
| `query` | 服务端以键值对存储、客户端使用 URI 编码后拼接；不得接受小程序传回的任意 query 覆盖服务端权限参数。 |
| `dapp.url` | 仅 HTTPS；主机名使用后端 allowlist 校验，禁止 `file:`、`javascript:`、内网 IP、重定向到非 allowlist 域。 |
| `revision` | 目录版本号单调递增；客户端用于缓存失效、最近/收藏条目的数据刷新。 |
| `status` | `online`、`offline`、`suspended`；后两者均不可展示和打开。紧急下架使用 `suspended` 并即时拒绝 session。 |

### 3.4 客户端状态机

`FinClipSdkBootstrap` 必须是进程级单例，避免多个入口并发初始化：

```text
uninitialized ── initialize ──► initializing ── success ──► ready
     ▲                                │                          │
     └──────── retry(backoff) ◄──── failed ◄── SDK error ─────────┘
```

- 多个 `ensureReady()` 调用共享同一个 in-flight `Future`；不重复调用原生初始化。
- `failed` 记录标准化错误、发生时间与可重试性；退避为 1s、5s、30s，且仅由用户再次打开或 App 前台恢复触发。
- 登出不把 `userId` 置空后继续复用未知 SDK 状态。PoC 需确认 SDK 是否支持重新初始化；不支持时 v1 关闭所有小程序、清业务 ticket，并要求下次冷启动使用新用户身份。
- 所有打开请求在 `ready` 之后串行化；同一 `entryId` 在 1 秒内合并，只保留最后一个请求。

---

## 4. 客户端（Flutter）功能清单

### 4.1 SDK 与工程接入

| 功能 | 说明 | 优先级 |
|------|------|--------|
| 依赖 `mop` | `pubspec.yaml` 引入；`flutter pub get`；iOS `pod install` | P0 |
| iOS Podfile 适配 | 以 FinClip 当前 Pod 的 XCFramework 切片为准。现项目 iOS deployment target 已为 15.0，默认仅排除 `i386`；**不得**为了旧文档盲目把所有 Pod 的 simulator `arm64` 排除，否则会破坏 Apple Silicon 模拟器构建。 | P0 |
| Android/iOS 包名校验 | 与 FinClip 控制台一致：`com.dcb.sok.tgplTeam` | P0 |
| 配置注入 | CI 构建注入 `FINCLIP_SDK_KEY`、`FINCLIP_SDK_SECRET`、`FINCLIP_API_SERVER`；**按环境、平台、签名应用区分**。此举仅防 Git 泄露，不能防安装包逆向。 | P0 |
| 启动初始化 | 首次需要打开前调用 `Mop.instance.initSDK(Config)`；由状态机保障幂等和并发安全，打开前必须 ready。 | P0 |
| 打开小程序 | `openApplet(appId, path:, query:, sequence:)` | P0 |
| 关闭 / 清缓存 | `closeAllApplets`、清除缓存 API（设置页或故障恢复） | P1 |
| 生命周期监听 | 注册小程序事件（打开/关闭/错误），用于埋点与 UI 状态 | P1 |

初始化伪代码（与官方一致）：

```dart
final store = FinStoreConfig(sdkKey, sdkSecret, apiServer, cryptType: 'SM');
final config = Config([store])
  ..userId = currentUserId
  ..appletDebugMode = kDebugMode ? BOOLState.BOOLStateTrue : BOOLState.BOOLStateFalse;
await Mop.instance.initSDK(config, uiConfig: uiConfig); // 先检查返回码并映射为领域错误
```

> 上例仅表达调用形态，`cryptType`、`UIConfig`、`BOOLState`、初始化返回字段必须以最终锁定的 `mop` 版本为准。不要把示例中的 FinClip 凭据、AppID 或调试开关带入生产。

### 4.2 统一打开门面

| 功能 | 说明 | 优先级 |
|------|------|--------|
| `MiniProgramFacade.open(entry)` | `dapp` → 现有 WebView；`finclip` → mop；未知类型 toast | P0 |
| 登录门禁 | `login_required` 时未登录则引导登录 | P0 |
| 并发保护 | 复用 `_openingSecondFloorDapp` 防抖；打开 FinClip 前可关闭二楼 | P0 |
| 失败降级 | FinClip 初始化失败 / 打开失败：错误提示；可选 fallbackUrl（若条目配置） | P1 |
| 版本门禁 | 客户端版本 &lt; `minAppVersion` 时引导升级 | P1 |

### 4.3 二楼 / 发现 UI

| 功能 | 说明 | 优先级 |
|------|------|--------|
| 目录拉取 | 替换 `SecondFloorDappData` mock，请求后端目录 API；本地短缓存 | P0 |
| 分类 / 搜索 / 最近 / 更多 | 保持现有 `SecondFloorPanel` 交互，数据源改为 API | P0 |
| 收藏与最近 | 本地 SharedPreferences 或与后端同步（见后端 5.2）；打开成功后写入 recent | P0 |
| 角标 / 上新 | 可选：`badge`、`isNew` 字段展示 | P2 |

### 4.4 宿主扩展 API（小程序 → App）

小程序通过 FinClip 自定义 API 调宿主，App `registerExtensionApi` 实现。

| API 名（建议） | 作用 | 鉴权 | 优先级 |
|----------------|------|------|--------|
| `sok.getSession` | 返回短期 `ticket` / 过期时间，供小程序后端换正式会话 | 需已登录 | P0 |
| `sok.getUserProfile` | 脱敏昵称、头像、面向该小程序的业务 subject（是否返回手机号由合规决定，默认不返回） | 需已登录 | P0 |
| `sok.closeApplet` | 关闭当前小程序 | — | P1 |
| `sok.share` | 分享到 IM 会话 / 系统分享 | 需授权弹窗 | P2 |
| `sok.scanQRCode` | 调起扫码 | 权限申请 | P2 |
| `sok.pay` / 钱包相关 | 与现有 Wallet 能力桥接 | 强鉴权 + 二次确认 | Phase 2 |

**原则**：扩展 API 只返回**短期票据**或脱敏信息，不返回长期 IM token / 钱包私钥。

#### 4.4.1 扩展 API 契约与调用约束

所有扩展 API 使用同一响应信封，避免把 Dart/原生异常直接暴露给小程序：

```json
{ "ok": true, "data": { "ticket": "...", "expiresIn": 300 } }
```

```json
{ "ok": false, "error": { "code": "MP_AUTH_REQUIRED", "message": "请先登录" } }
```

| 规则 | 约束 |
|------|------|
| 来源校验 | App 注册 handler 时绑定允许的 `finclipAppId`；未知/下架 AppID 的调用一律拒绝。 |
| 参数校验 | 对每个 API 定义 JSON Schema、字段白名单、最大大小（建议 8 KB）与超时（建议 10 秒）。 |
| 用户确认 | `getSession`、`getUserProfile` 仅在已登录后静默执行；涉及分享、相机、定位、交易的 API 必须展示宿主侧确认 UI，不能由小程序自行伪造。 |
| 幂等 | 可能产生副作用的 API 要求 `requestId`；客户端短时间重试不得重复触发。 |
| 版本协商 | 每个请求带 `apiVersion`；不支持时返回 `MP_API_UNSUPPORTED`，不能静默降级。 |
| 错误码 | 至少定义 `MP_AUTH_REQUIRED`、`MP_FORBIDDEN`、`MP_APP_SUSPENDED`、`MP_RATE_LIMITED`、`MP_TIMEOUT`、`MP_INTERNAL`。 |

### 4.5 安全与合规（客户端）

| 功能 | 说明 | 优先级 |
|------|------|--------|
| 凭据暴露面 | 禁止提交真实 SECRET 到 git；CI 注入并通过混淆降低误泄露风险，但明确接受“移动端凭据可被提取”的事实。生产保护依赖 FinClip 应用绑定、最小权限、控制台审计与可轮换凭据。远程下发不能从根本上解决可提取性。 | P0 |
| 调试开关 | `appletDebugMode` 仅 Debug/内部包开启 | P0 |
| 隐私声明 | SDK 权限清单与隐私政策更新（相机/存储等按实际能力） | P0 |
| 日志脱敏 | 不打印 SDK SECRET、ticket、用户 token、完整 query 或包含用户标识的扩展 API 请求体 | P0 |
| 网络边界 | catalog/session 仅 HTTPS；服务端校验证书和域名；是否启用 certificate pinning 需结合证书轮换能力单独决策 | P1 |
| 风险设备 | 对 root/jailbreak、代理、完整性检测仅作为风控信号；不得作为唯一安全边界 | P2 |

### 4.6 埋点

| 事件 | 属性 |
|------|------|
| `miniprogram_init_result` | success/fail, platform, errorCode |
| `miniprogram_open` | entryId, finclipAppId 的哈希或内部 ID, from |
| `miniprogram_open_fail` | entryId, errorCode, message |
| `miniprogram_close` | entryId, durationMs |
| `miniprogram_ext_api` | apiName, success |

复用现有 `MatomoService` 前需补全其事件上报实现（当前 `trackEvent` 的实际 SDK 调用被注释）；在此之前应走已有可用的埋点通道。埋点不携带 ticket、完整 URL/query、昵称、手机号或 IM `userId`。

### 4.7 建议新增模块路径

```
lib/im/miniprogram/
  finclip_config.dart          # KEY/SECRET/apiServer
  finclip_sdk_bootstrap.dart   # initSDK 单例与状态
  mini_program_facade.dart     # 统一打开
  mini_program_extension_api.dart
  mini_program_catalog_api.dart
  models/second_floor_entry.dart
```

### 4.8 缓存、收藏与最近

| 数据 | 存放与 TTL | 一致性规则 |
|------|------------|------------|
| Catalog | 内存 + 本地缓存，建议 10 分钟；保存 `etag`、`revision`、`fetchedAt` | 首屏可读旧缓存，后台带 `If-None-Match` 刷新；接口失败时仅在不超过 24 小时的缓存内降级。 |
| 条目详情 | 不单独长期缓存 | 每次打开以 catalog 快照为主；高风险/灰度条目再请求详情确认。 |
| 最近使用 | 本地立即写入，最多 50 条；登录后异步上传 | 以服务端 `lastOpenedAt` 合并；仅在 SDK 成功交给原生打开后记录，不以点击记录。 |
| 收藏 | v1 本地优先；若启用云同步则服务端为权威 | 已下架/无权限条目保留 ID 但不展示；恢复上架后可自动恢复。 |

缓存不得作为授权判断依据；离线时仅允许打开已缓存且不要求 `sok.getSession` 的条目，是否允许由产品明确开关。

---

## 5. 业务后端功能清单

> SOK 业务后端只区分为两套：**SOK IM 后端** 与 **SOK 运营平台后端**。  
> - SOK IM 后端同时提供 App 公开接口与受网络保护的内部服务接口；二者是**同一后端职责域**，不是第三套后端。  
> - SOK 运营平台后端只服务运营控制台，负责配置与发布管理，不直接承接 App 流量或签发用户 ticket。  
> 两个后端以及 IM 内部接口必须使用不同认证 client，不能混用。FinClip 官方平台 API / 控制台不对 App 直接开放，见第 6 节。

### 5.1 配置与环境

| 所属后端 | 功能 | 说明 | 优先级 |
|----------|------|------|--------|
| 运营平台后端 | 环境隔离 | test / prod 分别绑定 FinClip 应用与小程序 AppID；维护发布配置快照。 | P0 |
| IM 后端 | 客户端配置下发（可选） | IM API `GET /sok/app/appIm/miniProgram/runtimeConfig`：只提供是否启用、feature flag 等运行开关；**不下发明文 SECRET**（见 5.5）。 | P1 |
| 运营平台后端 | 包名校验元数据 | 后台记录 iOS/Android BundleID，与 FinClip 控制台对齐检查；发布时校验。 | P1 |

### 5.2 IM 后端运行时 API 与运营平台配置职责

| 所属后端 | 接口/职责 | 方法 | 说明 | 优先级 |
|----------|-----------|------|------|--------|
| IM 后端 | `/sok/app/appIm/miniProgram/catalog` | GET | 读取运营平台已发布配置，结合用户权限/灰度返回目录；支持 ETag/304。 | P0 |
| IM 后端 | `/sok/app/appIm/miniProgram/entries/{id}/launch` | POST | **打开前权威校验**，读取当前发布配置；检查状态、灰度、版本、权限。 | P0 |
| IM 后端 | `/sok/app/appIm/miniProgram/recent` | GET/PUT | 用户最近使用（登录用户）；带 `id`、客户端时间、幂等 `requestId`。 | P1 |
| IM 后端 | `/sok/app/appIm/miniProgram/favorites` | GET/PUT | 收藏列表；PUT 采用集合语义与 revision 防丢失。 | P1 |
| 运营平台后端 | `/sok/admin/miniProgram/**` | Admin | 管理条目、分类、上架/下架、排序、灰度人群、`minAppVersion` 与回滚。 | P0 |
| 运营平台后端 → IM 后端 | 发布配置同步 | 内部事件/共享存储 | 发布或下架后使 IM 后端配置缓存失效；IM 后端不允许自行编辑灰度与条目。 | P0 |

**Catalog 响应要点**：只返回客户端展示所需字段；`status!=online` 不下发；灰度不可见用户不返回该条目。禁止把“只在 catalog 隐藏”当作下架保护，`launch` 和 `session/issue` 必须重复鉴权。

### 5.3 会话票据（小程序登录桥）

小程序后端不应持有用户 IM 长期 token。推荐流程：

```
小程序                    App(扩展API)                 SOK IM 后端           小程序业务后端
  │  sok.getSession() ──► │                             │                      │
  │                       │  POST /appIm/.../issue ───► │                      │
  │                       │ ◄── { ticket, expireAt } ── │                      │
  │ ◄── ticket ────────── │                             │                      │
  │ ──────────────────────────────────────────────────► │  (可选校验走 SOK)     │
  │  Authorization: ticket ─────────────────────────────┼─────────────────────►│
  │                       │                             │ ◄─ introspect ──────│
  │                       │                             │ ── active + subject ─►│
```

| 接口 | 说明 | 优先级 |
|------|------|--------|
| `POST /sok/app/appIm/miniProgram/session/issue` | App 用用户登录态换 `ticket`（TTL 建议 5 分钟），绑定 `userId` + `finclipAppId` + `entryId` + install/device binding | P0 |
| `POST /sok/internal/miniProgram/session/introspect` | **IM 后端内部接口**：小程序业务后端或网关校验 ticket，返回最小化 `subject`、权限与过期时间；仅 mTLS / OAuth client credentials 服务到服务调用 | P0 |
| `POST /sok/internal/miniProgram/session/revoke` | **IM 后端内部接口**：登出时吊销未过期 ticket | P1 |

Ticket 声明建议：`jti`、`sub(userId)`、`aud(finclipAppId)`、`entryId`、`exp`、`iat`、`iss`、`deviceHash`；使用服务端签名 JWT（带 `kid` 便于轮换）或随机串 + Redis。不得在 ticket 中放手机号、头像、IM token 或钱包标识。

### 5.4 权限与风控

| 功能 | 说明 | 优先级 |
|------|------|--------|
| 登录校验 | 目录中 `login_required` 条目：issue session 必须已登录 | P0 |
| AppID 白名单 | `issue` 时校验 `finclipAppId` 属于已上架条目 | P0 |
| 频控 | 同用户 issue / introspect 限流 | P1 |
| 审计日志 | 记录打开意图、issue、扩展 API 敏感调用 | P1 |
| 违规下架 | 运营一键下架后目录不再返回；可选推送客户端清缓存 | P0 |
| 回滚 | 条目发布、排序和灰度均保留 revision 与操作者；支持一键回滚至上一 revision，审计不可修改 | P0 |
| 异常熔断 | 某条目 5 分钟内打开失败率超过阈值时自动告警；人工确认后可将其置为 `suspended` | P1 |

### 5.5 SDK 密钥管理（重要）

FinClip Flutter 集成文档要求客户端初始化时传入 SDK KEY/SECRET，并校验 BundleID/Application ID。由于客户端必须持有该值，**不能把它定义为不可泄露的服务端 secret**。

| 方案 | 做法 | 建议 |
|------|------|------|
| **CI 注入（推荐 v1）** | KEY/SECRET 存密钥管理系统，构建时 `--dart-define` 写入；分 debug/release、iOS/Android；防止进入代码库 | ✅ v1 |
| 远程下发 | 后端下发加密配置；只能降低批量替换和误配置风险，仍可在受控设备上被提取，不能作为核心保密方案 | 可选 P2 |
| 源码硬编码 | 禁止 | ❌ |

后端/运维在密钥管理系统中保存控制台访问凭据与 SDK 凭据，并设置最小权限、双人审批、审计、轮换和紧急吊销。普通用户 API 绝不下发明文 SECRET。

### 5.6 数据与埋点（后端）

| 功能 | 说明 | 优先级 |
|------|------|--------|
| `POST /sok/app/appIm/miniProgram/events` | 客户端上报 open/close/fail（可与现有埋点并存） | P2 |
| 运营统计 | 按 entryId / finclipAppId 打开次数、UV、失败率 | P2 |

### 5.7 与 IM / 钱包边界

| 能力 | v1 | 说明 |
|------|----|------|
| 用 IM `userID` 作为 FinClip `config.userId` | 是 | 便于 FinClip 侧用户维度统计 |
| 小程序直接调 Wallet 转账 | 否 | Phase 2，需独立产品与风控设计 |
| 小程序发 IM 消息 | 可选 P2 | 经扩展 API + 用户确认 |

---

## 6. FinClip 平台侧（非代码，但必做）

| 事项 | 说明 | 负责人 |
|------|------|--------|
| 注册企业账号 | [finclip.com](https://finclip.com/) | 产品/运维 |
| 创建 iOS / Android 应用 | BundleID = `com.dcb.sok.tgplTeam` | 运维 |
| 获取 SDK KEY / SECRET | 分环境、分端 | 运维 → 交 CI |
| 上传/绑定业务小程序 | 得到小程序 AppID，填入 SOK 目录 | 业务 + 运营 |
| 权限与域名配置 | 小程序 request 合法域名等 | 小程序开发 |
| 示例联调 | 可用 FinClip 示例 AppID 先通链路 | 客户端 |
| 发布与回滚演练 | 灰度发布小程序版本、验证旧包回滚、记录版本/AppID/发布时间 | 运营 + 小程序开发 |
| 控制台权限 | 生产组织启用最小权限、MFA、操作审计；开发/测试/生产账号与应用隔离 | 运维 |

### 6.1 FinClip 平台 API 边界

FinClip 的应用创建、SDK 凭据管理、小程序上传/发布/绑定属于 **FinClip 运营控制台或其官方服务端 API** 的职责，不属于 SOK IM API，也不能由 Flutter App 调用。

| 场景 | 调用方 | 接入方式 |
|------|--------|----------|
| 创建 FinClip 应用、配置 BundleID、申请/轮换 SDK 凭据 | SOK 运维 | FinClip 控制台；若启用自动化，仅由受控的 SOK 运维服务调用官方 API。 |
| 上传、审核、发布、回滚小程序包 | 小程序开发/运营 | FinClip Studio / 控制台；SOK 运营后台只保存 AppID、展示配置和上架状态。 |
| App 运行时下载、校验、启动小程序 | `mop` SDK | SDK 直连 FinClip `apiServer`，不是 Flutter 业务 HTTP 客户端调用。 |
| SOK 业务目录、灰度、用户 ticket | SOK IM 后端 + SOK 运营平台后端 | 第 8 章接口；运营平台负责配置，IM 后端在运行时执行授权与 ticket；FinClip 控制台不参与用户 ticket 签发。 |

### 6.2 FinClip 管理平台与 SOK 运营平台能力划分

FinClip 已提供小程序生命周期、专属服务商店、开发者开放平台、数据管理中心与灰度分发等运营能力（以采购版本、部署模式和开通模块为准）。因此 SOK 运营平台不应重复实现“小程序包管理平台”，而应聚焦 SOK App 的业务入口与用户运营。能力不可互相替代。

| 能力 | FinClip 管理平台（权威） | SOK 运营平台（权威） | SOK IM 后端运行时职责 |
|------|--------------------------|----------------------|------------------------|
| FinClip 应用 / BundleID / SDK KEY、SECRET | 创建、绑定、轮换、停用 | 只引用环境与应用标识，不保存控制台主凭据 | 使用构建期配置初始化 SDK |
| 开发者与小程序开发接入 | 开发者入驻、上传与管理小程序 | 管理 SOK 业务方准入记录（可选） | 不参与 |
| 小程序包与版本 | 上传、审核、版本发布、回滚、包分发 | 保存要展示版本的 AppID/备注；不复制或托管包文件 | `mop` SDK 拉取并运行已发布包 |
| 小程序自身灰度/分发 | 按 FinClip 能力对小程序版本/包进行灰度 | 不配置包版本灰度 | 依赖 SDK/FinClip 返回的可用包 |
| SOK App 二楼/发现页展示 | 可提供服务商店/小程序目录能力 | 管理 SOK 专属卡片、分类、排序、图标、文案、DApp 与小程序混排 | 返回当前用户可见的目录 |
| SOK 用户运营灰度 | 不掌握 SOK IM 用户权限 | 按 SOK 用户、角色、地区、版本、活动配置可见性 | 在 catalog/launch 时强制执行 |
| 用户收藏、最近使用 | 不作为 SOK 用户行为权威来源 | 不直接写用户态数据 | 维护、同步当前用户的收藏与最近 |
| SOK 登录、业务 ticket、用户资料桥 | 不签发 SOK 身份凭据 | 配置可调用的能力与审核规则 | 签发/校验/吊销 ticket，执行扩展 API 鉴权 |
| 小程序业务数据 | 仅提供容器/平台能力 | 可配置业务入口，不存业务交易主数据 | 不代理小程序业务请求 |
| 数据分析 | 小程序运行、分发等平台维度数据 | SOK 入口曝光、运营配置与活动维度数据 | 上报/聚合打开、失败、时长等 SOK 事件 |
| 紧急处置 | 下架小程序包、撤销平台发布 | 隐藏 SOK 入口、停止灰度、配置回滚 | 立即拒绝 `launch`/`issue`，吊销未过期 ticket |

**最终可打开条件**：`FinClip 小程序已发布且可分发` **并且** `SOK 运营平台条目为 online` **并且** `SOK IM 后端判定当前用户满足权限、灰度和最低版本`。任一条件不满足，App 均不可打开小程序。

### 6.3 两个平台的发布与状态同步

1. 小程序团队先在 FinClip 管理平台发布小程序，取得稳定的 `finclipAppId` 与已验证版本。
2. SOK 运营人员在 SOK 运营平台创建/编辑入口，填写 `finclipAppId`、展示信息、SOK 用户灰度与最低 App 版本，发布后产生 `revision`。
3. SOK 运营平台通过内部事件或共享配置使 SOK IM 后端的目录缓存失效；IM 后端开始按新 `revision` 服务 App。
4. FinClip 下架或回滚小程序时，运营人员必须同步在 SOK 运营平台暂停入口；若 FinClip 支持可靠 webhook，可自动触发暂停，否则以运营告警和轮询校验兜底。
5. SOK 紧急下架优先在 SOK 运营平台执行：IM 后端立即拒绝 `launch` 和 `session/issue`，无需等待 FinClip 包下架完成。

> 禁止用 SOK 运营平台绕过 FinClip 发布审核，也禁止仅在 FinClip 下架而保留 SOK 可见入口。两个平台分别保留自己的操作审计。

---

## 7. 端到端流程

### 7.1 首次启动

1. App 启动 → 读取构建期 FinClip 配置，但不必立即初始化 SDK。
2. 进入二楼 → 读缓存并并行 `GET /sok/app/appIm/miniProgram/catalog`（ETag 刷新）→ 渲染可见条目。
3. 首次打开 FinClip 条目 → 检查登录/版本 → `ensureReady(currentUserId)` → 初始化成功后才允许打开。
4. 登录切换 / 登出 → 吊销业务 ticket、清空最近/收藏中的用户态数据；根据 PoC 结果重新初始化或要求下一次冷启动，不得把前一用户的 SDK 上下文带给新用户。

### 7.2 打开 FinClip 小程序

1. 用户点击条目（`entryType=finclip`）。
2. 客户端做快速检查：登录、版本、SDK 状态与点击防抖。
3. `POST /sok/app/appIm/miniProgram/entries/{id}/launch`：服务端重新校验上架状态、灰度、用户权限、最低版本，并返回短寿命 launch 配置。
4. `ensureReady(currentUserId)`，再执行 `openApplet(appId, path, query, sequence)`。
5. 仅当 SDK 已接受打开请求时写 recent；通过 handler/生命周期回调补充实际打开、关闭、时长埋点。
6. 失败：将 SDK/网络错误映射为用户可读提示；不泄露服务地址、凭据或原始异常。

### 7.3 小程序获取用户态

1. 小程序调用 `sok.getSession({apiVersion, requestId})`。
2. App 校验发起 AppID 与用户登录状态，再调 `POST /sok/app/appIm/miniProgram/session/issue`。
3. App 仅把 ticket 和到期秒数返回给已授权小程序。
4. 小程序带 ticket 访问自有后端；自有后端使用服务凭据调用 `introspect`，得到最小权限主体。
5. 登出、下架、权限撤销或 ticket 过期后立即拒绝；小程序重新获取 ticket 也不能绕过 `launch`/`issue` 鉴权。

---

## 8. API 契约草案（SOK 后端）

### 8.0 接口总览与调用方

当前 Flutter 工程的 `ApiPaths` 已定义 IM App 前缀为 `/sok/app/appIm`（相对 `Config.imApiUrl`）。因此，以下 App 接口统一归为 **SOK IM API**，建议新增至该前缀下；它们不是运营后台 API。

运营后台尚未在本仓库定义网关前缀，本文建议独立使用 `/sok/admin/miniProgram/**`。这是**待后端确认的建议**，不得假定当前已存在。

| 所属 SOK 后端 | 接口暴露面 | 允许调用方 | 认证与网络边界 | 禁止事项 |
|--------------|------------|--------------|----------------|----------|
| **SOK IM 后端** | App API：`/sok/app/appIm/miniProgram/**` | Flutter App | 复用 App 的登录 token 和 IM API 网关 | 不能执行上架、下架、灰度、密钥或审计管理。 |
| **SOK IM 后端** | 内部 API：`/sok/internal/miniProgram/**` | 登录服务、风控、已注册的小程序业务后端 | mTLS 或 OAuth2 client credentials、服务网络策略 | 不得暴露给 App、WebView 或 FinClip 小程序。 |
| **SOK 运营平台后端** | 运营 API：`/sok/admin/miniProgram/**` | 运营 Web 控制台 | 独立后台 SSO、RBAC、MFA、管理网关 | 不得被 App 或小程序 JS 调用。 |
| **FinClip 平台** | 控制台或官方服务端 API | FinClip 控制台或 SOK 运维自动化任务 | FinClip 账号/API 凭据，存于密钥管理系统 | App 不直接调用 FinClip 管理 API，也不接收控制台凭据。 |

| 所属后端 | 接口 | 调用方 | 认证 | P |
|----------|------|--------|------|---|
| IM 后端（App API） | `GET /sok/app/appIm/miniProgram/catalog` | App | App 登录 token；允许匿名目录须单独产品确认 | P0 |
| IM 后端（App API） | `POST /sok/app/appIm/miniProgram/entries/{entryId}/launch` | App | App 登录 token（或明确的游客会话） | P0 |
| IM 后端（App API） | `POST /sok/app/appIm/miniProgram/session/issue` | App 的 `sok.getSession` handler | App 登录 token | P0 |
| IM 后端（App API） | `GET/PUT /sok/app/appIm/miniProgram/recent` | App | App 登录 token | P1 |
| IM 后端（App API） | `GET/PUT /sok/app/appIm/miniProgram/favorites` | App | App 登录 token | P1 |
| IM 后端（App API） | `GET /sok/app/appIm/miniProgram/runtimeConfig` | App | App 登录 token；若公开需单独拆匿名接口 | P1 |
| IM 后端（App API） | `POST /sok/app/appIm/miniProgram/events` | App | App 登录 token | P2 |
| IM 后端（内部 API） | `POST /sok/internal/miniProgram/session/introspect` | 小程序业务后端/网关 | mTLS 或 OAuth2 client credentials | P0 |
| IM 后端（内部 API） | `POST /sok/internal/miniProgram/session/revoke` | SOK 登录/登出服务、风控任务 | 内部服务认证 | P1 |
| 运营平台后端 | `/sok/admin/miniProgram/**` | 运营后台 | 后台 RBAC + MFA | P0 |

### 8.0.1 公共约定

**请求头**

| Header | 是否必填 | 说明 |
|--------|----------|------|
| `Authorization` | 用户接口必填 | 沿用 SOK 当前登录态格式和刷新策略；本设计不定义新的 token 格式。 |
| `X-Request-Id` | 写接口必填，读接口建议 | UUID v4；用于链路追踪和幂等。后端若缺失则生成并在响应返回。 |
| `Idempotency-Key` | `issue`、recent、favorites 写接口必填 | 与 `X-Request-Id` 可使用同一 UUID；同用户、同路径、同 key 的请求在 10 分钟内返回首次结果。 |
| `If-None-Match` | catalog 可选 | 上一次 catalog 响应的 ETag；命中时返回 304。 |
| `X-Client-Version` | launch 必填 | 语义化版本，如 `1.0.0`；不能由客户端自报决定是否绕过最低版本。 |
| `X-Platform` | launch 必填 | `ios` 或 `android`。 |

**公共成功响应**

```json
{
  "code": "OK",
  "data": {},
  "requestId": "d064f45a-a8dc-4084-9960-cf83f89419f9"
}
```

HTTP 状态表达协议与鉴权结果；业务 `code` 表达稳定的客户端动作。不得将内部异常、SQL、上游地址或凭据透传给客户端。

### 8.0.2 共享数据字典（IM 后端）

所有 IM 后端接口复用下列类型、枚举和错误码，接口小节不再重复解释。

**基础类型约定**

| 类型 | 说明 |
|------|------|
| `entryId` | string，SOK 目录条目稳定 ID，`^mp_[a-z0-9_]{1,48}$` 或 `^dapp_[a-z0-9_]{1,48}$`。 |
| `finclipAppId` | string，FinClip 小程序 AppID，由 FinClip 平台分配；客户端不得篡改。 |
| `revision` | int64，单调递增的目录发布版本号。 |
| `timestamp` | string，RFC3339 UTC，如 `2026-07-22T04:05:00Z`。 |
| `semver` | string，语义化版本，如 `1.2.3`。 |
| `requestId` | string，UUID v4；响应回显请求头 `X-Request-Id`。 |

**枚举**

| 枚举 | 取值 | 含义 |
|------|------|------|
| `entryType` | `finclip` / `dapp` | 小程序容器条目 / 内置 H5 条目。 |
| `status` | `online` / `offline` / `suspended` | 仅 `online` 可展示与打开；`suspended` 为紧急下架。 |
| `permission` | `login_required` | 打开该条目要求已登录；数组，未来可扩展。 |
| `platform` | `ios` / `android` | 客户端平台，取自 `X-Platform`。 |
| `scope` | `profile:read` 等 | ticket 授予小程序业务方的最小权限，白名单可扩展。 |

**统一业务错误码**

| `code` | HTTP | 触发条件 | 客户端建议动作 |
|--------|------|----------|----------------|
| `OK` | 200 | 成功 | 正常处理。 |
| `MP_UNAUTHENTICATED` | 401 | 无有效登录态 | 走既有登录失效流程。 |
| `MP_FORBIDDEN` | 403 | 已登录但无权限/不在灰度 | 隐藏条目，提示不可用。 |
| `MP_ENTRY_NOT_FOUND` | 404 | 条目不存在 | 删除本地条目。 |
| `MP_ENTRY_SUSPENDED` | 409 | 条目 `offline`/`suspended` | 立即隐藏并清理最近/收藏可打开状态。 |
| `MP_VERSION_TOO_LOW` | 426 | 客户端版本 < `minAppVersion` | 引导升级。 |
| `MP_FAVORITES_CONFLICT` | 409 | 收藏 `revision` 落后 | 拉取最新集合合并后重试。 |
| `MP_RATE_LIMITED` | 429 | 触发频控 | 退避重试，不无限重试。 |
| `MP_VALIDATION_FAILED` | 400 | 参数校验失败 | 修正请求，通常为客户端 bug。 |
| `MP_INTERNAL` | 500 | 服务端内部错误 | 提示稍后再试。 |

错误响应体统一为 `{ "code", "message", "requestId" }`，`message` 仅用于日志/调试展示，不作为客户端分支依据。

### 8.1 Catalog

`GET /sok/app/appIm/miniProgram/catalog`（SOK IM API）

Query：

| 参数 | 类型 | 必填 | 约束 |
|------|------|------|------|
| `categoryId` | string | 否 | 已发布分类 ID。 |
| `keyword` | string | 否 | 1–64 字符；服务端规范化、转义后按名称/描述检索。 |
| `cursor` | string | 否 | 不透明游标；不得让客户端传数据库 offset。 |
| `limit` | integer | 否 | 默认 50，范围 1–100。 |
| `source` | string | 否 | `second_floor` / `discover` / `search`，仅用于分析，不能决定授权。 |

Response：

```json
{
  "code": "OK",
  "data": {
    "revision": 42,
    "categories": [{ "id": "tools", "name": "工具", "sort": 100 }],
    "items": [
      {
        "id": "mp_xxx",
        "entryType": "finclip",
        "name": "示例",
        "description": "",
        "iconUrl": "https://cdn.sok.com/...",
        "categoryIds": ["tools"],
        "finclip": {
          "appId": "...",
          "path": "/pages/index/index",
          "query": ""
        },
        "permissions": ["login_required"],
        "minAppVersion": "1.0.0",
        "revision": 42,
        "sort": 100
      }
    ],
    "nextCursor": null
  },
  "requestId": "..."
}
```

Response 字段（`data`）：

| 字段 | 类型 | 必返回 | 说明 |
|------|------|--------|------|
| `revision` | `revision` | 是 | 本次目录快照版本；客户端缓存与 `launch` 比对。 |
| `categories[]` | array | 是 | 分类列表；可为空数组。 |
| `categories[].id` | string | 是 | 分类 ID。 |
| `categories[].name` | string | 是 | 分类显示名。 |
| `categories[].sort` | int | 是 | 升序排序权重。 |
| `items[]` | array | 是 | 当前用户可见条目；可为空数组。 |
| `items[].id` | `entryId` | 是 | 条目稳定 ID。 |
| `items[].entryType` | `entryType` | 是 | `finclip` 或 `dapp`。 |
| `items[].name` | string | 是 | 显示名，≤ 40 字符。 |
| `items[].description` | string | 否 | 描述，≤ 120 字符，可空串。 |
| `items[].iconUrl` | string(URL) | 否 | HTTPS 图标地址。 |
| `items[].categoryIds[]` | string[] | 是 | 所属分类，至少含 1 项或空数组（未分类）。 |
| `items[].finclip` | object\|null | `entryType=finclip` 时必返回 | 见下；`dapp` 条目为 `null`。 |
| `items[].finclip.appId` | `finclipAppId` | 条件 | FinClip AppID。 |
| `items[].finclip.path` | string | 否 | 启动页路径，以 `/` 开头。 |
| `items[].finclip.query` | string | 否 | 已编码的启动参数。 |
| `items[].dapp` | object\|null | `entryType=dapp` 时必返回 | H5 条目配置。 |
| `items[].dapp.url` | string(URL) | 条件 | HTTPS，服务端 allowlist 校验后的地址。 |
| `items[].permissions[]` | `permission[]` | 是 | 打开所需权限；可空数组。 |
| `items[].minAppVersion` | `semver`\|null | 否 | 低于此版本客户端不展示或置灰。 |
| `items[].revision` | `revision` | 是 | 该条目最后发布版本。 |
| `items[].sort` | int | 是 | 升序排序权重。 |
| `nextCursor` | string\|null | 是 | 分页游标；`null` 表示到底。 |

响应头：`ETag: "catalog:u-<hash>:r-42"`、`Cache-Control: private, max-age=600`。如果当前用户看见的目录受灰度、地区或角色影响，则禁止共享 CDN 缓存该响应。命中 `If-None-Match` 时返回 `304` 无响应体。

catalog 不做打开鉴权，只保证“看得到的都是当前发布且用户可见”；能否真正打开以 `launch` 为准。

### 8.2 Launch（打开前权威校验）

`POST /sok/app/appIm/miniProgram/entries/{entryId}/launch`（SOK IM API）

Path：`entryId` 为 SOK 目录 ID，不接受 FinClip AppID 作为路径参数。

Request 字段：

| 字段 | 类型 | 必填 | 约束 |
|------|------|------|------|
| `source` | string | 否 | `second_floor` / `discover` / `search` / `deeplink`；仅用于分析。 |
| `catalogRevision` | `revision` | 否 | 客户端当前目录版本；与服务端不一致时响应 `staleCatalog=true` 提示刷新。 |

Header：`X-Client-Version`（`semver`，必填）、`X-Platform`（`platform`，必填）、`Authorization`（登录态）。

服务端从 Header 读取客户端版本和平台，并按以下顺序校验：

1. 用户登录状态（及游客策略）；2. 条目存在、`online`；3. iOS/Android 可用性；4. `minAppVersion`；5. 灰度/地区/角色；6. `entryType` 与配置完整性；7. 对 FinClip 条目确认 AppID 仍在服务端 allowlist。

任一校验失败返回对应错误码（`MP_ENTRY_NOT_FOUND` / `MP_ENTRY_SUSPENDED` / `MP_FORBIDDEN` / `MP_VERSION_TOO_LOW` / `MP_UNAUTHENTICATED`）。

Response：

```json
{
  "code": "OK",
  "data": {
    "entryId": "mp_xxx",
    "revision": 42,
    "entryType": "finclip",
    "finclip": {
      "appId": "...",
      "path": "/pages/index/index",
      "query": "from=second_floor",
      "sequence": 0
    },
    "expiresAt": "2026-07-22T04:05:00Z",
    "staleCatalog": false
  },
  "requestId": "..."
}
```

Response 字段（`data`）：

| 字段 | 类型 | 必返回 | 说明 |
|------|------|--------|------|
| `entryId` | `entryId` | 是 | 回显请求条目。 |
| `revision` | `revision` | 是 | 本次授权基于的发布版本。 |
| `entryType` | `entryType` | 是 | `finclip` / `dapp`。 |
| `finclip` | object\|null | `entryType=finclip` 时必返回 | 打开所需 FinClip 参数。 |
| `finclip.appId` | `finclipAppId` | 条件 | 传给 `Mop.openApplet` 的 AppID。 |
| `finclip.path` | string | 否 | 启动页路径，以 `/` 开头。 |
| `finclip.query` | string | 否 | 已编码启动参数。 |
| `finclip.sequence` | int | 否 | FinClip 打开序号，默认 `0`。 |
| `dapp` | object\|null | `entryType=dapp` 时必返回 | H5 打开配置。 |
| `dapp.url` | string(URL) | 条件 | 经 allowlist 校验的 HTTPS 地址。 |
| `dapp.title` | string | 否 | WebView 标题。 |
| `expiresAt` | `timestamp` | 是 | launch 配置有效期，建议 5 分钟；过期需重新请求。 |
| `staleCatalog` | bool | 是 | `true` 表示客户端目录已过期，应刷新 catalog。 |

`expiresAt` 防止客户端把旧 launch 参数无限复用；它不是 FinClip SDK 凭据。若 `entryType=dapp`，返回经服务端 allowlist 校验的 `dapp.url`，客户端不得自行改用 catalog 中的旧 URL。

### 8.3 Issue Session

`POST /sok/app/appIm/miniProgram/session/issue`（SOK IM API）

Request：

```json
{
  "entryId": "mp_xxx",
  "finclipAppId": "...",
  "installId": "stable-app-install-id"
}
```

Request 字段：

| 字段 | 类型 | 必填 | 后端处理 |
|------|------|------|----------|
| `entryId` | `entryId` | 是 | 查询当前发布记录；重复检查在线、灰度、权限和用户资格。 |
| `finclipAppId` | `finclipAppId` | 是 | 必须与该 `entryId` 的当前配置完全一致，防止将 ticket 转发给其他小程序。 |
| `installId` | string | 是 | 客户端生成的随机安装实例 ID；后端只存不可逆 hash，不作为设备指纹或唯一安全凭据。 |
| `requestedScope[]` | `scope[]` | 否 | 期望权限；服务端取「期望 ∩ 条目允许」的交集，未传则用条目默认 scope。 |

Response：

```json
{
  "code": "OK",
  "data": {
    "ticket": "...",
    "tokenType": "SOK-MP-Ticket",
    "expiresIn": 300,
    "scope": ["profile:read"],
    "refreshAfter": 240
  },
  "requestId": "..."
}
```

Response 字段（`data`）：

| 字段 | 类型 | 必返回 | 说明 |
|------|------|--------|------|
| `ticket` | string | 是 | 短寿命业务票据（JWT 或不透明串）；绑定 `sub`/`aud=finclipAppId`/`entryId`。 |
| `tokenType` | string | 是 | 固定 `SOK-MP-Ticket`。 |
| `expiresIn` | int(秒) | 是 | 有效期，建议 300；不超过 900。 |
| `scope[]` | `scope[]` | 是 | 实际授予权限。 |
| `refreshAfter` | int(秒) | 否 | 建议客户端在此秒数后刷新 ticket，通常为 `expiresIn` 的 80%。 |

响应**不返回 `userId`**。小程序无需获知 SOK 账户主键；其业务后端应通过 `introspect` 获得仅限该业务使用的稳定 subject。`ticket` 不写入小程序持久化存储，内存持有、到期前按需刷新。

错误码：`MP_UNAUTHENTICATED`（未登录）、`MP_FORBIDDEN`（AppID 未授权或不在灰度）、`MP_ENTRY_SUSPENDED`（已下架）、`MP_RATE_LIMITED`（频控）。

### 8.4 IM 后端内部接口：Introspect（仅服务间）

`POST /sok/internal/miniProgram/session/introspect`（SOK IM 后端内部 API）

认证：mTLS 或 OAuth2 client credentials；服务账号必须绑定允许访问的 `finclipAppId`/业务方。**拒绝 App、小程序 JS、匿名互联网请求。**

Request 字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `token` | string | 是 | 待校验 ticket。 |
| `expectedAppId` | `finclipAppId` | 否 | 若传入，服务端额外校验 ticket `aud` 与其一致，不一致视为 `active:false`。 |

Response：

```json
{
  "code": "OK",
  "data": {
    "active": true,
    "subject": "mpu_7fb2...",
    "finclipAppId": "...",
    "entryId": "mp_xxx",
    "scope": ["profile:read"],
    "expiresAt": "2026-07-22T04:05:00Z"
  },
  "requestId": "..."
}
```

Response 字段（`data`）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `active` | bool | ticket 是否有效且未吊销/过期。 |
| `subject` | string | 面向该小程序业务方的伪匿名稳定 ID（同用户 + 同 AppID 稳定）。 |
| `finclipAppId` | `finclipAppId` | ticket 绑定的小程序。 |
| `entryId` | `entryId` | ticket 绑定的条目。 |
| `scope[]` | `scope[]` | 授予的权限。 |
| `expiresAt` | `timestamp` | 过期时间。 |

`subject` 是面向该小程序业务方的伪匿名稳定标识；不得返回 IM `userID`、手机号、头像、钱包标识或原始 ticket claims。无效/过期 token 仍返回 `200` + `active:false`（其余字段省略），认证失败才返回 `401/403`，以避免 token 枚举差异。

### 8.5 IM 后端内部接口：Revoke

`POST /sok/internal/miniProgram/session/revoke`（SOK IM 后端内部 API）

仅由登录/登出服务、风控任务和运营紧急下架任务调用。支持按 `userId`、`entryId`、`finclipAppId` 或 `jti` 吊销；写入 denylist 直至原 ticket 最长过期时间。登出时按用户与安装实例吊销，条目紧急下架时按 `entryId` 全量吊销。

Request 字段（四选一，至少一个非空）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `userId` | string | 吊销该用户全部未过期 ticket（登出场景）。 |
| `installId` | string | 与 `userId` 组合，仅吊销某安装实例（可选）。 |
| `entryId` | `entryId` | 吊销该条目全部 ticket（紧急下架场景）。 |
| `finclipAppId` | `finclipAppId` | 吊销该小程序全部 ticket。 |
| `jti` | string | 吊销单张 ticket。 |
| `reason` | string | 审计原因，如 `logout` / `suspend` / `risk`。 |

Response `data`：`{ "revokedCount": int, "notBefore": timestamp }`。

### 8.6 最近使用与收藏

#### 最近使用

`GET /sok/app/appIm/miniProgram/recent`

Query：`limit`（int，默认 20，范围 1–50）。

Response `data`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `items[]` | array | 最近条目，按 `lastOpenedAt` 倒序，仅含当前可见/可打开条目。 |
| `items[].entryId` | `entryId` | 条目 ID。 |
| `items[].lastOpenedAt` | `timestamp` | 服务端记录的最后打开时间。 |
| `items[].entrySnapshot` | object | 展示快照（`name`、`iconUrl`、`entryType`），便于渲染不再单独查 catalog。 |

`PUT /sok/app/appIm/miniProgram/recent/{entryId}`

Request 字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `openedAt` | `timestamp` | 否 | 客户端时间，仅诊断；服务端以自身时间为准。 |
| `catalogRevision` | `revision` | 否 | 打开时的目录版本，用于分析。 |

只接受当前用户可打开且 `launch` 已成功的条目；服务端每用户最多保留 50 条，超出删除最旧。`PUT` 必须携带 `Idempotency-Key`。Response `data`：`{ "entryId", "lastOpenedAt", "total" }`。

#### 收藏

`GET /sok/app/appIm/miniProgram/favorites`

Response `data`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `revision` | int | 收藏集合版本，用于并发冲突检测。 |
| `entryIds[]` | `entryId[]` | 收藏条目 ID，按用户排序保存。 |
| `items[]` | array | 可选：附带 `entrySnapshot`；下架/离线条目 `available=false` 但保留 ID。 |

`PUT /sok/app/appIm/miniProgram/favorites`

Request 字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `entryIds[]` | `entryId[]` | 是 | 全量集合替换，最多 100 项，服务端校验存在性。 |
| `revision` | int | 是 | 客户端持有的集合版本；落后则冲突。 |

收藏更新若 `revision` 落后，返回 `MP_FAVORITES_CONFLICT`（HTTP 409）以及当前集合，客户端拉取后合并再提交，避免覆盖另一设备修改。成功返回新 `revision` 与最终 `entryIds`。

### 8.7 Runtime Config（可选）

`GET /sok/app/appIm/miniProgram/runtimeConfig`（SOK IM API）

仅下发功能开关，例如：

```json
{
  "code": "OK",
  "data": {
    "enabled": true,
    "catalogEnabled": true,
    "allowOfflineOpen": false,
    "minClientVersion": "1.0.0",
    "cacheTtlSeconds": 600
  },
  "requestId": "..."
}
```

Response 字段（`data`）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 小程序能力总开关；`false` 时二楼隐藏入口。 |
| `catalogEnabled` | bool | 是否启用远程目录；`false` 可退回内置占位。 |
| `allowOfflineOpen` | bool | 是否允许离线打开已缓存且无需 session 的条目。 |
| `minClientVersion` | `semver` | 低于此版本提示整体升级。 |
| `cacheTtlSeconds` | int | 建议 catalog 本地缓存 TTL，客户端可据此调整刷新节奏。 |

不得通过本接口下发 FinClip SDK SECRET。若未来下发 `apiServer`，必须限定为预置 allowlist、使用签名响应，并保留上一份已验证配置用于故障回退。

### 8.8 SOK 运营后台 API（不是 IM API）

运营后台所有写接口必须走 RBAC、MFA、审批（至少生产上架/下架双人复核）、不可篡改审计，且与 App API 使用不同的认证 client。

| 接口 | 方法 | 关键请求/动作 |
|------|------|---------------|
| `/sok/admin/miniProgram/entries` | GET | 分页查条目、状态、AppID、当前 revision、灰度摘要。 |
| `/sok/admin/miniProgram/entries` | POST | 创建草稿；校验 `entryType`、FinClip AppID 登记、URL allowlist、分类。 |
| `/sok/admin/miniProgram/entries/{entryId}` | PATCH | 修改草稿字段，必须携带 `If-Match: revision` 防并发覆盖。 |
| `/sok/admin/miniProgram/entries/{entryId}/publish` | POST | 发布指定草稿 revision；创建可回滚快照并刷新目录 revision。 |
| `/sok/admin/miniProgram/entries/{entryId}/suspend` | POST | 紧急下架，立即拒绝 launch/issue，并触发该条目 ticket 吊销。 |
| `/sok/admin/miniProgram/entries/{entryId}/resume` | POST | 恢复已验证版本；不自动恢复过期 ticket。 |
| `/sok/admin/miniProgram/entries/{entryId}/rollback` | POST | 回滚至明确 `targetRevision`；写审计。 |
| `/sok/admin/miniProgram/entries/{entryId}/rollout` | PUT | 配置白名单、百分比、地区/版本条件；发布前验证规则无冲突。 |
| `/sok/admin/miniProgram/categories` | CRUD | 维护分类、排序与可见性。 |
| `/sok/admin/miniProgram/auditLogs` | GET | 按条目、操作者、时间、动作查询，仅审计角色可读。 |

### 8.9 事件上报（P2）

`POST /sok/app/appIm/miniProgram/events`（SOK IM API）

Request 字段：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `events[]` | array | 是 | 批量事件，单次 ≤ 50 条。 |
| `events[].type` | enum | 是 | 白名单：`sdk_init` / `launch_accepted` / `applet_opened` / `applet_closed` / `open_failed` / `extension_api_called`。 |
| `events[].entryId` | `entryId` | 否 | 关联条目（若适用）。 |
| `events[].finclipAppId` | `finclipAppId` | 否 | 关联小程序。 |
| `events[].platform` | `platform` | 是 | 平台。 |
| `events[].sdkVersion` | string | 否 | FinClip SDK 版本。 |
| `events[].durationMs` | int | 否 | 打开/停留耗时。 |
| `events[].errorCode` | string | 否 | 标准化错误码（失败事件）。 |
| `events[].occurredAt` | `timestamp` | 是 | 事件发生时间。 |

不允许传 ticket、完整 URL/query、用户输入或原始异常。服务端按用户/安装实例/entry 限流并异步写入分析队列，事件写入失败不得影响打开小程序，恒返回 `202`/`OK`。

### 8.10 错误响应和幂等约定

所有接口采用 SOK 现有统一错误包络；若未统一，最少定义：

```json
{
  "code": "MP_ENTRY_SUSPENDED",
  "message": "该小程序暂不可用",
  "requestId": "uuid"
}
```

- 写操作（recent、favorites、issue）必须接收客户端 `requestId`，服务端至少保留 10 分钟的幂等记录。
- `catalog` 返回 `ETag` 与 `Cache-Control: private, max-age=600`；用户维度灰度的响应必须 `Vary` 用户认证上下文，避免 CDN 串数据。
- 客户端在 `401` 走既有登录失效流程；`403/404/MP_ENTRY_SUSPENDED` 删除或隐藏本地条目；`429/5xx` 可提示重试但不无限自动重试。

---

## 9. 分阶段落地

### Phase 0：技术 PoC 与准入（约 3–5 人日）

- FinClip 控制台应用 + KEY/SECRET + 示例小程序 AppID。
- Flutter 接入**锁定版本**的 `mop`，经 CI 注入配置联调 `initSDK` / `openApplet`。
- 验证真机 iOS/Android、登录切换、冷启动、后台恢复、离线、关闭、缓存清理和原生回调。
- 输出：SDK 版本、包体增量、最低系统版本、权限/隐私清单、已验证 handler / extension API 语义；任一项不通过则不进入 Phase 1。

### Phase 1：产品化目录（约 1–2 周）

- 后端 catalog、launch 权威校验、admin 上下架/回滚。
- 二楼改统一 Entry；Facade 分发 dapp/finclip；ETag 缓存与本地最近。
- `sok.getSession` + issue/introspect。
- 服务到服务 introspect 鉴权、埋点与基础错误处理。

### Phase 2：增强

- 收藏/最近云端同步；灰度；分享/扫码扩展 API。
- 凭据轮换、远程配置健壮性增强；私有化 `apiServer`。
- 钱包相关能力（单独评审）。

---

## 10. 风险与对策

| 风险 | 对策 |
|------|------|
| BundleID / KEY 不匹配导致打不开 | 启动自检日志；文档化 checklist；分端密钥 |
| 模拟器架构问题 | 优先真机；Podfile 排除 arm64 simulator（按官方） |
| SECRET 泄露 | CI 注入 + git-secrets；泄露则控制台轮换 |
| 把编译期配置误认为安全存储 | 在安全评审中明确移动端可提取性；依赖应用绑定、控制台最小权限、监控、轮换和后端业务授权，而非“隐藏 SECRET” |
| 小程序越权拿长期 token | 只发短期 ticket + aud 绑定 AppID |
| catalog 缓存让已下架条目仍可打开 | 打开前 `launch` 和 `session/issue` 权威校验；紧急下架立即拒绝 |
| 目录 CDN 缓存串用户灰度数据 | 用户维度目录设为 private 或按安全 cache key 分区，响应正确声明 `Vary` |
| 小程序包供应链风险 | FinClip 控制台 RBAC/MFA、发布审批、版本审计与回滚演练；仅允许登记 AppID |
| 与 DApp WebView 体验不一致 | 统一入口卡片样式；打开动画尽量一致 |
| SDK 体积与启动耗时 | 懒加载 init；评估包体增量 |

---

## 11. 验收标准

1. 真机 iOS/Android 可打开 FinClip 示例或自有小程序。
2. 二楼同时存在 DApp 与 FinClip 条目，点击行为正确。
3. 未登录用户无法对 `login_required` 条目拿到 session。
4. 下架条目从 catalog 消失，无法通过旧 id 打开（服务端拒绝 issue）。
5. 扩展 API `sok.getSession` 票据可被 introspect，过期后失败。
6. 无 SECRET / token 进入日志与仓库。
7. 生产签名 iOS/Android 真机均验证通过；模拟器仅作为补充，不作为发布依据。
8. 紧急下架后，缓存条目在下一次 `launch` 立即被拒绝；最近/收藏不再可打开。
9. 登录用户 A 登出并以用户 B 登录后，B 无法读取 A 的 session、最近记录或 SDK 用户上下文。
10. `introspect` 不能被匿名客户端调用，且 ticket 的 `aud`/`entryId` 不匹配时被拒绝。

---

## 12. 参考

- 产品：[https://finclip.com/](https://finclip.com/)
- Flutter 集成：[文档](https://www.finclip.com/mop/document/zh/runtime-sdk/flutter/flutter-integrate.html)
- SDK：[pub.dev/packages/mop](https://pub.dev/packages/mop) · [GitHub](https://github.com/finogeeks/mop-flutter-sdk)
- 现有二楼：`lib/im/pages/second_floor/` · `conversation_view.dart`（`_openSecondFloorDapp`）
- 配置：`lib/wallet/config/app_config.dart`（`SOK_BASE_URL` 等）

---

## 13. 待确认问题

请产品/后端确认后即可进入实现计划：

1. FinClip 使用 **SaaS** 还是已有私有化部署？
2. 二楼是否 **DApp + 小程序混排**，还是独立「小程序」Tab？
3. v1 是否必须 **云端收藏/最近**，还是仅本地？
4. 小程序是否需要 **支付 / 扫码 / 分享**（影响 Phase 边界）？
5. SDK KEY/SECRET 是否使用 **CI dart-define**（推荐 v1，仅防 Git 泄露）？若要求远程下发，需要接受其仍可被客户端提取，并给出设备证明/轮换要求。
6. 小程序业务后端的责任方、数据地区和与 SOK 后端的服务到服务认证方式是什么？
7. iOS/Android 生产签名、BundleID/应用签名是否已确定？FinClip 控制台是否支持对应的发布与密钥轮换流程？
