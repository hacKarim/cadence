# Security policy

## What this project claims

A step cannot read another step's jail, and cannot write outside its own. On
macOS that is enforced by a generated `sandbox-exec` profile, on Linux by
`bubblewrap`. If you can break either, that is a real finding and I want to
hear about it.

Two things are already documented as deliberate widenings, so they are not
findings on their own. Both are in the Known limits section of the README:

- Agent steps are granted their CLI's own configuration and temp directories.
- Agent steps share the CLI's per-user temp path with each other.

Running with `CADENCE_SANDBOX=confined` disables enforcement on purpose. The
tool says so when it does, and `require_sandbox: true` refuses to run that way.

## Reporting

Open a private security advisory through the Security tab rather than a public
issue, and include the workflow YAML that triggers it. If it is a sandbox
escape, a failing test against `internal/sandbox` is the fastest route to a fix.

I will acknowledge within a week. This is a personal project with no paid
support behind it, so please do not expect a same-day turnaround.

## Supported versions

The tip of `main`. There are no backports.
