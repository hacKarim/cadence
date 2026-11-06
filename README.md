# cadence

A scheduler and workflow engine for AI agents.

Cron runs a command at a time. `cadence` runs a *graph* of agent steps at a
time: steps declare what they depend on, pass results forward, fan out over
lists, run in parallel under a bound, and each one executes inside its own
OS-enforced jail.

```
cadence run research-funnel.yaml
running research-funnel (sandbox: seatbelt)
  screen               succeeded
  analyse              succeeded (38 items)
  challenge            succeeded (6 items)
  publish              succeeded

run 12 succeeded
```

## Why

Useful agent work is almost never one prompt. The shape that keeps recurring
is this one:

1. a script gathers data, with no model involved,
2. N agents analyse that data in parallel,
3. an adversarial agent tries to break each surviving conclusion, **without
   having seen the first agent's reasoning**,
4. a decision rule fixed in advance is applied to whatever survives.

Step 3 is the hard part, and it is the reason this project exists. Two agents
on the same model looking at the same data agreeing is weak evidence. A
sceptic is only worth running if it genuinely cannot see the conclusion it is
meant to attack. Schedulers that fire one prompt and capture stdout never have
to answer that question. `cadence` answers it with the filesystem.

## Isolation

Every step item gets its own directory tree, created fresh:

```
<run>/<step>/<item>/
    work/      the process working directory
    work/out/  where the step writes results
    meta/      stdout, stderr and metadata, owned by the engine
```

Three things follow from that:

- **A step reads only what it declared.** Downstream steps do not read upstream
  directories. They declare `inputs:`, which the engine materialises inside
  `work/` before the process starts. The sceptic's jail contains `report.md`
  and nothing else, so there is no analyst reasoning to find.
- **The environment is built, not inherited.** A child gets `PATH`, `HOME` and
  a short allowlist, plus whatever the step's `env:` names. A credential
  sitting in your shell does not silently reach every step.
- **The kernel enforces it.** On macOS through a generated `sandbox-exec`
  profile, on Linux through `bubblewrap` with a fresh mount namespace.

`meta/` is never inside `work/`, so a step cannot read the engine's record of
itself or of anything else.

### When it cannot be enforced

If neither mechanism is available, `cadence` falls back to a `confined`
backend that sets the working directory and the environment and nothing more.
That is a real loss, so it is never silent:

- `cadence doctor` names the active backend and says plainly what it does not do
- the terminal interface shows it on every run
- a workflow can set `require_sandbox: true` and will refuse to run rather than
  degrade

```
$ cadence doctor
sandbox backend: seatbelt
                 isolation is enforced by the kernel
claude CLI:      /usr/local/bin/claude
```

## Install

```bash
git clone https://github.com/hacKarim/cadence.git
cd cadence
go build -o cadence ./cmd/cadence
```

