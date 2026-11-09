//! Root coordinates: Go fixtures as synthetic rows plus order, proof and retention properties.
use hegel::{TestCase, generators as gs};
use memory_core::coordinates::*;
use memory_core::domain::Agent;
use memory_core::locator::RootNamespace;
use proptest::prelude::*;
use std::collections::BTreeSet;
use std::sync::atomic::{AtomicU64, Ordering};

const PI: Agent = Agent::Pi;
const CLAUDE: Agent = Agent::Claude;
const CODEX: Agent = Agent::Codex;

fn root(agent: Agent, p: &str) -> Root {
    Root {
        agent,
        path: p.to_owned(),
    }
}
fn alias(agent: Agent, r: &str, p: &str) -> RootAlias {
    RootAlias {
        agent,
        root: r.to_owned(),
        path: p.to_owned(),
    }
}
fn obs(agent: Agent, p: &str, identity: u128) -> Observation {
    Observation {
        agent,
        path: p.to_owned(),
        identity,
    }
}
fn set(items: &[&str]) -> BTreeSet<String> {
    items.iter().map(|s| (*s).to_owned()).collect()
}
fn compile(r: &[Root], a: &[RootAlias], o: &[Observation]) -> Result<Inventory, CoordError> {
    Inventory::compile(r, a, o)
}

// ---- Go fixtures (coordinates_test.go:29,56,104,128) ----

#[test]
fn default_and_custom_roots_need_no_filesystem() -> Result<(), CoordError> {
    let roots = [
        root(CLAUDE, "/home/absent/.claude"),
        root(CODEX, "/custom/codex"),
        root(PI, "/custom/pi"),
    ];
    let i = compile(&roots, &[], &[])?;
    for r in &roots {
        let p = format!("{}/sessions/file.jsonl", r.path);
        let c = i.resolve(&r.agent, &p, &[])?;
        assert_eq!(c.root, r.path);
        assert_eq!(c.logical_path, p);
        assert_eq!(c.path, p);
        assert_eq!(c.equivalent_paths, set(&[&p]));
        assert_eq!(c.legacy_roots, set(&[&r.path]));
        assert_eq!(c.identities, set(&[&p]));
        assert!(i.verified(&c));
    }
    for bad in [
        "relative",
        "/bad/../path",
        "/bad/./path",
        "/bad\0path",
        "/bad\npath",
        "/bad\rpath",
        "/a//b",
        "/a/",
        "",
    ] {
        assert_eq!(i.resolve(&PI, bad, &[]), Err(CoordError::InvalidPath));
    }
    assert_eq!(
        i.resolve(&PI, "/outside/file", &[]),
        Err(CoordError::Unowned)
    );
    // The Go "unknown agent" row: a path under another agent's root is not owned.
    assert_eq!(
        i.resolve(&CLAUDE, "/custom/pi/file", &[]),
        Err(CoordError::Unowned)
    );
    Ok(())
}

#[test]
fn nested_projection_keeps_nested_identity_and_ignores_order() -> Result<(), CoordError> {
    let mut roots = vec![root(PI, "/logical"), root(PI, "/logical/nested")];
    let mut aliases = vec![
        alias(PI, "/logical", "/observed"),
        alias(PI, "/logical/nested", "/observed/nested"),
    ];
    let i = compile(&roots, &aliases, &[])?;
    let c = i.resolve(&PI, "/observed/nested/file", &[])?;
    assert_eq!(c.root, "/logical/nested");
    assert_eq!(c.logical_path, "/logical/nested/file");
    roots.reverse();
    aliases.reverse();
    let j = compile(&roots, &aliases, &[])?;
    assert_eq!(i, j);
    assert_eq!(j.resolve(&PI, &c.path, &[])?, c);
    assert_eq!(
        c.equivalent_paths,
        set(&["/logical/nested/file", "/observed/nested/file"])
    );
    for want in ["/logical/nested", "/observed/nested", "/observed"] {
        assert!(c.legacy_roots.contains(want), "{want}");
    }
    // Forged request claims are never trusted.
    let mut forged = c.clone();
    forged.root = "/forged".to_owned();
    assert!(i.verified(&c));
    assert!(!i.verified(&forged));
    let mut moved = c.clone();
    moved.path = "/outside/file".to_owned();
    assert!(!i.verified(&moved));
    Ok(())
}

