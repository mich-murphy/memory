# Naming

A name is as short as its scope allows and no shorter: it carries what a reader can't see from where they are, and nothing they can. The rules below come from [Effective Go](https://go.dev/doc/effective_go#names), [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) (CRC) and the [Google Go style guide](https://google.github.io/styleguide/go/decisions#naming) (Google). Where those sources differ, the rule says which one this repo follows. Rules marked **repo** are this project's own.

Lint checks only error names, receiver names and duration units. Everything else here is checked in review, so check a change against this page before sending it.

## Length follows scope

- The further a name is used from where it's declared, the more it has to say ([CRC](https://go.dev/wiki/CodeReviewComments#variable-names)). A loop variable or a value used in the next few lines gets one or two letters; a package-level name gets a full word or two.
- Leave out what the reader already has: the package, the receiver, the type, the enclosing function ([Google](https://google.github.io/styleguide/go/decisions#repetitive-with-package)). `state.File`, not `state.StateFile`; `Config.Path`, not `Config.ConfigPath`; `count` inside `UserCount`.
- Leave out the type: `files`, not `fileList` or `fileMap` ([Google](https://google.github.io/styleguide/go/decisions#variable-names)). Add a type word only when two forms of one value meet, as in `argv` and `argvJSON`.
- Don't drop letters to save typing (`Sandbox`, not `Sbx`). Abbreviations everyone reads the same way are fine: `cfg`, `ctx`, `db`, `err`, `sha` ([Google](https://google.github.io/styleguide/go/decisions#variable-names)).
- Name a value for what it holds, not where it came from ([Google](https://google.github.io/styleguide/go/decisions#variable-names)).

## Short names this repo uses

**repo.** Outside the package that defines the type, in a scope longer than a few lines, these names always mean the same thing and nothing else takes them. One concept has one name, and one name one concept ([Google, consistency](https://google.github.io/styleguide/go/guide#consistency)).

| Name | Holds |
|---|---|
| `ctx`, `err`, `cfg` | `context.Context`, `error`, `*config.Config` |
| `a` | `adapters.Adapter` |
| `sf` | `adapters.SessionFile` |
| `ref` | `adapters.SessionRef` |
| `sess` | `*state.Session` |
| `db` | a database handle: `*state.State`, or `*sql.DB` where SQLite is opened directly |
| `bucket` | `store.Store`, R2 or a local directory |
| `st` | `os.FileInfo` from a stat |
| `sha` | a hex SHA-256 |
| `now` | the current time as Unix seconds (`float64`); a clock function is `clock` |

## Packages

- Lower case, one word, no underscores or mixedCaps; the name is the last element of the import path ([Effective Go](https://go.dev/doc/effective_go#package-names)).
- Never `util`, `common`, `misc`, `helper`, `types` or `api` alone. A domain prefix makes such a word acceptable (`fsutil`), as [Google allows](https://google.github.io/styleguide/go/best-practices#util-packages).
- Exported names don't repeat the package: `bufio.Reader`, not `bufio.BufReader` ([Effective Go](https://go.dev/doc/effective_go#package-names)). A package whose main type shares its name constructs it with `New` or `Open` (`state.Open` returns a `*state.State`).

## Exported or not

**repo.** Export a name only when another package uses it. Tests live inside their package, so they reach unexported names. This keeps each package's API to what the rest of the collector calls ([Google: keep interfaces unexported if only used internally](https://google.github.io/styleguide/go/decisions#interfaces); the same reasoning applies to every name). Struct fields that encoding/json needs are exported however they're used.

## Functions and methods

- A function that does something is a verb (`Compress`, `RecordUpload`); one that returns something is a noun (`Pieces`, `Versions`) ([Google](https://google.github.io/styleguide/go/best-practices#function-name-conventions)).
- No `Get` prefix on getters: `Session`, not `GetSession`. A setter is `SetX` ([Effective Go](https://go.dev/doc/effective_go#Getters)).
- Constructors are `New`, or `NewT` when the package has several types ([Effective Go](https://go.dev/doc/effective_go#package-names)).
- A function that returns only a bool reads as a question at the call site: `IsDir(p)`, `HasPrefix`, `Contains`, `Exists`, `isAgent(argv)`. The standard library uses these forms; no guide states a rule.
- Methods with a standard meaning keep the standard name and signature: `String`, `Close`, `Flush` ([Effective Go](https://go.dev/doc/effective_go#interface-names)).
- Name results only when they document something, such as two results of one type ([CRC](https://go.dev/wiki/CodeReviewComments#named-result-parameters)).

## Types and interfaces

- Types are nouns: `SkippedFile`, not `Skipped`.
- A one-method interface is the method plus `-er` (`backuper`) ([Effective Go](https://go.dev/doc/effective_go#interface-names)). A larger one names the role (`Adapter`, `Store`).

## Receivers

One or two letters taken from the type, the same on every method of that type, never `this` or `self` ([CRC](https://go.dev/wiki/CodeReviewComments#receiver-names)). **Lint** (revive `receiver-naming`).

## Constants, units and time

- MixedCaps, never `ALL_CAPS` or a `k` prefix; name the role, not the value ([Google](https://google.github.io/styleguide/go/decisions#constant-names)).
- A duration is a `time.Duration` with no unit in its name: `timeout`, `recopyAfter` ([Google](https://google.github.io/styleguide/go/decisions#flags)). **Lint** (revive `time-naming`).
- A raw number with a unit names the unit at the end: `Size` for bytes, as `os.FileInfo.Size` does, then `NS`, `Days`, `Seconds`: `maxFileSize`, `MtimeNS`, `keepDays`, `delaySeconds` (compare [`time.Unix(sec, nsec)`](https://pkg.go.dev/time#Unix)). Hold a duration as a `time.Duration` and convert only where a file format needs a number, as the hook `timeout` does.
- A point in time is Unix seconds in a `float64`, as the manifests store it, named for the event without a unit: `TakenAt`, `DeletedAt`, `started`, `now`.

## Initialisms

Every letter of an initialism has one case: `ID`, `URL`, `SHA256`, `JSON`, `R2`, `SQLite`; `id`, `url`, `sha` at the start of an unexported name ([CRC](https://go.dev/wiki/CodeReviewComments#initialisms)). Names that mirror an external format (`sessionId` in a JSON key, `UploadId` in the S3 SDK) keep the format's spelling.

## Errors

Sentinel values are `ErrFoo` or `errFoo`, error types `FooError` ([Gerrand, "What's in a name?"](https://go.dev/talks/2014/names.slide)). Error strings are lower case with no final punctuation and say where they came from (`"state: ..."`) ([CRC](https://go.dev/wiki/CodeReviewComments#error-strings)). **Lint** (revive `error-naming`, `error-strings`).

## Spelling

**repo.** Identifiers use US spelling, as the standard library does (`Normalize`, `Canceled`) and as [the Go project's policy](https://go.dev/wiki/Spelling) sets for its own code. Comments, messages and docs keep the British spelling they're written in.

## Tests

- Test functions name what they check in MixedCaps with no underscores, as a sentence when they check one behaviour: `TestRewrittenFileKeepsItsEarlierVersion`. `go test` requires the `Test` prefix ([testing](https://pkg.go.dev/testing)); the rest is **repo**.
- Subtest names are short identifiers, not prose, and never a table index ([Google](https://google.github.io/styleguide/go/decisions#subtest-names)).
- Failure messages name the call and put got before want: `Pieces = %v, want %v` ([Go Test Comments](https://go.dev/wiki/TestComments#got-before-want)).

## Files

Lower case. Underscores only where Go gives them meaning: `_test.go` and platform suffixes such as `_linux.go`, which silently restrict the build ([cmd/go](https://pkg.go.dev/cmd/go#hdr-Build_constraints)).
