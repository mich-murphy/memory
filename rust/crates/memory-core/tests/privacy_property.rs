//! Privacy policy oracles that outlive the config scrubbing (brief 57): credential identities,
//! unknown kinds, unscrubbed transcripts and which profiles are prefix-stable. The generated
//! document oracle covered the deleted `Profile::Config` scrubbing and is gone.
use memory_core::privacy::{
    self, Admission, Authorization, PolicyContext, Profile, Reason, ResolvedSourceFacts,
};

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "hegel-fixture-v1".to_owned(),
        rules: Some(privacy::rules_digest()),
        collector_paths: vec!["/fixture/collector".to_owned()],
        collector_identities: vec![99],
    }
}

fn facts(paths: Vec<String>, profile: Profile, nonce: u64) -> ResolvedSourceFacts {
    ResolvedSourceFacts {
        identities: paths,
        file_identity: Some(u128::from(nonce) + 100),
        ancestor_identities: vec![1, 2],
        path_resolved: true,
        root_resolved: true,
        owner: Some(format!("fixture-owner-{nonce:016x}")),
        profile,
    }
}

#[derive(Debug, PartialEq, Eq)]
enum Withheld {
    Admission,
    Transform(Reason),
    Authorization,
}

fn publish(facts: ResolvedSourceFacts, bytes: &[u8]) -> Result<privacy::AuthorizedBody, Withheld> {
    let policy = context();
    let Admission::Allow(source) = privacy::admit(&policy, facts) else {
        return Err(Withheld::Admission);
    };
    let transformed = privacy::transform(&source, bytes).map_err(Withheld::Transform)?;
    match privacy::authorize(&source, transformed, &policy) {
        Authorization::Allow(body) => Ok(body),
        Authorization::Deny(_) | Authorization::Unknown(_) => Err(Withheld::Authorization),
    }
}

#[test]
fn credential_identities_through_aliases_and_backup_suffixes_are_denied() {
    for credential in [
        "auth.json.bak.old~",
        "config.env-20260412-235959.gz",
        ".env.local.before-update.bak",
        "id_ed25519.agent-sessions-backup-20261014-101010",
        ".ssh/aliased-file",
    ] {
        let observed = facts(
            vec![
                "/fixture/alias.md".to_owned(),
                format!("/fixture/{credential}"),
                "/fixture/final-blob".to_owned(),
            ],
            Profile::Scanned,
            1,
        );
        assert!(
            matches!(
                privacy::admit(&context(), observed),
                Admission::Deny(Reason::Credential)
            ),
            "{credential}"
        );
    }
}

#[test]
fn unknown_kind_has_no_raw_route() {
    let observed = facts(
        vec!["/fixture/unknown-kind".to_owned()],
        Profile::Unknown,
        2,
    );
    assert!(matches!(
        privacy::admit(&context(), observed),
        Admission::Deny(Reason::UnknownKind)
    ));
}

#[test]
fn transcripts_and_other_known_sources_are_not_scrubbed() {
    // Go does not scrub transcripts either (DESIGN 8.3): invalid UTF-8 and secret-looking
    // text pass byte-identical, so a rewrite here would be a behaviour change.
    let mut transcript = b"transcript API_KEY=ordinary-literal\npartial {".to_vec();
    transcript.push(255);
    let body = publish(
        facts(
            vec!["/fixture/session.jsonl".to_owned()],
            Profile::Transcript,
            3,
        ),
        &transcript,
    );
    assert_eq!(body.map(|b| b.bytes().to_vec()).ok(), Some(transcript));
    let known = b"known safe raw asset; ordinary literal text";
    let body = publish(
        facts(
            vec!["/fixture/known.data".to_owned()],
            Profile::OtherKnown,
            4,
        ),
        known,
    );
    assert_eq!(body.map(|b| b.bytes().to_vec()).ok(), Some(known.to_vec()));
}

// ---------- named regressions found by the mutation baseline ----------

fn admitted(path: &str, profile: Profile) -> Result<privacy::AdmittedSource, Withheld> {
    match privacy::admit(&context(), facts(vec![path.to_owned()], profile, 1)) {
        Admission::Allow(source) => Ok(source),
        _ => Err(Withheld::Admission),
    }
}

#[test]
fn only_scanned_sources_are_not_prefix_stable() -> Result<(), Withheld> {
    // Mutants: `prefix_stable` constant true/false. A shell that scanned a delta alone would
    // miss a secret that spans the split.
    assert!(admitted("/f/session.jsonl", Profile::Transcript)?.prefix_stable());
    assert!(admitted("/f/known.data", Profile::OtherKnown)?.prefix_stable());
    assert!(!admitted("/f/run.json", Profile::Scanned)?.prefix_stable());
    Ok(())
}
