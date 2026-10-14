# Implementation Notes

## 2026-10-14 — Initial requestfilter module

- Created the standalone module `github.com/winezer0/requestfilter` with a single public root package. The package uses only the Go standard library and does not import Tanscan or Burp types.
- Exact request deduplication is always active. Parameter parsing is opt-in through `ParameterPolicyNone`, `ParameterPolicyReport`, or `ParameterPolicyCoverage`; this keeps active-scan callers from dropping requests that differ only by values.
- Request fingerprints use length-prefixed SHA-256 fields. URL canonicalization lowercases host/scheme, removes default ports and fragments, and preserves path escapes, trailing slashes, query ordering, and repeated query values.
- Authentication partitioning uses caller-supplied non-secret `AuthContextID`. Raw credentials and request parameter values are not retained in filter state or returned in decisions.
- Batch filtering and streaming sessions share `Session.Reserve`/`Commit` logic. Streaming state is local to a caller-created session. `Commit` records downstream acceptance; `Release` and lazy reservation expiry permit retries.
- `MaxEntries` counts committed and pending request fingerprints, coverage endpoint records, committed parameter names, and per-reservation parameter references. Counting pending references keeps concurrent streams within the configured memory bound.
- Entry TTL and reservation expiry use lazy cleanup on the next session operation. This avoids package-owned background goroutines; callers should close sessions when done.
- JSON parameter paths use escaped JSON Pointer segments and include array indexes. Incomplete parameter parsing disables coverage mutation/filtering for that request while exact deduplication remains available.
- The initial API returns an empty batch result on malformed input to avoid exposing a partially applied batch. Capacity rejection is returned as an individual decision so other batch entries can still be evaluated.

## 2026-10-14 — Unified body parameter parsing

- Kept the public API in the root package and added an internal media-type parser for URL-encoded forms, JSON, XML, and multipart form data.
- XML coverage uses leaf-element and attribute paths with expanded namespace names, with a 64-level nesting limit. Repeated same-name siblings receive stable zero-based indexes.
- Multipart coverage records ordinary field names and file field names separately, including file parts with an empty filename parameter. File names and file contents are not retained; part count and total body size have independent bounds.
- XML parsing uses a bounded temporary tree so the parser can decide which siblings need indexes before emitting paths; its node bound counts elements and non-namespace attributes.
- Body parameter, XML node/depth, and multipart part limits report `ParameterStatusPartial`; URL and total request-body hard limits remain input errors. Unsupported or malformed bodies also report `ParameterStatusPartial`, so coverage mode falls back to exact raw-request deduplication.
- Added table-driven HTTP method identity tests and parser/fallback cases for JSON, XML, forms, multipart uploads, malformed input, and unknown binary media types.
- Added a `net/http` caller adapter example to the README. It restores the consumed body, marks it captured only after a complete bounded read, and documents handling for in-flight retries and capacity rejection. Persistence, cross-process coordination, and finer header fingerprinting remain integration-driven extensions.
- The adapter bypasses `Reserve` when body capture fails or exceeds the configured limit. An absent body cannot support exact comparison of unseen bytes, even though `BodyCaptured` distinguishes unknown from known-empty bodies in the fingerprint.

## 2026-10-14 — Full verification

- Ran `go build ./...` successfully.
- Ran the complete `go test ./... -cover` suite successfully: 82 tests passed in one package; statement coverage was 89.5%.
- Ran `go test -race ./...` successfully: 82 tests passed in one package.
- Used the repository-local `GOCACHE=go-build` and `GOMODCACHE=.gomod-cache` directories. The commands are documented in the README test section.
