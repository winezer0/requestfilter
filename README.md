# requestfilter

独立的 Go HTTP 请求过滤工具包，支持一次性批量过滤和逐条流式过滤。提供 HASH、PARAM、SMART 三种互相独立的请求去重策略，认证上下文作为独立分组条件。

设计参考了 [passive-scan-client-plus](https://github.com/winezer0/passive-scan-client-plus) 的请求去重思路；本项目使用独立的 Go API 和实现，不依赖 Burp Suite 或 Tanscan。

## 安装

```shell
go get github.com/winezer0/requestfilter
```

## 批量过滤

批量模式按输入顺序处理请求。未选择策略时不做过滤；HASH、PARAM、SMART 各自独立启用，不叠加其他策略。

```go
package main

import (
	"fmt"

	"github.com/winezer0/requestfilter"
)

func main() {
	result, err := requestfilter.FilterBatch([]requestfilter.Request{
		{Method: "GET", URL: "https://example.test/items?id=1"},
		{Method: "GET", URL: "https://example.test/items?id=1"},
		{Method: "GET", URL: "https://example.test/items?id=2"},
	}, requestfilter.Options{DedupStrategy: requestfilter.DedupStrategySelectedFieldsHash})
	if err != nil {
		panic(err)
	}
	fmt.Println(len(result.Accepted)) // 2
}
```

`BatchResult.Decisions` 与输入一一对应。非法请求会使 `FilterBatch` 返回错误和空结果；达到会话容量时，对应请求得到 `ActionReject`，其他输入仍会继续处理。

## 流式过滤与重试

每个独立流量源、任务或认证上下文创建一个会话。先预留请求，再调用下游；下游确认接收后提交，失败时释放预留以便重试。

```go
session, err := requestfilter.NewSession(requestfilter.Options{})
if err != nil {
	panic(err)
}
defer session.Close()
sendToScanner := func() error { return nil }

decision, reservation, err := session.Reserve(requestfilter.Request{
	Method: "POST",
	URL:    "https://example.test/login",
	Header: map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
	Body: []byte("user=alice"), BodyCaptured: true,
})
if err != nil {
	panic(err)
}
if decision.Action == requestfilter.ActionAllow {
	if err := sendToScanner(); err != nil {
		_ = session.Release(reservation)
		return
	}
	if err := session.Commit(reservation); err != nil {
		panic(err)
	}
}
```

`ActionRetry` 表示同一请求或参数目前仍有未完成的预留；调用方可稍后重试。`ActionDrop` 表示精确重复或已覆盖的参数集合；`ActionReject` 表示容量不足。`Commit` 代表下游已接受请求，不代表扫描完成。

## `net/http` 调用方适配

下面的服务端示例将收到的 HTTP 请求映射为 `requestfilter.Request`，在转发给扫描器之前过滤。请求体必须读取到 EOF 才能标记 `BodyCaptured: true`；读体错误或超过过滤器上限时，不提交给过滤器。示例把超限请求作为入口错误返回，集成方也可以按自己的策略将其送入旁路队列。

```go
const maxCapturedBodyBytes int64 = 8 << 20
var errBodyTooLarge = errors.New("request body exceeds filter limit")

func newFilter() (*requestfilter.Session, error) {
	return requestfilter.NewSession(requestfilter.Options{
		DedupStrategy: requestfilter.DedupStrategyParameterShapeCoverage,
		MaxBodyBytes:   int(maxCapturedBodyBytes),
	})
}

func toFilterRequest(r *http.Request, authenticationGroupID string) (requestfilter.Request, error) {
	if r.URL == nil {
		return requestfilter.Request{}, errors.New("missing request URL")
	}
	url := r.URL.String()
	if !r.URL.IsAbs() {
		if r.Host == "" {
			return requestfilter.Request{}, errors.New("missing request host")
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		absoluteURL := *r.URL
		absoluteURL.Scheme = scheme
		absoluteURL.Host = r.Host
		url = absoluteURL.String()
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxCapturedBodyBytes+1))
	if err != nil {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		return requestfilter.Request{}, err
	}
	if int64(len(body)) > maxCapturedBodyBytes {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		return requestfilter.Request{}, errBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	return requestfilter.Request{
		Method:        r.Method,
		URL:           url,
		Header:        r.Header.Clone(),
		Body:          body,
		BodyCaptured:  true,
        AuthenticationGroupID: authenticationGroupID, // Use a stable, non-secret identity from the caller.
	}, nil
}

func handle(w http.ResponseWriter, r *http.Request, filter *requestfilter.Session) {
	filtered, err := toFilterRequest(r, "credential-profile-7")
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "request cannot be filtered", status)
		return
	}

	decision, reservation, err := filter.Reserve(filtered)
	if err != nil {
		http.Error(w, "filter failed", http.StatusBadRequest)
		return
	}
	switch decision.Action {
	case requestfilter.ActionDrop:
		w.WriteHeader(http.StatusNoContent)
	case requestfilter.ActionRetry:
		// This sample returns a retryable status; a queueing caller can enqueue and retry later.
		http.Error(w, "request is already being processed", http.StatusServiceUnavailable)
	case requestfilter.ActionReject:
		// Capacity means this request was not tracked. Apply backpressure or an explicit bypass policy.
		http.Error(w, "filter capacity reached", http.StatusServiceUnavailable)
	case requestfilter.ActionAllow:
		if err := sendToScanner(r); err != nil {
			_ = filter.Release(reservation)
			http.Error(w, "scanner unavailable", http.StatusBadGateway)
			return
		}
		if err := filter.Commit(reservation); err != nil {
			http.Error(w, "filter reservation expired", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
```

示例中的 imports 需要包含 `bytes`、`errors`、`io`、`net/http` 和 `github.com/winezer0/requestfilter`；`sendToScanner` 由调用方实现。生产代码应让 `maxCapturedBodyBytes` 与 `Options.MaxBodyBytes` 一致。通过 TLS 反向代理接入时，应从可信代理配置确定外部 scheme，不要直接信任任意请求的 `X-Forwarded-Proto`。`AuthenticationGroupID` 应来自认证配置或主体的稳定非秘密 ID，不要传 Cookie 或 Token。

如果请求体没有完整捕获，不要把缺失或截断的内容送入 `Reserve` 并期待精确去重：过滤器无法凭空区分正文不同、但可见部分相同的请求。示例在读体失败或超限时不调用过滤器。`BodyCaptured: false` 表示正文未知，不应被当作“已确认的空请求体”。

动作处理要点：

- `ActionAllow`：下游确认接收后调用 `Commit`；下游明确拒绝时调用 `Release`，保留重试机会。
- `ActionRetry`：已有匹配请求仍处于预留状态，稍后重新调用 `Reserve`；可以排队或返回可重试状态，避免忙循环。
- `ActionReject`：会话容量不足，过滤器没有记录该请求。应施加背压或明确选择旁路策略，不能把它当成 `ActionDrop`。
- `ActionDrop`：请求已重复或覆盖，可按调用协议返回成功的空响应。

持久化状态、跨进程协调和更细粒度的请求头指纹，应由实际集成需求驱动；当前核心只维护单进程会话状态。

## HASH 字段与规范化

`DedupStrategySelectedFieldsHash` 默认对大写方法、规范化 URL、Content-Type、请求体捕获状态和请求体原始字节计算 SHA-256。`AdditionalHashHeaderNames` 为默认字段追加指定请求头；它不替换默认字段。认证分组独立应用，不纳入请求字段列表。

- 默认方法为空时按 `GET` 处理。URL 只接受 HTTP/HTTPS，拒绝 userinfo；主机名转小写，移除默认端口和 fragment，保留路径尾斜杠、转义形式、query 顺序和重复参数。
- `Content-Type` 规范化媒体类型及参数；额外请求头名大小写不敏感，多个值按原顺序参与摘要。
- `BodyCaptured` 区分未捕获请求体和已捕获的空请求体。非空 `Body` 必须标记为已捕获。
- 指纹采用带长度边界的 SHA-256 字段摘要，避免直接保存原始 Cookie、Token 或请求体。

## 去重策略

`DedupStrategyNone` 是默认值，不过滤请求。

`DedupStrategySelectedFieldsHash` 使用上述默认字段和附加请求头做精确摘要判重。摘要相同的请求只放行一次。

`DedupStrategyRequireParameters`（PARAM）对完整解析且无参数的请求按认证组、方法、路径和媒体类型分组：每组首次请求放行，后续重复请求过滤。带参数请求放行且不记录状态。解析不完整时直接放行，不执行 HASH 回退。

`DedupStrategyParameterShapeCoverage`（SMART）按方法、源站、路径、内容类型、认证分组及参数位置/名称集合判重，忽略参数值。比如 `/xxx?a=1` 与 `/xxx?a=20` 形状相同；空参数集合也视为一种形状。同一形状只有首次成功提交的请求放行。解析不完整时直接放行，不执行 HASH 回退。

### 与参考去重模式的对应

| 参考模式 | 本包对应项 | 行为说明 |
| --- | --- | --- |
| HASH | `DedupStrategySelectedFieldsHash` | 默认字段加调用方指定的请求头字段。 |
| PARAM | `DedupStrategyRequireParameters` | 完整解析且无参数时，每个认证组/方法/路径/媒体类型组合首次放行，重复请求过滤；带参数和解析不完整的请求放行。 |
| SMART | `DedupStrategyParameterShapeCoverage` | 按参数位置和名称覆盖判重；解析器与端点分组细节可能不同。 |
| AUTH | `AuthenticationGroupID` | 调用方提供非秘密认证组标识，作为所选策略的独立分组条件。 |
JSON 对象递归展开为 JSON Pointer 路径（例如 `/user/id`、`/items/0/id`）；数组索引纳入名称。XML 记录叶子元素及属性路径，例如 `/user/id`、`/user/@role`；同级同名重复元素加从 0 开始的索引（如 `/user/item/0`），命名空间以 `{URI}name` 纳入路径。multipart 只记录普通字段名和文件字段名，不保留字段值、文件名或文件内容；`MaxMultipartParts` 单独限制部件数。

未知媒体类型、未捕获的请求体或解析不完整时，参数状态为 `partial`。PARAM 和 SMART 在此情况下直接放行。`BodyCaptured: false` 对所有 HTTP 方法都表示请求体未捕获；已确认无请求体时应设置为 `true`。XML 使用 Go 标准库解析，非 UTF-8 编码等无法解析的 XML 会走该回退行为。请求体总大小受 `MaxBodyBytes` 限制。

## 认证上下文

调用方使用 `AuthenticationGroupID` 区分不同登录态。该字段必须是非秘密、不透明的标识，例如凭证配置 ID；不要传入 Cookie、Token 或密码。若调用方需要按凭证内容隔离，应在包外生成不暴露原值的标识。认证组不同的请求不会互相去重。

## 状态限制与错误

`Options` 的零值会采用以下上限：会话状态 10000 项、URL 16 KiB、请求体 8 MiB、每个请求参数 4096 项、JSON/XML 深度各 64 层、XML 元素与属性名总数 10000 个、multipart 部件 4096 个、预留有效期 30 秒。解析器自己的深度、节点、参数或部件上限会将状态标为 `partial`；PARAM 和 SMART 在不完整解析时放行。URL 和请求体硬上限仍会返回 `ErrInvalidRequest`。`MaxEntries` 统计已记录的 HASH 项、PARAM 无参端点、SMART 覆盖端点/参数和未完成的预留引用；容量不足时不会清空已有状态。

`EntryTTL` 控制已提交记录的空闲过期时间，零值表示不过期。预留过期和记录 TTL 都在下一次会话操作时惰性清理，不启动后台 goroutine。调用方应在生命周期结束时调用 `Close`。会话只保存在进程内，进程重启后状态不会保留。

公开错误包括 `ErrInvalidOptions`、`ErrInvalidRequest`、`ErrSessionClosed` 和 `ErrReservationNotFound`。错误文本不包含输入 URL、请求头、请求体或认证标识。

## 测试

在 PowerShell 中使用仓库固定的 Go 缓存目录运行构建和完整验证：

```powershell
$env:GOCACHE = Join-Path $PWD 'go-build'
$env:GOMODCACHE = Join-Path $PWD '.gomod-cache'

go build ./...
go test ./... -cover
go test -race ./...
go vet ./...
```
