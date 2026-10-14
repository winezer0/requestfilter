# AGENTS.md

@AGENTS.common.md

## 项目规范

1. 本仓库实现独立 Go 模块 `github.com/winezer0/requestfilter`，以 `独立请求过滤工具包设计方案.md` 为产品范围依据；它不是 Tanscan Web 平台。
2. 模块职责是提供批量和流式 HTTP 请求过滤 API。`DedupStrategyNone`、`DedupStrategySelectedFieldsHash`、`DedupStrategyRequireParameters` 和 `DedupStrategyCoverageParameters` 是互相独立的策略；未选择策略时不做去重。
3. `DedupStrategyRequireParameters` 与 `DedupStrategyCoverageParameters` 支持 query、`application/x-www-form-urlencoded`、JSON、XML 和 multipart 参数识别。两种参数策略遇到不完整解析时均放行且不记录策略状态。`DedupStrategySelectedFieldsHash` 使用固定默认字段并可追加请求头字段。
4. 本模块保持独立，仅依赖 Go 标准库，不导入 Tanscan、Burp 或扫描器实现；不负责发送 HTTP 请求、扫描任务管理、数据库存储、目标范围校验或 SSRF 防护。
5. 请求身份、URL 规范化、参数解析和会话状态必须遵守设计中的长度、数量、深度、容量与过期限制。认证分组支持内置 Cookie/Authorization/常见认证头、调用方追加头名及 `AuthenticationGroupID`；不得把原始凭据写入状态、判定或错误。
6. 测试重点覆盖策略隔离、`DedupStrategySelectedFieldsHash` 请求字段、内置/自定义认证头分组及 `Proxy-Authorization` 排除、`DedupStrategyCoverageParameters` 覆盖判重（包括空参数集）与不完整解析放行、`DedupStrategyRequireParameters` 首个完整无参端点请求放行及重复过滤、输入限制、批量错误原子性、预留/提交/释放、并发、容量、TTL、会话隔离及敏感信息边界。覆盖率要求遵循 `AGENTS.common.md`，不引入项目外的固定覆盖率目标。
7. Tanscan 适配不属于本模块当前范围。未来接入应通过公开 API 完成，并单独核实资产唯一键是否保留不同方法和请求体的语义；除非任务明确要求，不在本仓库实现 Tanscan 平台功能。
8. README、实现记录及仓库脚本应记录本模块实际使用的构建、测试和缓存命令。涉及未由设计明确的行为或重要 API/包边界决策时，更新 `implementation-notes.md`。
