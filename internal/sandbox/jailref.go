package sandbox

import "github.com/hacKarim/cadence/internal/jail"

// jailRef is an alias so the Sandbox interface reads without importing jail
// at every call site.
type jailRef = jail.Jail
