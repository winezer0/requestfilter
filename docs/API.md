# Requestfilter API 参考

本文说明 Go 模块 `github.com/winezer0/requestfilter` 的公开 API。包接收调用方解析好的 HTTP 请求字段，提供批量过滤和进程内流式会话。它不发送 HTTP 请求，也不负责跨进程共享状态。

## 安装

```shell
go get github.com/winezer0/requestfilter@v0.1.0
```

模块要求 Go 1.24 或更新版本。`v1.0.0` 之前，公开 API 仍可能调整。

## 公开 API 一览

| API | 用途 |
| --- | --- |
| `Request` | 请求方法、绝对 URL、请求头、已捕获正文和可选的调用方身份标识。 |
| `Options` | 选择一种去重策略并设置解析、容量和过期限制。 |
| `FilterBatch` | 使用临时会话按输入顺序过滤一批请求。 |
| `NewSession` | 创建隔离且支持并发调用的流式会话。 |
| `Session.Reserve` | 判定请求并为放行的请求建立预留。 |
| `Session.Commit` | 下游接收请求后提交预留。 |
| `Session.Release` | 下游处理失败后释放预留，以便请求重试。 |
| `Session.Close` | 清理会话状态并禁止后续操作。 |
| `Decision` | 返回动作、原因、指纹（仅 HASH）和参数解析状态。 |
| `BatchResult` | 返回放行的请求和每个输入请求对应的判定。 |

其他导出类型包括 `DedupStrategy`、`Action`、`Reason`、`Reservation`、`Parameter`、`ParameterLocation` 和 `ParameterStatus`。导出的错误为 `ErrInvalidOptions`、`ErrInvalidRequest`、`ErrSessionClosed` 和 `ErrReservationNotFound`。

`Parameter` 由 `Location` 和 `Name` 组成，不含参数值。位置常量包括 `ParameterQuery`、`ParameterForm`、`ParameterJSON`、`ParameterXML`、`ParameterMultipartField` 和 `ParameterMultipartFile`。解析出的参数名称只用于内部策略判定；当前 API 不返回参数名称集合。`ParameterStatus` 可能为 `ParameterStatusNotRequested`、`ParameterStatusComplete` 或 `ParameterStatusPartial`。

## 请求和选项

`Request` 的字段如下：

| 字段 | 说明 |
| --- | --- |
| `Method` | HTTP 方法；空值按 `GET` 处理。 |
| `URL` | 完整的 HTTP 或 HTTPS URL。 |
| `Header` | `http.Header` 请求头。 |
| `Body` | 请求正文的字节。 |
| `BodyCaptured` | 是否已完整捕获正文；已确认无正文时也应设为 `true`。 |
| `AuthenticationGroupID` | 可选的稳定、非秘密调用方身份标识，会与内置认证来源共同分组。 |

`Options.DedupStrategy` 选择且只选择一种策略。其零值 `DedupStrategyNone` 表示不做过滤。

### 去重策略

| 策略 | 行为 |
| --- | --- |
| `DedupStrategyNone` | 放行所有有效请求。 |
| `DedupStrategySelectedFieldsHash`（HASH） | 对选定请求字段做精确摘要；相同且已提交的请求会被丢弃。 |
| `DedupStrategyRequireParameters`（PARAM） | 仅记录完整解析且没有参数的请求。每个端点组的首次请求放行，后续无参请求丢弃；带参数请求直接放行且不记录。 |
| `DedupStrategyParameterShapeCoverage`（SMART） | 按参数位置和名称集合覆盖判重，不比较参数值。已完整覆盖的请求会被丢弃。 |

PARAM 和 SMART 遇到不完整参数解析时直接放行，不回退到 HASH。SMART 将空参数集合视为一种形状：同组第一次已提交的无参请求放行，后续相同空形状请求丢弃。

### HASH 身份字段

HASH 默认使用大写后的方法、规范化 URL、规范化后的 `Content-Type`、`BodyCaptured` 状态和正文原始字节。`AdditionalHashHeaderNames` 会在这些默认字段上追加指定请求头，不会替换默认字段。

