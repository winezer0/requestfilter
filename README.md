# requestfilter

独立的 Go HTTP 请求过滤工具包，支持批量和流式过滤。提供四种互相独立的策略：`DedupStrategyNone`、`DedupStrategySelectedFieldsHash`、`DedupStrategyRequireParameters` 和 `DedupStrategyCoverageParameters`。认证信息是过滤时的分组条件，不是单独的策略。

**API 参考：**[查看完整 API 文档](docs/API.md)，包括批量和流式调用、策略语义、认证分组、输入限制及错误处理。

## 安装

```shell
go get github.com/winezer0/requestfilter
```

## 批量过滤

批量模式按输入顺序处理请求。`DedupStrategyNone` 是零值且不做过滤；其他策略每次只选一种，不相互叠加。

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
		DedupStrategy: requestfilter.DedupStrategyCoverageParameters,
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

对应的适配实现及成功读体、读体错误、超限恢复、HTTP/HTTPS URL 映射、chunked 正文保真和端到端 Reserve/Commit/认证 Cookie 分组测试位于 `internal/nethttpadapterexample`，由 `go test ./...` 一并编译和验证。

如果请求体没有完整捕获，不要把缺失或截断的内容送入 `Reserve` 并期待精确去重：过滤器无法凭空区分正文不同、但可见部分相同的请求。示例在读体失败或超限时不调用过滤器。`BodyCaptured: false` 表示正文未知，不应被当作“已确认的空请求体”。

过滤器接收的是已解析的 `Request` 字段，不解析原始 HTTP 字节流。通过 `net/http` 接入时，chunked framing 通常已由 HTTP server 解码；HTTP/2 伪头也应映射到 `Method`、URL 和普通头字段。`CONNECT host:port` 这样的 authority-form 和 `OPTIONS *` 这样的 asterisk-form 不能直接作为 `URL` 传入；集成方需先决定它们对应的目标 URL/资源身份，或在进入本包前旁路处理。本包不负责验证原始报文行、传输分块或 HTTP/2 framing。

动作处理要点：

- `ActionAllow`：下游确认接收后调用 `Commit`；下游明确拒绝时调用 `Release`，保留重试机会。
- `ActionRetry`：已有匹配请求仍处于预留状态，稍后重新调用 `Reserve`；可以排队或返回可重试状态，避免忙循环。
- `ActionReject`：会话容量不足，过滤器没有记录该请求。应施加背压或明确选择旁路策略，不能把它当成 `ActionDrop`。
- `ActionDrop`：请求已重复或覆盖，可按调用协议返回成功的空响应。

持久化状态、跨进程协调和更细粒度的请求头指纹，应由实际集成需求驱动；当前核心只维护单进程会话状态。

## 请求字段与规范化

`DedupStrategySelectedFieldsHash` 根据大写方法、规范化 URL、Content-Type、请求体捕获状态和请求体原始字节计算 SHA-256。`AdditionalHashHeaderNames` 在这些默认字段上追加指定请求头，不替换默认字段。认证分组单独应用，不包含在 `Decision.Fingerprint` 中。

- 默认方法为空时按 `GET` 处理。URL 只接受 HTTP/HTTPS，拒绝 userinfo；主机名转小写，移除默认端口和 fragment，保留路径尾斜杠、转义形式、query 顺序和重复参数。
- `Content-Type` 规范化媒体类型及参数；额外请求头名大小写不敏感，多个值按原顺序参与摘要。
- `BodyCaptured` 区分未捕获请求体和已捕获的空请求体。非空 `Body` 必须标记为已捕获。
- 指纹采用带长度边界的 SHA-256 字段摘要，避免直接保存原始 Cookie、Token 或请求体。

## 策略行为

| 当前策略名称 | 判定依据 | 首次或新形状请求 | 已记录的重复请求 | 解析不完整时 |
| --- | --- | --- | --- | --- |
| `DedupStrategyNone` | 不比较请求 | 放行 | 放行 | 放行 |
| `DedupStrategySelectedFieldsHash` | 方法、规范化 URL、Content-Type、正文捕获状态和正文；可追加指定请求头 | 放行；下游接收并 `Commit` 后记录 | 已提交的相同请求丢弃 | 不适用参数解析 |
| `DedupStrategyRequireParameters` | 完整解析后判断参数集合是否为空；按认证组、方法、scheme/host/port、路径和媒体类型分组 | 每组首个完整无参请求放行；带参数请求也放行且不记录 | 后续已提交的无参请求丢弃 | 放行且不记录 |
| `DedupStrategyCoverageParameters` | 按认证组、方法、scheme/host/port、路径、媒体类型及参数位置/名称比较；忽略参数值 | 新参数名称放行；已确认无参请求的空集合也算新形状 | 形状已被完整覆盖的请求丢弃 | 放行且不记录 |

