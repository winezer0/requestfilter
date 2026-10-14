# Implementation Notes

## 2026-10-14 — Initial requestfilter module

- Created the standalone module `github.com/winezer0/requestfilter` with a single public root package. The package uses only the Go standard library and does not import Tanscan or Burp types.
- The current API selects one independent strategy: no filtering, selected-field HASH, parameter-required PARAM, or parameter-shape SMART. HASH is no longer an always-on baseline.
- Request fingerprints use length-prefixed SHA-256 fields. URL canonicalization lowercases host/scheme, removes default ports and fragments, and preserves path escapes, trailing slashes, query ordering, and repeated query values.
- Authentication partitioning uses caller-supplied non-secret `AuthenticationGroupID`. Raw credentials and request parameter values are not retained in filter state or returned in decisions.
- Batch filtering and streaming sessions share `Session.Reserve`/`Commit` logic. Streaming state is local to a caller-created session. `Commit` records downstream acceptance; `Release` and lazy reservation expiry permit retries.
- `MaxEntries` counts committed and pending request fingerprints, coverage endpoint records, committed parameter names, and per-reservation parameter references. Counting pending references keeps concurrent streams within the configured memory bound.
- Entry TTL and reservation expiry use lazy cleanup on the next session operation. This avoids package-owned background goroutines; callers should close sessions when done.
- JSON parameter paths use escaped JSON Pointer segments and include array indexes. Incomplete parameter parsing causes PARAM and SMART to allow without retaining strategy state; HASH is selected independently.
- The initial API returns an empty batch result on malformed input to avoid exposing a partially applied batch. Capacity rejection is returned as an individual decision so other batch entries can still be evaluated.

## 2026-10-14 — Unified body parameter parsing

- Kept the public API in the root package and added an internal media-type parser for URL-encoded forms, JSON, XML, and multipart form data.
- XML coverage uses leaf-element and attribute paths with expanded namespace names, with a 64-level nesting limit. Repeated same-name siblings receive stable zero-based indexes.
- Multipart coverage records ordinary field names and file field names separately, including file parts with an empty filename parameter. File names and file contents are not retained; part count and total body size have independent bounds.
- XML parsing uses a bounded temporary tree so the parser can decide which siblings need indexes before emitting paths; its node bound counts elements and non-namespace attributes.
- Body parameter, XML node/depth, and multipart part limits report `ParameterStatusPartial`; URL and total request-body hard limits remain input errors. Unsupported or malformed bodies also report `ParameterStatusPartial`; PARAM and SMART allow these requests without hash fallback.
- Added table-driven HTTP method identity tests and parser/fallback cases for JSON, XML, forms, multipart uploads, malformed input, and unknown binary media types.
- Added a `net/http` caller adapter example to the README. It restores the consumed body, marks it captured only after a complete bounded read, and documents handling for in-flight retries and capacity rejection. Persistence, cross-process coordination, and finer header fingerprinting remain integration-driven extensions.
- The adapter bypasses `Reserve` when body capture fails or exceeds the configured limit. An absent body cannot support exact comparison of unseen bytes, even though `BodyCaptured` distinguishes unknown from known-empty bodies in the fingerprint.

## 2026-10-14 — Ignore-empty parameter policy

- Added the standalone `ParameterPolicyIgnoreEmpty` policy, based on the reference PARAM mode. It drops requests only when parameter parsing completes with an empty extracted parameter set; incomplete parsing continues through exact request deduplication.
- Kept descriptive policy names and documented their approximate mappings to HASH, PARAM, SMART, and AUTH. `AuthContextID` remains caller-supplied.
- Added tests for parameterless requests, empty form bodies, parameterized requests, and malformed JSON fallback. The policy does not alter Coverage behavior.

This policy was superseded by the independent HASH/PARAM/SMART strategy API below; PARAM now passes incomplete parses without HASH fallback.

## 2026-10-14 — Remove parameter reporting API

- Removed `ParameterPolicyReport` and `Decision.NewParameters`; parameter collection without filtering had no role in this package's request deduplication API.
- Preserved the internal parameter-set tracking used by `ParameterPolicyCoverage`, so coverage filtering behavior is unchanged.
- Updated README, design, project rules, and coverage tests to describe and verify the remaining policies.

## 2026-10-14 — Full verification

- Ran `go build ./...` successfully.
- Ran the complete `go test ./... -cover` suite successfully: 82 tests passed in one package; statement coverage was 89.5%.
- Ran `go test -race ./...` successfully: 82 tests passed in one package.
- Used the repository-local `GOCACHE=go-build` and `GOMODCACHE=.gomod-cache` directories. The commands are documented in the README test section.

## 2026-10-14 — Ignore-empty verification

