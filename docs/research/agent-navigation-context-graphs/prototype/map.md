# Compact BusinessCraft navigation map (authored routing, v1)

Authored **only** from `docs/repository/README.md` (Find your area, lines
13–24) and `repo.yaml`. This is an experiment candidate, not an ownership
registry or a catalogue of task answers. The machine-readable counterpart is
`routing.json`; route labels/terms, roots and starting documents are frozen
before evaluation. It deliberately does not enumerate implementation filenames.

| Concept / query vocabulary | Scope to search | Start reading |
| --- | --- | --- |
| Web screens, navigation, browser frontend | `web/apps/web/` | `docs/web/contributing.md` |
| Business rules, data access, shared UI | `web/packages/` | `docs/web/README.md` |
| BusinessCraft Agent, hosted service, CLI, desktop knowledge | `web/agent/` | `web/agent/README.md`; `docs/engineering/businesscraft-agent/README.md` |
| Classic business routines, definitions, DBL, legacy | `classic/src/dbl/` | `docs/knowledge/legacy/source-navigation.md` |
| Classic Windows client | `classic/src/windows/` | `ONBOARDING.md` |
| SQL, ISAM, automation runtime bridges, DBL RPC | `classic/src/runtimes/` | `docs/engineering/sql/isam-sql-testing.md` |
| ABC build, generation, language and proof tooling | `tools/abc/` | `tools/abc/docs/command-surface.md` |
| Classic automation scenarios and coverage | `classic/automation/` | `classic/automation/runbooks/rpc-agent-runbook.md` |
| Product knowledge and modernisation scope | `docs/knowledge/` | `docs/knowledge/README.md` |
| BC Model facts, intent and bindings | `web/agent/packages/bc-model/` | `docs/knowledge/model/README.md` |
| Cloud delivery, customer stamps, development and staging | `infra/web/` | `docs/devops/dev-staging-cicd.md` |
| Repository and worker helpers | `scripts/` | `scripts/README.md` |

`repo.yaml` confirms the Classic roots and distinguishes runtime source from
generated build/DBC trees. Generated output is not navigation evidence.
`classic/src/web/` is retained browser prior art; new browser product work belongs
in `web/` (repository map). No human owner is inferred from an area name.

## Agent use

1. Pick at most three matching areas. Use a distinctive filename or business term
   within those scopes rather than reading an entire tree.
2. Read the listed orientation document only when it helps disambiguate the area.
3. Keep returned paths bounded; then open the best source/test explicitly.
4. Do not read disabled instruction/skill files, dependency trees, dumps, generated
   trees, benchmark tasks or judgments as navigation input.

For the indexed candidate, use `--mode map_lexical` for scoped routing plus the
shared lexical index. `--mode map_only` returns only matching roots/start docs,
not implementation search results. Full commands and semantics are in `README.md`.
