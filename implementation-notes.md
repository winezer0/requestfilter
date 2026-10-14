# Implementation Notes

## 2026-10-14 — Initial requestfilter module

- Created the standalone module `github.com/winezer0/requestfilter` with a single public root package. The package uses only the Go standard library and does not import Tanscan or Burp types.
- The current API selects one independent strategy: no filtering, selected-field HASH, parameter-required PARAM, or parameter-shape SMART. HASH is no longer an always-on baseline.
- Request fingerprints use length-prefixed SHA-256 fields. URL canonicalization lowercases host/scheme, removes default ports and fragments, and preserves path escapes, trailing slashes, query ordering, and repeated query values.
- Initially, authentication partitioning used only caller-supplied non-secret `AuthenticationGroupID`; later notes record the built-in header sources added to the current API. Raw credentials and request parameter values are not retained in filter state or returned in decisions.
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
| HASH | Uses the raw request URL, appending an MD5 of the body byte-array string for a non-null POST body; optional AUTH data is appended when AUTH is enabled. | Uses SHA-256 over length-delimited defaults: method, normalized full URL, Content-Type, body-capture state, and body bytes; configured headers are additive. Authentication is a separate partition. Fingerprint fields, encoding, and strategy composition intentionally differ. |
| PARAM | When enabled, returns early for every request whose Burp-parsed parameter list is empty, including the first; it runs before SMART/HASH, so flags can compose. | Independent strategy. Parameterized and incomplete requests pass. A complete empty-parameter request is allowed once per authentication group/method/origin/path/media type after commit, then repeats drop. First-request behavior and strategy isolation intentionally differ. |
| SMART | Uses URL without query, Burp content type, and optional auth data as the group key. It replaces values with a marker and cumulatively merges unseen parameter names. Names flatten across locations. The key omits HTTP method. | Uses the normalized endpoint plus auth group, method, scheme/host/port/path, and media type. Coverage IDs include parameter location and name; values are ignored. Empty coverage is tracked as a shape. Method partitioning and location-aware names intentionally differ. |
| AUTH | Optionally extracts parameter/header names matching `token|auth|sessid|session` case-insensitively. Since `Proxy-Authorization` contains `auth`, the reference matcher includes it. | Cookie, Authorization, and common token/auth/session headers are detected by default; custom names and caller `AuthenticationGroupID` can be added. `Proxy-Authorization` is excluded. Header identities are HMACed with a session key. |

The reference README describes the same broad concepts (exact HASH, empty-parameter filtering, SMART parameter dedup, and auth separation), but it does not define a stable wire-compatible fingerprint or a complete parser contract. This package therefore preserves the conceptual modes while following the current API decisions above, rather than claiming exact behavioral parity. A source-derived regression corpus now covers representative request formats, but runtime parser parity remains unverified: the reference delegates parsing to Burp's `IRequestInfo`/`IParameter` APIs and has no checked-in test harness or partial-parse contract.

### Source-level audit scope

- `testdata/reference-corpus.json` now carries a machine-readable rule for each source mode (HASH, PARAM, SMART, AUTH), alongside case-level expected outcomes and the pinned source locations. `TestReferenceBehaviorCorpus` checks that every rule is present and complete.
- Added a cross-method SMART corpus case. The source's endpoint key omits method, while this package intentionally includes it; the current package test therefore expects the second request to pass. The source outcome is conditional on Burp exposing equivalent parameter names/content types.
- PARAM's existing regression explicitly documents the intentional first-empty-request difference: the package allows the first committed complete empty request and drops later duplicates; the reference returns early and drops it immediately.
- These are source-derived comparisons only. No Burp process or reference parser was available, so parser outputs and conditional outcomes are not presented as observed runtime data.

## 2026-10-14 — Request edge-case regression coverage

- Added parser and SMART regressions for repeated, blank, flag-style, percent-encoded, case-distinct, and UTF-8 parameter names; malformed query/form escapes return `partial` without trusting partial names.
- Added JSON cases for top-level scalars, arrays with object/scalar/empty values, duplicate object keys, case-sensitive names, UTF-8 names, and vendor `+json` media types. Object-key duplicates collapse to one path; array positions remain part of the shape.
- Added Content-Type parameter and multipart-boundary grouping cases, repeated multipart fields/files, extended `filename*`, missing/unknown media types, and non-UTF-8-declared XML fallback.
- Added exact-boundary parser tests for body bytes, parameter count, JSON depth, XML depth/nodes, and multipart part count, alongside one-over cases already present. Added repeated Cookie-line normalization, duplicate cookie names, Basic credentials with padding, and ordered multi-value Authorization/token-header identity coverage.
- Tightened multipart parsing so a captured empty body without a final delimiter is partial, while a valid closing-boundary-only body is a complete empty shape. Added regressions to distinguish the two.
- Documented that the API receives parsed `Request` fields, not raw wire messages. `net/http` framing, HTTP/2 pseudo-header mapping, authority-form CONNECT targets, and asterisk-form OPTIONS targets must be handled by the integration layer; raw CONNECT/OPTIONS targets are rejected as invalid URLs.
- This coverage verifies this package's defined behavior. It does not turn source-derived Burp assumptions into runtime reference results.
- Verification passed: `go build ./...`, the full test suite (180 tests), `go test -race ./...`, `go vet ./...`, and `git diff --check`. Statement coverage is 92.0%. The temporary coverage profile was removed after measurement.

## 2026-10-14 — Fuzzing, net/http adapter checks, and performance baseline

