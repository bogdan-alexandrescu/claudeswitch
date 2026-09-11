# Contributing

## Running the tests

```sh
go test ./...
```

Both platforms matter. The credential store, the service install and the
notifier differ between macOS and Linux, and a cross-compile does not catch what
a real run does — two genuine bugs were found this way that `GOOS=linux go build`
had passed. If you are on macOS and have Docker:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.23 go test ./...
```

## The one rule worth stating

**Where reality is observable, observe it.** Do not infer from something written
down earlier when the fact itself can be checked.

Most of the bugs in this program's history came from breaking that rule: a
cached account name trusted over the credential itself, an organization treated
as a quota pool because its id happened to be in a response header, a burst rate
limit read as a sustained one and then defended for two days. `docs/GROUND_TRUTH.md`
records each of them, including what the wrong conclusion was and how long it
survived.

## Undocumented surfaces

This tool depends on endpoints and file formats that Anthropic has not
documented and may change: `/api/oauth/usage`, `/api/oauth/profile`,
`/v1/oauth/token`, the credential store layout, and the shape of Claude Code's
transcripts.

Each lives behind one adapter, with a contract test asserting the shape against
a recorded response (`internal/usage/testdata/`). If one fails after a Claude
Code upgrade, do not loosen the assertion — re-capture the fixture, work out what
moved, and decide whether the daemon can still do its job. That decision is what
the test exists to force.

## Things it must never do

- Put a token in argv, a log, or an error message.
- Write a credential without reading it back to check.
- Replace the live credential wholesale — `mcpOAuth` lives there too, and
  overwriting it signs the user out of every MCP server.
- Refresh the account currently in use without explicit consent; a refresh
  revokes the token the running session holds.
- Treat an account it could not read as available.
