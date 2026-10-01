# Installed Package Verification

Run `bun scripts/verify-package.ts --tarball /absolute/path/to/candidate.tgz`
against the single reviewed candidate. Supplying `--tarball` never repacks it;
the verifier checks its SHA-256 again after verification. Without that argument,
the existing convenience mode packs the current build outputs.

Four disposable parent projects (Bun and npm, each present/absent) declare the exact tarball in
`optionalDependencies`. All install with `--ignore-scripts`; the absent case
also uses `--omit optional`. Runtime auto-install is disabled with `--no-install`.
The present project checks installed file types/modes and public declaration
types before running the copied consumer fixture. The absent project exercises
the parent's direct local HTTP fallback.
Node imports the installed ESM without side effects and creation returns
`UNSUPPORTED_RUNTIME`. npm uses disposable configuration/cache without credentials.

Only preparation uses checkout tools: the installed TypeScript compiler and Bun
types validate the consumer, and Go compiles
`helper/internal/testhelper/packagefixture` using the checkout module and normal
Go build cache. That test-only executable is copied beside the consumer, not
inside the installed package. Runtime subprocesses use only installed exports,
the copied fixture/binary, isolated HOME/TMPDIR, and system PATH. They do not
import source-tree modules or inherit ambient credentials/preload settings.
On macOS, the runtime consumer and its helpers additionally run under
`sandbox-exec` with checkout file reads denied. Linux checks isolated package
resolution without that OS-level filesystem sandbox.

The fixture checks import-time process/browser startup and filesystem effects,
unchanged global fetch, synthetic browser callback notification and persistent
authorized reuse, local HTTP proxy POST forwarding without proxy credential
leakage, repeated close, refused connections after close, and helper reaping and
ephemeral-state removal after cancellation, timeout, and callback/auth failures.
Synthetic authorization is just a private marker file, not a Tailscale identity;
the callback validates a synthetic URL and never opens a browser or fetches it.

Production native coverage remains separate: an invalid protocol version must
return only the sanitized protocol error. Public `createBridge` with no
`helperPath` must run the installed default helper and return `STATE_UNSAFE` for
a private state directory containing a deliberately unsafe `0644` file. State
validation precedes node construction, so neither check can enroll a real node.
Proxy traffic is loopback-only through the synthetic Go node. This does not
claim real control-plane, browser launch, or tailnet connectivity coverage.
