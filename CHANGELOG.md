# Changelog

## Unreleased

### 修复

- **GET/HEAD/OPTIONS 无 body 自动视为 BodyCaptured**：此前漏设
  `BodyCaptured` 时无 body GET 的参数状态永远 Partial，覆盖判定与
  路径泛化整体静默失效（爬虫场景实际踩坑）。非幂等方法未捕获正文
  仍按 Partial 放行，带 body 未捕获仍报 ErrInvalidRequest。

## v0.1.0 — 2026-10-14

Initial pre-1.0 release of the standalone Go request filtering package.

- Provides `DedupStrategyNone`, `DedupStrategySelectedFieldsHash`, `DedupStrategyRequireParameters`, and `DedupStrategyCoverageParameters`.
- Supports authentication grouping, bounded session state, and streaming Reserve/Commit/Release operations.
- Uses only the Go standard library and requires Go 1.24 or newer.
- Public APIs may change before v1.0.0.
