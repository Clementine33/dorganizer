# 0009 业务分包与依赖方向

Date: 2026-09-23

Status: Draft

后端原来按「层」组织：`internal/repo/sqlite`、`internal/httpapi`、`internal/usecase`、`internal/tasks`、`internal/services`。分层的名字掩盖了实际形状——HTTP 处理器直接持有 `*sqlite.Repository`，工作集用例在自己身上定义记录类型，转换任务在 `sqlite` 包里读写 `entries` 表；包的边界不是业务的边界，改一条业务规则要在三四个目录之间来回。

记录决定：**按业务分包，依赖方向由消费者声明端口**。每个业务模块（`library`、`inventory`、`workset`、`conversion`、`fileops`）拥有自己的类型、自己的用例、以及一份「我需要什么」的接口；`internal/adapters/…` 实现这些接口，`internal/app` 是唯一知道全部适配器的装配根。由此得到下面几条可被机器检查的规则（`internal/arch` 用 `go list -deps` 断言它们）。

## 1 依赖只向一个方向流动

`app → adapters → business → pathnorm`。业务包不得 import `internal/adapters/…`，也不得 import `internal/app`。一个具体的 `*sqlite.Repository` 满足所有端口，但它只在装配根被提到；业务包看到的是 `library.Store`、`workset.Store`、`conversion.Inventory` 这类由消费方声明、恰好被同一个类型满足的接口。

端口按需取方法：一个模块只声明自己真正调用的那些，不按表拆 Repository，也不为「以后可能要读」预留方法。

## 2 复合事务整条留在 SQLite 内

「替换当前记录」「发布新计划并退役旧计划」「带条件创建执行会话」「同步库存及生成凭据」「整批 staging 原子写」各自是一个事务，其不变式调用方无法从外部重新建立。它们因此是端口上的单个方法，而不是被拆成若干次读写。若某个端口方法只能靠拆分事务实现，说明端口切错了——改端口，不改事务。

## 3 entries 表只有一个主人

扫描合并、执行后的库存同步、码率回写都写 `entries`，`conversion`、`workset` 与浏览路由都读它。写与读的接口因此全部归 `inventory`：`library` 只保留 `libraries` 表、根变更/删除规则与目录身份（`dir_id`），浏览的条目由 `inventory` 提供。同一张表有两个主人，迟早会再搬一次。

## 4 接缝两侧的类型由内容的一方拥有

`workset` 拥有操作生命周期（身份、归属、版本、草稿、修订、计划、执行会话）并声明 `Task` 接缝；`conversion` 实现它。因此接缝上的类型（记录、成员、草稿、修订、计划、执行会话、`CanonicalJSONHash`）都在 `workset`，而任务自己的载荷是不透明 JSON。反方向的依赖（`workset → conversion`）不存在，也不允许存在。

## 5 冻结数据逐字节不动

`CanonicalJSONHash` 的规范化（`UseNumber`、键排序、数组保序、原始字节回落）、草稿文档 schema v1、NUL 连接的标签快照、`{delete_mode}` 会话选项、逐组件结果行，都是已落库的数据。搬迁它们时逐字节照搬，任何「顺手清理」都会让旧哈希对不上，从而改变幂等重放与历史修订的读取。

## 6 准入的申请时机是契约的一部分

进程级的准入把「扫描 / 规划 / 执行」与「直接文件管理」互斥起来。申请必须发生在响应提交**之前**（否则 409 会变成 200 之后流内的一条 `event: error`），而三种扫描入口的检查各不相同：全库扫描先查「执行中」，页面成员刷新只取扫描槽，写后刷新两者都不取（它已在槽内）。把三者归一成一次 `Acquire` 会改变可见行为。

## 7 传输层只做解码与映射

`internal/adapters/httpapi` 不 import 存储适配器，也不做业务判定：路由解析出的参数交给业务入口，业务入口返回的错误信封被映射成状态码与错误码。校验规则（策略是否完整、槽名长度、标签是否为空）随其所属模块走，不留在处理器里。

## 被否决的替代方案

- **先搬全部类型、再立接口（水平五阶段）**：搬类型的同一刻 `sqlite` 需要 `workset` 的类型，而 `workset` 仍需要 `sqlite.Repository`，互相 import。原文用「短期别名」绕开它，但本模块没有 `internal/` 之外的消费者，别名纯属额外工序与待删残留。改为垂直切片：类型与端口同步落地。
- **按表拆 Repository（`LibraryRepository`、`EntriesRepository`…）**：表不是业务边界，一张表常被多个模块读写；按表拆会把 §2 的复合事务拆散，并把「谁拥有这张表」这个问题原样留下。
- **让 `httpapi` 依赖具体的 Repository，只把 SQL 挪进方法**：请求路径仍旧直达数据库，业务规则（准入顺序、错误码）继续散在处理器里；这正是重构要消除的形状。
- **为 `rename_noreplace_*.go` 造一个端口搬进 `adapters/filesystem`**：三个未导出平台文件，唯一消费者是 `fileops`，本身没有业务逻辑；搬走换不来解耦，只多一个端口。
