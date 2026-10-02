# sdk-go

Community Go SDK for [Caido](https://caido.io) - the lightweight web security auditing toolkit.

> **This checkout is a FORK and carries local changes that upstream does not
> have.** It is vendored into `caido-mcp-server`; its own origin
> (`szybnev/sdk-go`) answers "Repository not found", so the vendored copy is the
> only published artifact and a rebase onto upstream silently drops the list
> below. All three are defects that upstream shares.
>
> 1. **`retry_transport.go` (new file).** Retries connection-level failures once
>    so the client self-heals after macOS sleep/wake instead of returning a
>    stale-connection error (FR fr-1777836025988). Since 2026-10-01 it does
>    **not** retry a GraphQL **mutation** once the bytes may have been
>    delivered - replaying `startReplayTask` or `deleteFindings` duplicates a
>    side effect, and "reset while reading the reply" is indistinguishable from
>    "delivered and executed". Dial failures keep their retry, because nothing
>    was sent, and that is also the case the retry exists for. Its idle-connection
>    purge runs **before** that mutation check (2026-10-02): the socket that just
>    failed is dead whether or not this request may replay its bytes, and leaving
>    it pooled hands it to the next caller.
> 2. **`createReplaySession` now selects its payload `error`**
>    (`graphql/operations/replay.graphql`, regenerated into
>    `graphql/generated.go`, surfaced by `createSessionPayloadError` in
>    `replay.go`). The payload's error is optional, so every refusal -
>    permission denied, a cloud restriction, an unknown collection id - used to
>    arrive as `err == nil` with a nil session and came back as
>    "create replay session returned no session", which reads as an SDK bug
>    rather than an answer from Caido.
> 3. **`GetReplaySession` reads `entries(last: 100)`, not `first: 100`**
>    (2026-10-02). Its only consumer is the MCP's pre-send baseline, which needs
>    the entries that existed just before a send. Measured against 0.58.3:
>    `first:N` returns the OLDEST N, `last:N` the NEWEST N - so with `first` the
>    baseline stopped covering anything recent once a session passed 100 entries,
>    and the guard built on it had quietly stopped guarding.

This SDK mirrors the API surface of the official [JavaScript SDK](https://github.com/caido/sdk-js) (`@caido/sdk-client`) and uses [genqlient](https://github.com/Khan/genqlient) for type-safe GraphQL code generation from the official Caido schema.

## Installation

```bash
go get github.com/caido-community/sdk-go
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"

    caido "github.com/caido-community/sdk-go"
)

func main() {
    ctx := context.Background()

    client, err := caido.NewClient(caido.Options{
        URL:  "http://localhost:8080",
        Auth: caido.PATAuth("your-pat-token"),
    })
    if err != nil {
        log.Fatal(err)
    }

    if err := client.Connect(ctx); err != nil {
        log.Fatal(err)
    }

    // List proxied requests
    first := 10
    resp, err := client.Requests.List(ctx, &caido.ListRequestsOptions{
        First: &first,
    })
    if err != nil {
        log.Fatal(err)
    }

    for _, edge := range resp.Requests.Edges {
        req := edge.Node
        status := 0
        if req.Response != nil {
            status = req.Response.StatusCode
        }
        fmt.Printf("%s %s%s -> %d\n", req.Method, req.Host, req.Path, status)
    }
}
```

## Authentication

The SDK supports Personal Access Tokens (PAT), which is the recommended method:

```go
client, err := caido.NewClient(caido.Options{
    URL:  "http://localhost:8080",
    Auth: caido.PATAuth("caido_xxxxx"),
})
```

You can also use access/refresh token pairs from the OAuth device flow:

```go
client, err := caido.NewClient(caido.Options{
    URL:  "http://localhost:8080",
    Auth: caido.TokenAuth(accessToken, refreshToken),
})
```

## Domain SDKs

The client exposes domain-specific SDKs matching the JS SDK:

| SDK | Description |
|-----|-------------|
| `client.Requests` | Proxied HTTP requests (list, get, metadata) |
| `client.Intercept` | MITM intercept entries and message queue |
| `client.Replay` | Replay sessions, entries, and send requests |
| `client.Findings` | Security findings attached to requests |
| `client.Scopes` | Target scope management |
| `client.Projects` | Project management |
| `client.Environments` | Variable environments |
| `client.HostedFiles` | Files served by Caido |
| `client.Workflows` | Automation workflows |
| `client.Tasks` | Background task management |
| `client.Instance` | Runtime info and settings |
| `client.Filters` | Saved HTTPQL filter presets |
| `client.Users` | Current user info |
| `client.Plugins` | Installed plugin packages |
| `client.Automate` | Fuzzing sessions (Automate) |
| `client.Sitemap` | Site structure tree |

## Readiness Polling

Wait for the Caido instance to be ready before making requests:

```go
err := client.ConnectWithOptions(ctx, caido.ConnectOptions{
    WaitForReady:  true,
    ReadyTimeout:  60 * time.Second,
    ReadyInterval: 2 * time.Second,
})
```

## Low-Level GraphQL Access

For operations not yet covered by domain SDKs, use the GraphQL client directly:

```go
import gen "github.com/caido-community/sdk-go/graphql"

resp, err := gen.ListScopes(ctx, client.GraphQL)
```

## Schema Updates

The GraphQL schema is vendored from [`@caido/schema-proxy`](https://www.npmjs.com/package/@caido/schema-proxy) on npm.

```bash
make schema    # Pull latest schema
make generate  # Regenerate Go code
make build     # Verify compilation
```

## Development

```bash
git clone https://github.com/caido-community/sdk-go.git
cd sdk-go
make check     # generate + build + vet
make test      # run tests
```

## License

MIT - see [LICENSE](LICENSE).

## Acknowledgments

- [Caido](https://caido.io) team for the platform and schema
- Built with [genqlient](https://github.com/Khan/genqlient) for type-safe GraphQL