#[test]
fn parent_alias_owns_nested_without_explicit_route() -> Result<(), CoordError> {
    let i = compile(
        &[root(CLAUDE, "/logical"), root(CLAUDE, "/logical/nested")],
        &[alias(CLAUDE, "/logical", "/physical")],
        &[],
    )?;
    let c = i.resolve(&CLAUDE, "/physical/nested/x", &[])?;
    assert_eq!(c.root, "/logical/nested");
    assert!(c.legacy_roots.contains("/physical/nested"));
    assert_eq!(
        c.equivalent_paths,
        set(&["/logical/nested/x", "/physical/nested/x"])
    );
    Ok(())
}

#[test]
fn unproven_siblings_are_not_authorized() -> Result<(), CoordError> {
    // Go leaf rows: an exact alias confers nothing on siblings or children.
    let i = compile(&[root(PI, "/root")], &[], &[])?;
    for p in ["/outside/alias", "/outside/sibling", "/outside/alias/child"] {
        assert_eq!(i.resolve(&PI, p, &[]), Err(CoordError::Unowned));
    }
    let sib = i.resolve(&PI, "/root/old-c", &[])?;
    assert_eq!(sib.logical_path, sib.path);
    Ok(())
}

#[test]
fn rejected_declarations() {
    let base = [root(PI, "/logical"), root(PI, "/other")];
    type Row<'a> = (&'a [Root], Vec<RootAlias>, Vec<Observation>, CoordError);
    let rel = [root(PI, "relative")];
    let newline = [root(PI, "/bad\nprivate")];
    let rows: Vec<Row> = vec![
        (&rel, vec![], vec![], CoordError::InvalidPath),
        (&newline, vec![], vec![], CoordError::InvalidPath),
        (
            &base,
            vec![alias(PI, "/missing", "/physical")],
            vec![],
            CoordError::UnknownRoot,
        ),
        (
            &base,
            vec![alias(PI, "/logical/missing", "/physical")],
            vec![],
            CoordError::UnknownRoot,
        ),
        (
            &base,
            vec![alias(CLAUDE, "/logical", "/physical")],
            vec![],
            CoordError::UnknownRoot,
        ),
        (
            &base,
            vec![
                alias(PI, "/logical", "/physical"),
                alias(PI, "/other", "/physical"),
            ],
            vec![],
            CoordError::Ambiguous,
        ),
        (
            &base,
            vec![
                alias(PI, "/logical", "/physical"),
                alias(PI, "/other", "/physical/nested"),
            ],
            vec![],
            CoordError::Ambiguous,
        ),
        (
            &base,
            vec![
                alias(PI, "/logical", "/other"),
                alias(PI, "/other", "/logical"),
            ],
            vec![],
            CoordError::Ambiguous,
        ),
        (
            &base,
            vec![alias(PI, "/logical", "/logical/loop")],
            vec![],
            CoordError::Ambiguous,
        ),
        (
            &base,
            vec![alias(PI, "/logical", "relative")],
            vec![],
            CoordError::InvalidPath,
        ),
        (
            &base,
            vec![alias(PI, "rel", "/p")],
            vec![],
            CoordError::InvalidPath,
        ),
        (
            &base,
            vec![],
            vec![obs(PI, "rel", 1)],
            CoordError::InvalidPath,
        ),
        (
            &base,
            vec![],
            vec![obs(PI, "/p", 1), obs(PI, "/p", 2)],
            CoordError::Ambiguous,
        ),
        (
            &base,
            vec![],
            vec![obs(PI, "/logical", 1), obs(PI, "/other", 1)],
            CoordError::Ambiguous,
        ),
        (
            &base,
            vec![],
            vec![obs(PI, "/logical", 1), obs(PI, "/p", 1), obs(PI, "/p/q", 1)],
            CoordError::Ambiguous,
        ),
    ];
    for (n, (roots, aliases, observations, want)) in rows.iter().enumerate() {
        let got = compile(roots, aliases, observations);
        assert_eq!(got.as_ref().err(), Some(want), "row {n}");
        let text = got.err().map(|e| e.to_string()).unwrap_or_default();
        assert!(
            !text.contains("private") && !text.contains("/logical"),
            "row {n}"
        );
    }
}

#[test]
fn bounds_and_alias_depth() -> Result<(), CoordError> {
    let roots: Vec<Root> = (0..MAX_ROUTES)
        .map(|n| root(PI, &format!("/r{n}")))
        .collect();
    compile(&roots, &[], &[])?;
    assert_eq!(
        compile(&roots, &[alias(PI, "/r0", "/alias")], &[]),
        Err(CoordError::TooMany)
    );
    assert_eq!(
        compile(&roots, &[], &[obs(PI, "/x", 1)]),
        Err(CoordError::TooMany)
    );
    let mut nested = Vec::new();
    let (mut logical, mut physical) = (String::from("/nested"), String::from("/physical"));
    let mut aliases = Vec::new();
    for _ in 0..=MAX_DEPTH {
        nested.push(root(PI, &logical));
        aliases.push(alias(PI, &logical, &physical));
        logical.push_str("/nested");
        physical.push_str("/nested");
    }
    compile(&nested, aliases.get(..MAX_DEPTH).unwrap_or_default(), &[])?;
    assert_eq!(compile(&nested, &aliases, &[]), Err(CoordError::TooDeep));
    Ok(())
}