策略状态只在允许的请求成功 `Commit` 后生效。未完成的同一请求或参数预留会得到 `ActionRetry`。`DedupStrategyCoverageParameters` 中，`/xxx?a=1` 与 `/xxx?a=20` 的参数位置和名称相同，因此后一个请求在前一个已提交后会被丢弃；出现新的参数位置或名称时，请求会放行并在提交后扩展覆盖状态。

参数名称按来源解析。Query 和 URL-encoded form 名称按标准表单规则解码；重复的同位置同名参数折叠为一个名称，空名称会保留。非法百分号转义会使解析状态变为 `partial`。JSON 对象递归展开为 JSON Pointer 路径（例如 `/user/id`、`/items/0/id`）；数组索引纳入名称，顶层标量和空对象/数组用 `$` 表示。同一对象内的重复键折叠为一个路径。XML 记录叶子元素及属性路径，例如 `/user/id`、`/user/@role`；同级同名重复元素加从 0 开始的索引，命名空间以 `{URI}name` 纳入路径。Multipart 记录普通字段名和文件字段名，不保留字段值、文件名或文件内容；重复的同位置同名部件折叠为一个名称，`filename*` 也识别为文件字段。

未知媒体类型、未捕获正文、解析错误或解析资源超限会产生 `ParameterStatusPartial`。`DedupStrategyRequireParameters` 和 `DedupStrategyCoverageParameters` 在此情况下直接放行且不记录策略状态。`BodyCaptured: false` 对所有 HTTP 方法都表示正文未知；已确认无正文时应设置为 `true`。请求正文总大小受 `MaxBodyBytes` 限制。

## 认证上下文

启用任一过滤策略时，包会自动按 Cookie 头、Authorization 头，以及名称包含 `token`、`auth`、`sessid`、`session` 的请求头分组；匹配头名和 Authorization 方案名时不区分大小写。`Proxy-Authorization` 始终排除，避免代理凭据误分目标站点。Cookie 项会跨重复 Cookie 头合并，并按名称和值排序后参与分组，因此 Cookie 行和项的顺序变化不改变身份。其他认证头的多个值按输入顺序参与身份。`AdditionalAuthenticationHeaderNames` 可添加如 `X-API-Key` 等自定义头。调用方也可通过 `AuthenticationGroupID` 添加稳定的非秘密分组 ID；该 ID 会与自动识别的头组合。认证值只用于会话内 HMAC 分组，不会存入去重状态或写入判定结果。

## 状态限制与错误

`Options` 的零值会采用以下上限：会话状态 10000 项、URL 16 KiB、请求体 8 MiB、每个请求参数 4096 项、JSON/XML 深度各 64 层、XML 元素与属性名总数 10000 个、multipart 部件 4096 个、预留有效期 30 秒。解析器自己的深度、节点、参数或部件上限会将状态标为 `partial`；两种参数策略在不完整解析时放行。URL 和请求体硬上限仍会返回 `ErrInvalidRequest`。`MaxEntries` 统计字段摘要记录、无参端点记录、覆盖端点/参数记录和未完成的预留引用；容量不足时不会清空已有状态。

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

Run the parser and URL fuzz targets with a bounded local smoke period; `NO_TESTS` avoids shell-specific escaping of Go's empty test-name regular expression:

```powershell
go test -run=NO_TESTS -fuzz=FuzzExtractParameters -fuzztime=30s -parallel=2
go test -run=NO_TESTS -fuzz=FuzzNormalizeURL -fuzztime=30s -parallel=2
```

运行参数解析、参数覆盖会话和字段摘要批处理基准，并显示内存分配情况：

```powershell
go test -run=NO_TESTS -bench=Benchmark -benchmem -benchtime=1s -count=3 ./...
```

`testdata/reference-corpus.json` pins regression requests to the reference project's source snapshot and records the source-derived expectation for each case. The reference delegates raw request parsing to Burp's `IExtensionHelpers`; the corpus marks results that cannot be confirmed from checked-in source instead of presenting them as Burp runtime observations.