Requires Go 1.24+ and the [Claude CLI](https://github.com/anthropics/claude-code)
on your `PATH` for agent steps.

## A workflow

```yaml
name: research-funnel
schedule: "0 0 7 * * 1-5"      # six fields: second minute hour day month weekday
timezone: Europe/Paris
concurrency: skip
require_sandbox: true

defaults:
  timeout: 20m
  retries: 1
  backoff: { base: 5s, factor: 2, max: 2m }

notify:
  on: [failure]
  slack: ${SLACK_WEBHOOK}

steps:
  - id: screen
    run: ./screen.py --out out/candidates.json
    outputs:
      candidates: { from: file, path: candidates.json, as: json }

  - id: analyse
    needs: [screen]
    for_each: '{{ .steps.screen.outputs.candidates }}'
    max_parallel: 4
    cache: daily
    inputs:
      figures.json: '{{ .item | json }}'
    agent:
      tools: [Read, Write, WebSearch]
      prompt: |
        Read figures.json and analyse {{ .item.ticker }}.
        End your report after a line containing =====REPORT=====
      extract: { after: "=====REPORT=====" }
    outputs:
      verdict: { from: stdout, as: raw, regex: '"verdict":"([a-z-]+)"' }

  - id: challenge
    needs: [analyse]
    for_each: '{{ .steps.analyse.items }}'
    when_item: '{{ eq .item.outputs.verdict "under-valued" }}'
    inputs:
      report.md: '{{ .item.result }}'
    agent:
      tools: [Read]
      prompt: |
        You are the sceptic. Read report.md. Your only job is to find out
        whether its case is wrong. Do not try to be balanced.
```

More in [`examples/`](examples/). They are covered by the test suite, so they
cannot drift away from what the engine actually implements.

## The spec language

A step has exactly one of `run:` (a shell command) or `agent:` (a `claude`
invocation).

| Key | Meaning |
|---|---|
| `needs` | steps that must finish first; also bounds what the step may reference |
| `for_each` | a list to fan out over; each element becomes `.item` |
| `when_item` | run this item only when the condition holds |
| `if` | run this step only when the condition holds |
| `max_parallel` | items in flight for this step |
| `inputs` | files staged into `work/` before the step starts |
| `outputs` | values lifted out of what the step produced |
| `env` | environment entries, expanded from `${VAR}` at load time |
| `allow` | paths outside the jail this step may read and write |
| `cache` | `never` (default), `daily`, or `always` |
| `isolate` | give the step a clean working directory |
| `timeout`, `retries`, `backoff` | applied per item, not per step |
| `on_item_failure` | `continue` (default) or `fail_fast` |

Templates are Go `text/template` with a small fixed helper set: `json`, `trim`,
`lines`, `indent`, `default`, `file`. Nothing in it can read the filesystem or
shell out.

**References are checked before anything runs.** The validator walks each
template and rejects a workflow where a step reads `.steps.<id>` that is not in
its `needs` closure. Without that check, `needs` would be decoration and the
isolation guarantee would rest on convention:

```
$ cadence validate funnel.yaml
step "challenge": agent.prompt: references step "screen", which is not in the
needs closure of "challenge"; add it to needs
```

Outputs are declared, never guessed:

```yaml
outputs:
  candidates: { from: file, path: candidates.json, as: json }
  verdict:    { from: result, as: json, select: "$.verdict" }
  build_id:   { from: stdout, as: raw, regex: 'id=(\d+)' }
```

To hand a downstream step a file rather than its contents, use the `file`
helper with `copy:`:

```yaml
inputs:
  data.json: { copy: '{{ file .steps.screen "candidates.json" }}' }
```

## Running

```
cadence                          the interactive interface
cadence run <workflow.yaml>      run now
cadence run <wf> --resume <id>   resume a failed run
cadence validate <workflow.yaml> parse and check without running
cadence runs [workflow]          recent runs
cadence logs <run-id> [step]     what a run produced
cadence daemon [--dir DIR]       background scheduler
cadence doctor                   sandbox backend and CLI status
cadence cache prune
```

### Caching and resume

A step item's cache key hashes the resolved step, the staged inputs and the
workflow file. `cache: daily` folds the date in, which matches the usual
pattern of reusing today's work and redoing it tomorrow. Environment values are
excluded, so rotating a credential does not invalidate everything.

`--resume` creates a *new* run that inherits the earlier one's successful
steps, leaving the failed run on record exactly as it happened. If the workflow
file changed since then it refuses, rather than quietly mixing results from two
different definitions.

### Scheduling

`concurrency` decides what a trigger does while a previous run is still going:
`skip` (the default, and it records a skipped run so the gap is visible),
`queue`, or `allow`. The lock lives in the database, so a manual `cadence run`
and the daemon cannot start the same workflow twice.

Schedules missed while the daemon was down are dropped by default. A week of
downtime should not launch 120 agent runs. `catchup: true` fires once to cover
the gap, never once per missed slot.

## Permissions

Agent steps run without `--dangerously-skip-permissions`. A step that needs to
write says so:

```yaml
agent:
  tools: [Read, Write, Bash]
  elevated_permissions: true
```

An unattended scheduler that silently grants agents full write access is a
footgun, so the default stays restrictive even though it means some workflows
have to ask.

## Known limits

- **Agent credentials must be reachable from inside the jail.** On macOS the
  Claude CLI keeps its credentials in the keychain, which a `sandbox-exec` jail
  cannot reach. Either put an API key in the step's `env:`, or set
  `CADENCE_SANDBOX=confined` to accept the weaker backend. `doctor` and the
  interface both report when that override is in effect.
- **Agent steps are granted their CLI's own configuration and temp
  directories.** They have to be, or the process cannot start. This is a
  documented widening: an agent step with file tools can reach its own CLI
  state. It still cannot reach another step's jail.
- `cadence` is single-machine and single-user. No remote runners, no multi
  tenancy.

## Development

```bash
go test ./...                          # unit tests, no network, no model calls
go test -tags integration ./...        # also exercises the real sandbox
```

The engine depends only on a `Runner` interface, so the whole execution model
is tested against a fake with scripted latency and failures. The isolation
guarantee has tests at both levels: one asserts a staged jail contains exactly
the declared files, and a tagged integration test asserts the kernel actually
refuses a write outside the jail and a read of a sibling step's output.

## License

MIT. See [LICENSE](LICENSE).
