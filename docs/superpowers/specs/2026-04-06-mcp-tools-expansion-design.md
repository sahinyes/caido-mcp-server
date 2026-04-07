# MCP Tools Expansion - Design Spec

**Date:** 2026-04-06
**Goal:** Add 10-12 new MCP tools that complete real testing workflows. Move from "traffic viewer" to "testing assistant."
**Constraint:** Stay under ~32 total tools. Every tool must answer "what does the user ask the AI to do that it currently can't?"

---

## Phase 1 - New MCP Tools Using Existing SDK (v0.2.2)

No changes to `caido-community/sdk-go`. Pure MCP server additions.

### Tool 1: `caido_list_intercept_entries`

**Purpose:** View requests sitting in the intercept queue.

- Input: `limit` (default 20, max 100), `after` (cursor), `filter` (HTTPQL)
- Output: list of `{id, method, host, path, isTls, statusCode, createdAt}`, pagination info
- SDK method: `client.Intercept.ListEntries(ctx, opts)`
- Pattern: same as `list_requests.go`

### Tool 2: `caido_forward_intercept`

**Purpose:** Forward an intercepted message, optionally with a modified raw request.

- Input: `id` (required), `raw` (optional base64-encoded modified request)
- Output: `{forwardedId}`
- SDK method: `client.Intercept.Forward(ctx, id, input)`
- If `raw` is empty, forward as-is (pass nil input)
- If `raw` is provided, wrap in `ForwardInterceptMessageInput{Raw: raw}`

### Tool 3: `caido_drop_intercept`

