# Contributing

Thanks for looking. A few things worth knowing before you open a pull request.

## The one rule

Every change has a test that failed before it and passes after it. The
isolation guarantees are the reason this project exists, so a change that
touches `internal/sandbox`, `internal/jail` or `internal/spec` without a test
will not be merged, however obviously correct it looks.

## Running the suite

```
go build ./...
go test ./...
gofmt -l .
go vet ./...
```

The bubblewrap tests only run where `bwrap` exists, which on a Mac is nowhere.
CI runs them on Linux:

```
go test -tags integration ./internal/sandbox/ -v
```

If you are on macOS and your change touches the Linux backend, say so in the
pull request and let CI be the judge.

## What is likely to be accepted

- A failing case in the spec validator, with the workflow that triggers it.
- A sandbox escape, with the test that demonstrates it. Please read
  [SECURITY.md](SECURITY.md) first if it is exploitable.
- Anything in the roadmap section of the README.

## What is unlikely to be accepted

- New keys in the spec language. The language is deliberately small, and
  "the YAML became a programming language" is an explicit anti-goal.
- A dependency, unless it replaces more code than it adds.
- Weakening a sandbox profile to make something work. Grant the specific path
  through `allow:` instead.

## Style

`gofmt` decides formatting. Comments explain why, not what. Commit messages
are imperative mood, and the body says what breaks if the change is wrong.