- Added native fuzz targets for URL normalization and body parameter extraction with seeds for query/form escapes, JSON structure/duplicates, XML validity, multipart boundaries, and unknown media types. The extraction target checks deterministic sorted unique results and valid complete/partial states.
- Added an internal, non-public adapter example corresponding to the README pattern. Tests cover relative HTTP/HTTPS URL mapping, absolute proxy URLs, exact and over-limit bodies, body restoration after success/read failure/overflow, nil bodies, cloned headers, and missing host rejection.
- Added benchmarks for 100-field JSON parsing, 64-part multipart parsing, repeated covered SMART reservation, and 64-request HASH batches (duplicate and unique input).
- Five-second fuzz smoke runs passed on Go 1.26.4 Windows/amd64: parameter parsing executed 122,317 cases; URL normalization executed 257,185 cases. No crashes or nondeterministic results were found.
- Initial benchmark baseline (`-benchtime=100ms -count=3`) on AMD Ryzen AI MAX+ 395: JSON parse median 127,514 ns/op (170,554 B, 2,647 allocs); multipart parse 133,724 ns/op (174,356 B, 2,213 allocs); covered SMART reserve 3,960 ns/op (2,944 B, 68 allocs); HASH batch duplicate 206,820 ns/op (156,257 B, 3,213 allocs); HASH batch unique 273,931 ns/op (172,617 B, 3,285 allocs). Timing is an initial machine-specific reference; allocation counts were stable across the three samples.
- Final verification passed: build, full suite (213 tests across two packages), race detector, vet, and diff check. Coverage: root package 92.6%, internal adapter example 96.4%. The temporary coverage profile was removed after measurement.

## 2026-10-14 — Built-in authentication identity sources

- Every active dedup strategy now partitions by a session-keyed HMAC identity derived from Cookie, Authorization, and header names containing `token`, `auth`, `sessid`, or `session`. Cookie pairs are sorted so header pair order does not split a group.
- `Proxy-Authorization` is excluded from automatic and additional-header selection because it identifies proxy credentials rather than the target site's authentication. `AdditionalAuthenticationHeaderNames` supports application-specific headers such as `X-API-Key`.
- `AuthenticationGroupID` is combined as an additional caller-defined group dimension. The HMAC output and original authentication values are not exposed in decisions or persisted as raw session state.
- Verification passed: `go build ./...`, `go test ./... -cover`, `go test -race ./...`, and `go vet ./...`. All 103 tests passed; statement coverage is 91.3% (617/676). Go caches used `go-build` and `.gomod-cache`.

## 2026-10-14 — Package-level production checks

- Kept validation scoped to this standalone library: no deployment checklist or CI workflow was added.
- Added a real `net/http` chunked-request regression proving that the adapter captures the decoded body and leaves the identical bytes available to the caller afterward.
- Added a concurrent unique-request capacity regression: with 64 configured entries and 128 requests, exactly 64 are allowed and the remainder receive `ActionReject` with `ReasonCapacity`.
- Verified on the declared minimum Go 1.24.0 and Go 1.26.4: `go test ./...` passed (215 tests across two packages). Also passed `go test ./... -cover` (root 92.6%, adapter 96.4%), `go test -race ./...`, `go vet ./...`, and 5-second fuzz runs for `FuzzExtractParameters` and `FuzzNormalizeURL`.
- Local command environment used `GOCACHE=go-build` and `GOMODCACHE=.gomod-cache`. The library remains an in-process, per-session filter; callers configure session lifetime and handle capacity rejection and retry decisions.

## 2026-10-14 — Consumer flow smoke test and release candidate

- Reviewed the public `Request`, `Options`, `Decision`, `Session`, and `Reservation` flow against the README: callers map a complete request, reserve it, forward only `ActionAllow`, commit after downstream acceptance, release after downstream failure, and handle `ActionRetry`/`ActionReject` separately.
- Added an end-to-end `net/http` smoke test with synthetic JSON requests. The first request for a Cookie identity is forwarded and committed, its same-identity duplicate is dropped, and the same request under another Cookie identity is forwarded. The test also verifies that bytes read by the downstream stand-in exactly match the received body.
- No consuming application or redacted business traffic was available. The smoke test validates package integration semantics, not behavior against a specific production caller.
- Prepared the first pre-1.0 release as `v0.1.0` in `CHANGELOG.md`. The repository has an MIT license, declares Go 1.24.0, and has no third-party module dependencies. Public API compatibility is not promised before v1.0.0.
- Verification passed on Go 1.24.0 and Go 1.26.4: all 219 tests across two packages. On Go 1.26.4, race, vet, build, and diff checks passed. Coverage is root 92.6% and adapter 96.4%.

## 2026-10-14 — Source-derived reference corpus and authentication matrix

- Added `testdata/reference-corpus.json`, pinned to reference commit `69e8da271e5e46862edb0b5b522ae3fa554df0c4`. Eight requests cover query, URL-encoded form, nested JSON, XML, multipart, cross-location name collisions, malformed JSON, and unknown binary bodies.
- Each case records the current package's expected parser result and SMART action alongside the source-derived reference expectation. Conditional reference outcomes state the Burp parameter assumptions; cases whose parser result cannot be inferred are explicitly marked undetermined.
- The reference checkout has no tests and its Maven project does not provide a usable implementation of Burp's `IExtensionHelpers`. The corpus does not claim runtime output from Burp; obtaining that oracle requires executing the extension inside Burp or adding a supported Burp test harness.
- Added a 3-strategy by 5-source authentication matrix, caller-ID composition checks, and per-strategy `Proxy-Authorization` exclusion checks. The keyword-header matrix passes mixed-case names through the raw `http.Header` map.
- Verification passed after the source-level audit update: `go build ./...`, `go test -coverprofile coverage ./...`, `go test -race ./...`, `go vet ./...`, and `git diff --check`. All 137 tests passed; statement coverage is 91.4%. The coverage profile was a temporary local artifact. Go caches used `go-build` and `.gomod-cache`.