URL 仅支持 HTTP 和 HTTPS。规范化会将主机名转小写、移除默认端口和 fragment，并保留 query 顺序及重复参数；URL 中的 userinfo 会被拒绝。`Decision.Fingerprint` 是 HASH 请求字段的摘要，不含单独应用的认证分组，因此不能单独作为完整去重身份。

### PARAM 和 SMART 端点分组

参数策略按认证身份、方法、scheme/host/port、路径和媒体类型分组。支持识别 query、`application/x-www-form-urlencoded`、JSON、XML 和 multipart 字段/文件字段。参数值和上传文件内容不会作为参数名称状态保存。

未知媒体类型、未捕获正文、解析错误或解析资源超限会产生 `ParameterStatusPartial`。PARAM 和 SMART 对此类请求直接放行且不记录策略状态。其他状态为 `ParameterStatusComplete` 和 `ParameterStatusNotRequested`。

## 认证分组

启用任一过滤策略时，认证信息会先作为分组条件，再执行该策略。内置来源包括整条 `Cookie`、`Authorization`，以及名称包含 `token`、`auth`、`sessid` 或 `session` 的请求头（不区分大小写）。`Proxy-Authorization` 会被排除。通过 `AdditionalAuthenticationHeaderNames` 可补充应用专属请求头，例如 `X-API-Key`。

`Request.AuthenticationGroupID` 可增加调用方定义的分组维度。应使用稳定、非秘密的 ID，不要传入原始凭据。认证值通过会话密钥 HMAC 参与分组，不会作为原始值存入会话或返回到判定结果。选择 `DedupStrategyNone` 时不进行去重或认证分组。

## 批量过滤

`FilterBatch` 按输入顺序处理请求。`BatchResult.Decisions` 与输入一一对应；`BatchResult.Accepted` 只包含判定动作为 `ActionAllow` 的请求。函数会在临时会话内自动提交放行请求。

无效请求会使 `FilterBatch` 返回空结果和错误。容量不足以单个 `ActionReject` 表示，不会作为整个批次的错误。

```go
package main

import (
	"fmt"

	"github.com/winezer0/requestfilter"
)

func filterBatch() error {
	requests := []requestfilter.Request{
		{Method: "GET", URL: "https://example.test/items?id=1", BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/items?id=1", BodyCaptured: true},
	}
	result, err := requestfilter.FilterBatch(requests, requestfilter.Options{
		DedupStrategy: requestfilter.DedupStrategySelectedFieldsHash,
	})
	if err != nil {
		return err
	}
	fmt.Println(len(result.Accepted)) // 1
	return nil
}

func main() {
	_ = filterBatch()
}
```

## 流式会话

需要在确认下游结果后才记录去重状态时，使用 `Session`。下游接收成功后调用 `Commit`；下游失败时调用 `Release`，让请求可以重试。

```go
package main

import (
	"fmt"
	"net/http"

	"github.com/winezer0/requestfilter"
)

func sendDownstream() error    { return nil } // 由调用方实现
func retryLater() error        { return nil } // 由调用方实现
func applyBackpressure() error { return nil } // 由调用方实现

func handle() error {
	session, err := requestfilter.NewSession(requestfilter.Options{
		DedupStrategy: requestfilter.DedupStrategySelectedFieldsHash,
	})
	if err != nil {
		return err
	}
	defer session.Close()

	decision, reservation, err := session.Reserve(requestfilter.Request{
		Method: "POST",
		URL:    "https://example.test/items",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"id":1}`), BodyCaptured: true,
	})
	if err != nil {
		return err
	}

	switch decision.Action {
	case requestfilter.ActionAllow:
		if err := sendDownstream(); err != nil {
			_ = session.Release(reservation)
			return err
		}
		return session.Commit(reservation)
	case requestfilter.ActionDrop:
		return nil // 不转发重复请求
	case requestfilter.ActionRetry:
		return retryLater() // 匹配请求仍在处理中
	case requestfilter.ActionReject:
		return applyBackpressure() // 容量已满，请求未被记录
	default:
		return fmt.Errorf("unknown filter action %q", decision.Action)
	}
}

