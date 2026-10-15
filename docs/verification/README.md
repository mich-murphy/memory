# Bounded operator verification fixtures

`linux-native-2026-10-15.patch` reproduces test-only fixture `4664334` from source
`a95976a`. It is evidence/reproduction material, not a shipped or supported Linux
sandbox feature. It contains no actual CLI distributions, user data or credentials.

In a separate worktree at that base, apply its zero-context hunks with:

```sh
git apply --unidiff-zero /path/to/linux-native-2026-10-15.patch
```

Read the resulting `private/linux-native/README.md` before supplying public assets
or executing anything. Never run the real agents outside the required outer OS
boundary. The frozen fixture retains its **pre-execution UNVERIFIED** label;
actual executed results, failure history and scope are recorded separately in
[production-readiness.md](../production-readiness.md). No full production gate was
substituted by this operator fixture.