// ---- Go location_test.go:12,70,96 as shell observations ----

#[test]
fn var_alias_resolves_to_one_namespace() -> Result<(), CoordError> {
    let logical = "/var/folders/x/pi";
    let physical = "/private/var/folders/x/pi";
    let i = compile(
        &[root(PI, logical)],
        &[],
        &[obs(PI, logical, 7), obs(PI, physical, 7)],
    )?;
    let a = i.resolve(&PI, &format!("{logical}/s.jsonl"), &[])?;
    let chain = vec![
        format!("{physical}/s.jsonl"),
        "/private/var".to_owned(),
        "/var".to_owned(),
    ];
    let b = i.resolve(&PI, &format!("{physical}/s.jsonl"), &chain)?;
    assert_eq!(a.root, logical);
    assert_eq!(b.root, logical);
    assert_eq!(a.namespace, b.namespace);
    assert!(RootNamespace::new(&a.namespace).is_ok());
    assert_eq!(b.logical_path, format!("{logical}/s.jsonl"));
    assert_eq!(b.equivalent_paths, a.equivalent_paths);
    assert!(b.legacy_roots.contains(physical) && b.legacy_roots.contains(logical));
    // Every intermediate is retained, plus the logical spelling.
    for s in chain.iter().chain([&format!("{logical}/s.jsonl")]) {
        assert!(b.identities.contains(s), "{s}");
    }
    // The same root under another agent is a different namespace.
    let other = compile(&[root(CODEX, logical)], &[], &[])?.resolve(&CODEX, logical, &[])?;
    assert_ne!(other.namespace, a.namespace);
    Ok(())
}

#[test]
fn explicit_alias_pair_is_accepted() -> Result<(), CoordError> {
    let i = compile(
        &[root(PI, "/var/x")],
        &[alias(PI, "/var/x", "/private/var/x")],
        &[],
    )?;
    let c = i.resolve(&PI, "/private/var/x/f", &[])?;
    assert_eq!(
        (c.root.as_str(), c.logical_path.as_str()),
        ("/var/x", "/var/x/f")
    );
    Ok(())
}

#[test]
fn unproven_physical_aliases_are_never_authorized() -> Result<(), CoordError> {
    let logical = "/var/x";
    let physical = "/private/var/x";
    let cases: Vec<Vec<Observation>> = vec![
        vec![],
        vec![obs(PI, logical, 1)],
        vec![obs(PI, physical, 1)],
        vec![obs(PI, logical, 1), obs(PI, physical, 2)],
        vec![obs(PI, logical, 1), obs(CODEX, physical, 1)],
        vec![obs(CODEX, logical, 1), obs(PI, physical, 1)],
    ];
    for (n, o) in cases.iter().enumerate() {
        let i = compile(&[root(PI, logical)], &[], o)?;
        assert_eq!(
            i.resolve(&PI, &format!("{physical}/f"), &[]),
            Err(CoordError::Unowned),
            "case {n}"
        );
        assert!(i.resolve(&PI, &format!("{logical}/f"), &[]).is_ok());
    }
    Ok(())
}

#[test]
fn case_insensitive_alias_needs_proof_and_distinct_dirs_stay_distinct() -> Result<(), CoordError> {
    let lower = "/home/u/.pi";
    let proven = "/Home/U/.pi";
    let unobserved = "/HOME/U/.PI";
    let i = compile(
        &[root(PI, lower)],
        &[],
        &[obs(PI, lower, 5), obs(PI, proven, 5)],
    )?;
    let c = i.resolve(&PI, &format!("{proven}/s"), &[])?;
    assert_eq!(c.root, lower);
    // Complete roots are never lowercased: an unobserved spelling is simply unowned.
    assert_eq!(
        i.resolve(&PI, &format!("{unobserved}/s"), &[]),
        Err(CoordError::Unowned)
    );
    // Case-sensitive filesystem: distinct identities, so the upper-case dir is distinct.
    let d = compile(
        &[root(PI, "/protected")],
        &[],
        &[obs(PI, "/protected", 1), obs(PI, "/PROTECTED", 2)],
    )?;
    assert_eq!(
        d.resolve(&PI, "/PROTECTED/private.data", &[]),
        Err(CoordError::Unowned)
    );
    // Both declared: two roots, two namespaces.
    let both = compile(
        &[root(PI, "/protected"), root(PI, "/PROTECTED")],
        &[],
        &[obs(PI, "/protected", 1), obs(PI, "/PROTECTED", 2)],
    )?;
    let x = both.resolve(&PI, "/protected/f", &[])?;
    let y = both.resolve(&PI, "/PROTECTED/f", &[])?;
    assert_ne!(x.namespace, y.namespace);
    Ok(())
}