- Re-ran `go build ./...`, `go test ./... -cover`, `go test -race ./...`, and `go vet ./...` after adding IgnoreEmpty; all passed (87 tests in one package).
- Statement coverage remained 89.5%. `GOCACHE` and `GOMODCACHE` were the repository-local `go-build` and `.gomod-cache` directories.

## 2026-10-14 — Report API removal verification

- Ran `go build ./...`, the full `go test ./... -cover` suite, `go test -race ./...`, and `go vet ./...`; all passed (86 tests in one package).
- Statement coverage is 89.4%. `GOCACHE` and `GOMODCACHE` were the repository-local `go-build` and `.gomod-cache` directories.

## 2026-10-14 — Independent dedup strategy redesign

- Replaced `ParameterPolicy` with `DedupStrategy`: `None`, `SelectedFieldsHash`, `RequireParameters`, and `ParameterShapeCoverage`. Exactly one strategy is selected per session; the zero value does not filter.
- HASH defaults to method, normalized URL, Content-Type, body-capture state, and body bytes; caller-specified header names are additive. AUTH is a separate authentication group applied before each strategy.
- PARAM tracks only completely parsed empty-parameter endpoint groups: the first committed request per authentication group, method, path, and media type is allowed, and later repeats are dropped. Parameter-bearing and incomplete requests pass without state.
- SMART deduplicates complete parameter location/name shapes, ignores values, treats an empty parameter set as a shape, and allows incomplete parsing without HASH fallback.
- Adapted session reservation accounting so strategies without persistent dedup state do not retain request entries after commit.
- PARAM duplicate tracking is endpoint-scoped and includes the authentication group. The first complete empty-parameter request is held as a reservation and recorded only after commit; release allows retry.
- Added PARAM-specific endpoint grouping, capacity, and TTL tests; renamed a stale parser-limit test that implied HASH fallback.
- Verification passed: build, full tests (93 tests), race detector, and vet. Statement coverage is 90.6%; Go caches used `go-build` and `.gomod-cache`.

## 2026-10-14 — Reference behavior comparison

Compared the current package with `winezer0/passive-scan-client-plus` at commit [`69e8da2`](https://github.com/winezer0/passive-scan-client-plus/commit/69e8da271e5e46862edb0b5b522ae3fa554df0c4), the repository linked by this project's README. The relevant reference code is [`HttpAndHttpsProxy.java`](https://github.com/winezer0/passive-scan-client-plus/blob/69e8da271e5e46862edb0b5b522ae3fa554df0c4/src/main/java/burp/HttpAndHttpsProxy.java) and [`Utils.java`](https://github.com/winezer0/passive-scan-client-plus/blob/69e8da271e5e46862edb0b5b522ae3fa554df0c4/src/main/java/burp/Utils.java).

| Mode | Reference snapshot behavior | Current package behavior and difference |
|---|---|---|
| HASH | Uses the request URL, appending an MD5 of the byte-array string for a non-null body. The proxy path captures a body for POST requests. AUTH optionally appends serialized auth parameters. | Uses SHA-256 over length-delimited defaults: method, normalized full URL, Content-Type, body-capture state, and body bytes; configured headers are additive. Authentication is a separate partition. The fingerprint format and selected fields intentionally differ. |
| PARAM | Returns early for every request whose Burp-parsed parameter list is empty, including the first occurrence. It runs before SMART and HASH, so enabled flags can compose. | Allows the first successfully committed complete empty-parameter request per authentication group/method/origin/path/media type, then drops repeats. Parameter-bearing and incomplete parses pass. The first-request behavior and strategy isolation intentionally differ. |
| SMART | Keys cumulative parameter-name coverage by URL without query, Burp content type, and optionally serialized auth parameters. Values are replaced by a sentinel; unseen names are merged and allowed, while a request whose names are already covered is dropped. The map flattens names without parameter-location identity. | Uses the normalized endpoint plus auth group, method, scheme/host/port/path, and media type. Coverage IDs include parameter location and name; values are ignored. Empty coverage is tracked as a shape. Incomplete parses pass without state. Endpoint partitioning and location-aware names are deliberate extensions. |
| AUTH | Optional extraction from parameter names and headers using the configured auth regex; extracted auth values become part of SMART/HASH keys. | Caller supplies an opaque `AuthenticationGroupID`; credentials are neither parsed nor retained. The group partitions HASH, PARAM, and SMART independently. |

The reference README describes the same broad concepts (exact HASH, empty-parameter filtering, SMART parameter dedup, and auth separation), but it does not define a stable wire-compatible fingerprint or a complete parser contract. This package therefore preserves the conceptual modes while following the current API decisions above, rather than claiming exact behavioral parity. Parser-format and malformed-input parity still requires a concrete Burp SDK fixture corpus; the reference delegates parsing to Burp's `IRequestInfo`/`IParameter` APIs and does not expose a comparable partial-parse state.