**Purpose:** Drop an intercepted message (don't forward it).

- Input: `id` (required)
- Output: `{droppedId}`
- SDK method: `client.Intercept.Drop(ctx, id)`

### Tool 4: `caido_automate_task_control`

**Purpose:** Start, pause, resume, or cancel a fuzzing task.

- Input: `action` (required: start/pause/resume/cancel), `session_id` (required for start), `task_id` (required for pause/resume/cancel)
- Output: `{action, taskId}`
- SDK methods:
  - start: `client.Automate.StartTask(ctx, sessionID)` - returns task ID
  - pause: `client.Automate.PauseTask(ctx, taskID)`
  - resume: `client.Automate.ResumeTask(ctx, taskID)`
  - cancel: `client.Automate.CancelTask(ctx, taskID)`

### Tool 5: `caido_list_environments`

**Purpose:** List environments and their variables (tokens, session IDs, etc.).

- Input: none
- Output: list of `{id, name, variables: [{name, value, kind}]}`, plus `{globalId, selectedId}` from context
- SDK methods: `client.Environment.List(ctx)` + `client.Environment.GetContext(ctx)`
- Combine both calls to give full picture in one tool call

### Tool 6: `caido_select_environment`

**Purpose:** Switch the active environment (controls which variables are used in replay placeholders).

- Input: `id` (required, pass empty string to deselect)
- Output: `{id, name}` of selected environment (or null if deselected)
- SDK method: `client.Environment.Select(ctx, id)`

### Tool 7: `caido_delete_findings`

**Purpose:** Delete findings (clean up false positives).

- Input: `ids` (optional list of finding IDs) OR `reporter` (optional reporter name). One must be provided (`@oneOf`).
- Output: `{deletedIds}` - list of deleted finding IDs
- SDK method: `client.Findings.Delete(ctx, input)`
- GraphQL input: `DeleteFindingsInput @oneOf { ids: [ID!], reporter: String }`

### Tool 8: `caido_export_findings`

**Purpose:** Export findings for reporting.

- Input: `ids` (optional list of finding IDs) OR `reporter` (optional filter by reporter). One must be provided (`@oneOf`).
- Output: `{exportId}` - the export ID for download
- SDK method: `client.Findings.Export(ctx, input)`
- GraphQL input: `ExportFindingsInput @oneOf { filter: FilterClauseFindingInput, ids: [ID!] }`

---

## Phase 2 - SDK Additions Then MCP Tools

These operations are in the Caido GraphQL schema but NOT in `caido-community/sdk-go` v0.2.2. Need to add them to the SDK first, release v0.3.0, then bump the dependency.

### SDK Addition 1: Tamper Rules (Match & Replace)

**GraphQL operations to add in `graphql/operations/tamper.graphql`:**

```graphql
query ListTamperRuleCollections
query GetTamperRule($id: ID!)
mutation CreateTamperRule($input: CreateTamperRuleInput!)
mutation UpdateTamperRule($id: ID!, $input: UpdateTamperRuleInput!)
mutation DeleteTamperRule($id: ID!)
mutation ToggleTamperRule($id: ID!, $enabled: Boolean!)
mutation CreateTamperRuleCollection($input: CreateTamperRuleCollectionInput!)
```

**Go SDK wrapper: `tamper.go`**

```go
type TamperSDK struct { client *Client }

func (s *TamperSDK) ListCollections(ctx) // returns all collections with rules
func (s *TamperSDK) GetRule(ctx, id)
func (s *TamperSDK) CreateRule(ctx, input)
func (s *TamperSDK) UpdateRule(ctx, id, input)
func (s *TamperSDK) DeleteRule(ctx, id)
func (s *TamperSDK) ToggleRule(ctx, id, enabled)
func (s *TamperSDK) CreateCollection(ctx, input)
```

**MCP Tool: `caido_list_tamper_rules`**

- Input: none
- Output: list of collections, each with rules: `{id, name, rules: [{id, name, enabled, condition, section, sources}]}`
- Flatten the section union into a human-readable string (e.g., "request_header: add Authorization")

**MCP Tool: `caido_create_tamper_rule`**

- Input: `collection_id` (required), `name` (required), `section` (required - one of the section types), `match` (string/regex to match), `replace` (replacement string), `condition` (optional HTTPQL filter), `sources` (optional, default all)
- Output: `{id, name, enabled}`
- The tool handler must translate the simplified input into the nested GraphQL input types:
  - `section: "request_header"` + `operation: "add"` + `header_name: "Authorization"` + `replace: "Bearer xyz"` maps to `TamperSectionInput{requestHeader: {operation: {add: {matcher: {name: "Authorization"}, replacer: {term: "Bearer xyz"}}}}}`
- Supported section shortcuts:
  - `request_header_add` - add/set a header
  - `request_header_update` - update existing header value
  - `request_header_remove` - remove a header
  - `request_body` - match/replace in request body
  - `request_path` - match/replace in request path
  - `request_query` - match/replace in query string
  - `response_body` - match/replace in response body
  - `response_header_add` / `response_header_update` / `response_header_remove`
  - `response_status_code` - change response status code

**MCP Tool: `caido_toggle_tamper_rule`**

- Input: `id` (required), `enabled` (required boolean)
- Output: `{id, enabled}`

**MCP Tool: `caido_delete_tamper_rule`**

- Input: `id` (required)
- Output: `{deletedId}`

### SDK Addition 2: Workflow Execution

**GraphQL operations to add in `graphql/operations/workflow.graphql` (append):**

```graphql
mutation RunActiveWorkflow($id: ID!, $input: RunActiveWorkflowInput!)
mutation RunConvertWorkflow($id: ID!, $input: Blob!)
mutation ToggleWorkflow($id: ID!, $enabled: Boolean!)
```

**Go SDK additions to `workflow.go`:**

```go
func (s *WorkflowSDK) RunActive(ctx, id, requestID)
func (s *WorkflowSDK) RunConvert(ctx, id, inputBlob)
func (s *WorkflowSDK) Toggle(ctx, id, enabled)
```

**MCP Tool: `caido_run_workflow`**

- Input: `id` (required), `request_id` (required for active workflows), `input` (required for convert workflows - the data to transform)
- Output: active workflow returns `{taskId}`, convert workflow returns `{output}` (the transformed data)
- Tool detects type from which optional params are provided

**MCP Tool: `caido_toggle_workflow`**

- Input: `id` (required), `enabled` (required boolean)
- Output: `{id, name, enabled}`

---

## Architecture Notes

### File Structure

All new tools follow the existing pattern: one file per tool in `internal/tools/`, with:
- Input struct with json + jsonschema tags
- Output struct
- Handler function returning `func(context.Context, *mcp.CallToolRequest, Input) (*mcp.CallToolResult, Output, error)`
- Register function called from `serve.go`

### Registration in serve.go

New registration calls grouped by section:

```go
// Intercept (expanded)
tools.RegisterInterceptStatusTool(server, client)
tools.RegisterInterceptControlTool(server, client)
tools.RegisterListInterceptEntriesTool(server, client)    // NEW
tools.RegisterForwardInterceptTool(server, client)        // NEW
tools.RegisterDropInterceptTool(server, client)           // NEW

// Automate (expanded)
tools.RegisterListAutomateSessionsTool(server, client)
tools.RegisterGetAutomateSessionTool(server, client)
tools.RegisterGetAutomateEntryTool(server, client)
tools.RegisterAutomateTaskControlTool(server, client)     // NEW

// Environments (new section)
tools.RegisterListEnvironmentsTool(server, client)        // NEW
tools.RegisterSelectEnvironmentTool(server, client)       // NEW

// Findings (expanded)
tools.RegisterListFindingsTool(server, client)
tools.RegisterCreateFindingTool(server, client)
tools.RegisterDeleteFindingsTool(server, client)          // NEW
tools.RegisterExportFindingsTool(server, client)          // NEW

// Tamper Rules (new section, Phase 2)
tools.RegisterListTamperRulesTool(server, client)         // NEW
tools.RegisterCreateTamperRuleTool(server, client)        // NEW
tools.RegisterToggleTamperRuleTool(server, client)        // NEW
tools.RegisterDeleteTamperRuleTool(server, client)        // NEW

// Workflows (expanded, Phase 2)
tools.RegisterListWorkflowsTool(server, client)
tools.RegisterRunWorkflowTool(server, client)             // NEW
tools.RegisterToggleWorkflowTool(server, client)          // NEW
```

### Tool Count

- Current: 20
- After Phase 1: 28 (+8)
- After Phase 2: 34 (+6)
- If 34 feels heavy, `caido_delete_findings` and `caido_export_findings` are the weakest candidates to cut

### SDK Changes (Phase 2)

- New files in sdk-go: `tamper.go`, `graphql/operations/tamper.graphql`
- Modified files: `workflow.go`, `graphql/operations/workflow.graphql`, `client.go` (add `Tamper TamperSDK` field)
- Run `make generate` to regenerate `graphql/generated.go` from updated operations + schema
- Tag as v0.3.0
- Bump dependency in MCP server go.mod

### What We're NOT Adding

- WebSocket streams - deferred until user demand is clear
- Plugin management - admin task, use the UI
- DNS rewrites / upstream proxies - admin task
- Backup/restore - admin task
- AI assistant passthrough - the MCP client IS the AI
- Request metadata updates - nice-to-have, not a testing workflow gap
- Data import - one-time setup task
- GraphQL subscriptions - MCP stdio doesn't support push well