#[test]
fn unrelated_observations_are_ignored_and_chain_is_kept_verbatim() -> Result<(), CoordError> {
    let i = compile(
        &[root(PI, "/r")],
        &[],
        &[obs(PI, "/r", 1), obs(PI, "/noise", 9), obs(PI, "/r2", 1)],
    )?;
    assert_eq!(i.resolve(&PI, "/noise/f", &[]), Err(CoordError::Unowned));
    let chain = vec!["not absolute\n".to_owned(), "/r/link".to_owned()];
    let c = i.resolve(&PI, "/r2/f", &chain)?;
    assert!(chain.iter().all(|s| c.identities.contains(s)));
    assert!(c.identities.contains("/r2/f") && c.identities.contains("/r/f"));
    let slash = compile(&[root(PI, "/")], &[], &[])?;
    let c = slash.resolve(&PI, "/", &[])?;
    assert_eq!((c.root.as_str(), c.logical_path.as_str()), ("/", "/"));
    assert_eq!(slash.resolve(&PI, "/a/b", &[])?.root, "/");
    Ok(())
}

// ---- properties ----

// The last entry is reserved for proof twins: roots, aliases and chains never use it, so it is
// never covered by a declared root or alias.
const POOL: [&str; 9] = [
    "/a", "/a/b", "/a/b/c", "/p", "/p/b", "/q", "/A", "/Q/x", "/z/x",
];
const PROOF_PATH: &str = "/z/x";
fn pool(i: u8) -> &'static str {
    POOL.get(usize::from(i) % (POOL.len() - 1))
        .copied()
        .unwrap_or("/")
}
fn agent(i: u8) -> Agent {
    if i.is_multiple_of(2) { CLAUDE } else { PI }
}

type Scenario = (Vec<Root>, Vec<RootAlias>, Vec<Observation>, Vec<String>);
fn decode(bytes: &[u8]) -> Scenario {
    let (mut roots, mut aliases, mut observations, mut chain) = (vec![], vec![], vec![], vec![]);
    for c in bytes.chunks(4) {
        let g = |i: usize| c.get(i).copied().unwrap_or(0);
        let declared = |k: u8| {
            let r: &Root = roots.get(usize::from(k) % roots.len().max(1))?;
            Some(r.clone())
        };
        match g(0) % 6 {
            0 => roots.push(root(agent(g(1)), pool(g(2)))),
            1 => {
                if let Some(r) = declared(g(2)) {
                    aliases.push(alias(r.agent, &r.path, pool(g(3))));
                }
            }
            2 | 3 => {
                let (a, p) = match (g(1) & 2 != 0, declared(g(2))) {
                    (true, Some(r)) => (r.agent, r.path),
                    _ => (agent(g(1)), pool(g(2)).to_owned()),
                };
                observations.push(obs(a, &p, u128::from(g(3) % 3)));
            }
            4 => chain.push(if g(1) % 4 == 0 {
                "weird\n".to_owned()
            } else {
                pool(g(2)).to_owned()
            }),
            // Proof twin: the same agent observes identity k at a declared root and at a path no
            // declared root or alias covers, so that path resolves by proof alone.
            _ => {
                if let Some(r) = declared(g(2)) {
                    let k = u128::from(g(3) % 3);
                    observations.push(obs(r.agent.clone(), &r.path, k));
                    observations.push(obs(r.agent, PROOF_PATH, k));
                }
            }
        }
    }
    (roots, aliases, observations, chain)
}

fn shuffle<T>(v: &mut [T], mut seed: u64) {
    for i in (1..v.len()).rev() {
        seed = seed
            .wrapping_mul(6_364_136_223_846_793_005)
            .wrapping_add(1_442_695_040_888_963_407);
        v.swap(i, usize::try_from(seed >> 33).unwrap_or(0) % (i + 1));
    }
}

fn under(path: &str, prefix: &str) -> bool {
    path == prefix
        || path
            .strip_prefix(prefix)
            .is_some_and(|r| r.starts_with('/'))
}

