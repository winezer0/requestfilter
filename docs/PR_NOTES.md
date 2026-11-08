# 上游 PR 答辩要点：为什么泛化只放在 DedupStrategyCoverageParameters

本文预置评审中最可能被追问的问题及答复，随 `feat(coverage): learned dynamic
path-segment generalization` 提交一并提供。

## Q1: 为什么不做成正交开关（Options 级别），让 RequireParameters 也受益？

`DedupStrategyRequireParameters` 的分组 key 是**精确端点**，其语义是「每个具体
路径的无参请求只测一次」。引入路径泛化会把它改变为「每个模式只测一次」，这是对
该策略核心语义的修改，而不是能力增强。

`DedupStrategyCoverageParameters` 的分组 key 本来就是**形状**（参数位置/名称集
合，值不敏感）。路径段泛化在抽象层面与之同构：把路径段视为一种值不敏感的形状成
员（`ParameterLocation=path_segment`）。因此泛化只属于形状覆盖策略，是该语义的
自然扩展而非新能力。

正交开关形态可作为未来演进（若出现 RequireParameters + 泛化的真实需求），本 PR
刻意不引入，以保持「一次只选一种策略」的既有约束与语义稳定性。

## Q2: 为什么不新增独立策略 DedupStrategyPathPattern？

受「每次只选一种策略」约束，独立策略意味着泛化无法与参数覆盖组合——而组合正是
爬虫场景的真实需求（既防 `/users/{dyn}` 膨胀，又防同形状参数重复）。独立策略还
会把泛化与覆盖的状态机复制一份，增加维护面。

## Q3: 学习参数与解析参数并存，是否破坏 ParameterStatus 语义？

路径段学习总是执行（用于判定折叠），但只有 `ParameterStatusComplete` 的请求才把
`ParameterPath` 参数并入覆盖判定。`Partial` 请求（未知媒体类型、未捕获正文、解析
超限）照常参与学习、不参与覆盖、不记录形状——与既有「Partial 不记录状态」的保守
语义一致。API 不返回参数名称集合，合成参数对调用方不可见。

## Q4: 折叠瞬间的行为是否定义明确？

定义明确，见 docs/API.md「泛化的两条行为边界」：

1. 折叠后泛化组的覆盖从零开始，学习期已提交的形状在泛化组内可能再次放行一次
   （爬虫期望此行为；扫描器侧为可容忍的少量重复）。
2. 学习/折叠状态计入 `MaxEntries`，按 `EntryTTL` 过期，容量耗尽走既有
   `ActionReject` 路径。

## 测试覆盖

`pathpattern_test.go`（9 例）：学习期放行、阈值折叠、父路径隔离（/users 与
/items 独立学习）、与 query 覆盖混合、尾斜杠等价、嵌套动态段递归泛化、认证组隔
离、选项校验（-1/4097 拒绝）、`ParameterPath` 位置产生。上游既有全量测试零回归。