func main() {
	_ = handle()
}
```

只有 `ActionAllow` 返回有效的 `Reservation`。`ActionDrop`、`ActionRetry` 和 `ActionReject` 不返回预留。预留会在 `ReservationTTL` 后过期；对已过期、已提交、已释放或不属于当前会话的预留执行 `Commit`/`Release` 会返回 `ErrReservationNotFound`。`Close` 会使所有预留失效，并使后续会话操作返回 `ErrSessionClosed`。

## 动作和原因

| `Decision.Action` | 含义 | 常见 `Decision.Reason` |
| --- | --- | --- |
| `ActionAllow` | 可以转发；请求有一个有效预留。 | `ReasonNewRequest` |
| `ActionDrop` | 已提交请求或参数形状已覆盖本请求。 | `ReasonExactDuplicate`、`ReasonParameterCoverage`、`ReasonNoParameters` |
| `ActionRetry` | 匹配请求仍有未完成预留；本请求没有获得预留。 | `ReasonInFlight` |
| `ActionReject` | 会话无法再记录更多状态。 | `ReasonCapacity` |

不要把 `ActionReject` 当作重复请求。调用方应为容量不足选择背压或旁路策略。收到 `ActionRetry` 时应稍后重试或入队，避免忙循环。

## 请求正文和 HTTP 接入

`Request` 表示解析后的字段，不是原始 HTTP 字节流。调用方负责传入绝对 URL，并映射方法、请求头、正文和可选身份标识。

只有完整读到正文后才能设置 `BodyCaptured: true`。已确认无正文的请求使用空 `Body` 和 `BodyCaptured: true`；正文未知时使用空 `Body` 和 `BodyCaptured: false`。HASH 会区分这两种状态，参数策略会将未知正文视为解析不完整。非空正文配合 `BodyCaptured: false` 会返回 `ErrInvalidRequest`。

经 `net/http` 接入时，HTTP server 通常已解码 chunked framing。若适配器读取正文用于过滤，转发前必须恢复正文内容。TLS 在反向代理终止时，应从可信代理配置确定外部 scheme，不要直接信任任意 `X-Forwarded-Proto`。CONNECT authority-form 和 OPTIONS `*` 目标需要集成层确定请求身份或在调用本 API 前旁路。

## 默认限制、过期和错误

对应选项的零值采用以下默认限制：

| 选项 | 默认值 | 用途 |
| --- | ---: | --- |
| `MaxEntries` | 10,000 | 会话可记录的已提交及待处理状态项上限。 |
| `MaxURLBytes` | 16 KiB | URL 长度上限。 |
| `MaxBodyBytes` | 8 MiB | 捕获的请求正文长度上限。 |
| `MaxParameters` | 4,096 | 每个请求可提取的参数项上限。 |
| `MaxJSONDepth` | 64 | JSON 嵌套深度上限。 |
| `MaxXMLDepth` | 64 | XML 嵌套深度上限。 |
| `MaxXMLNodes` | 10,000 | XML 元素和非命名空间属性数量上限。 |
| `MaxMultipartParts` | 4,096 | multipart 部件数量上限。 |
| `ReservationTTL` | 30 秒 | 待处理预留的有效期。 |
| `EntryTTL` | 不过期 | 已提交状态的空闲过期时间；零值表示不过期。 |

解析器自身的资源上限会将参数状态标记为不完整；PARAM 和 SMART 随后直接放行。URL 或正文超过硬限制时返回 `ErrInvalidRequest`。策略、TTL、上限或配置请求头名称无效时返回 `ErrInvalidOptions`。错误文本不包含请求凭据或正文值。

会话状态只保存在当前进程和当前 `Session` 中，不持久化，也不跨进程共享。预留过期和 `EntryTTL` 清理由后续会话操作惰性触发，不启动后台 goroutine。所属流量源或任务结束时应调用 `Close`。

## 查看 Go 符号文档

可通过 Go 工具查看包和具体类型的源码注释：

```shell
go doc github.com/winezer0/requestfilter
go doc github.com/winezer0/requestfilter.Session
```