/// Independent justification: a path may only resolve below a declared root, an explicit
/// alias, or an observation whose identity equals an observed declared root's identity.
fn justified(s: &Scenario, a: &Agent, path: &str) -> bool {
    let (roots, aliases, observations, _) = s;
    let at_root = |id: u128| {
        roots.iter().any(|r| {
            &r.agent == a
                && observations
                    .iter()
                    .any(|o| &o.agent == a && o.path == r.path && o.identity == id)
        })
    };
    roots.iter().any(|r| &r.agent == a && under(path, &r.path))
        || aliases
            .iter()
            .any(|x| &x.agent == a && under(path, &x.path))
        || observations
            .iter()
            .any(|o| &o.agent == a && at_root(o.identity) && under(path, &o.path))
}

struct Stats {
    compiled: AtomicU64,
    resolved: AtomicU64,
    proof_only: AtomicU64,
}
static HEGEL_STATS: Stats = Stats::new();
static PROPTEST_STATS: Stats = Stats::new();
impl Stats {
    const fn new() -> Self {
        Self {
            compiled: AtomicU64::new(0),
            resolved: AtomicU64::new(0),
            proof_only: AtomicU64::new(0),
        }
    }
    fn guard(&self) {
        for (name, n) in [
            ("compiled", &self.compiled),
            ("resolved", &self.resolved),
            ("proof-only", &self.proof_only),
        ] {
            assert!(
                n.load(Ordering::Relaxed) > 0,
                "generator never produced {name}"
            );
        }
    }
}
fn bump(c: &AtomicU64) {
    c.fetch_add(1, Ordering::Relaxed);
}

fn check(bytes: &[u8], seed: u64, stats: &Stats) {
    let s = decode(bytes);
    let first = compile(&s.0, &s.1, &s.2);
    // (a) order independence over roots, aliases, observations and chain.
    let mut t = s.clone();
    shuffle(&mut t.0, seed);
    shuffle(&mut t.1, seed ^ 1);
    shuffle(&mut t.2, seed ^ 2);
    shuffle(&mut t.3, seed ^ 3);
    let second = compile(&t.0, &t.1, &t.2);
    assert_eq!(first.is_ok(), second.is_ok());
    let (Ok(i), Ok(j)) = (first, second) else {
        return;
    };
    bump(&stats.compiled);
    assert_eq!(i, j);
    for ag in [CLAUDE, PI, CODEX] {
        for p in POOL {
            let x = i.resolve(&ag, p, &s.3);
            assert_eq!(x, j.resolve(&ag, p, &t.3));
            // (b) never a root without proof.
            let Ok(c) = x else {
                continue;
            };
            bump(&stats.resolved);
            assert!(justified(&s, &ag, p), "unproven alias resolved");
            assert!(s.0.iter().any(|r| r.agent == ag && r.path == c.root));
            assert!(RootNamespace::new(&c.namespace).is_ok());
            let declared = s.0.iter().any(|r| r.agent == ag && under(p, &r.path))
                || s.1.iter().any(|x| x.agent == ag && under(p, &x.path));
            if !declared {
                bump(&stats.proof_only);
            }
            // (c) every input spelling is retained.
            assert!(s.3.iter().all(|e| c.identities.contains(e)));
            assert!(c.identities.contains(p) && c.identities.contains(&c.logical_path));
            assert!(c.equivalent_paths.is_subset(&c.identities));
            assert!(i.verified(&c));
        }
    }
}

#[test]
fn hegel_coordinates_order_proof_and_retention() {
    hegel::Hegel::new(|tc: TestCase| {
        let n = usize::from(tc.draw(gs::integers::<u8>()) % 48) * 4;
        let bytes: Vec<u8> = (0..n).map(|_| tc.draw(gs::integers::<u8>())).collect();
        let seed = u64::from(tc.draw(gs::integers::<u16>()));
        check(&bytes, seed, &HEGEL_STATS);
    })
    .run();
    HEGEL_STATS.guard();
}

#[test]
fn proptest_coordinates_order_proof_and_retention() {
    let mut runner = proptest::test_runner::TestRunner::default();
    let strat = (prop::collection::vec(any::<u8>(), 0..192), any::<u16>());
    let result = runner.run(&strat, |(bytes, seed)| {
        check(&bytes, u64::from(seed), &PROPTEST_STATS);
        Ok(())
    });
    assert!(result.is_ok(), "{result:?}");
    PROPTEST_STATS.guard();
}

#[test]
fn stats_guard_bites() {
    let r = std::panic::catch_unwind(|| Stats::new().guard());
    assert!(r.is_err());
}
