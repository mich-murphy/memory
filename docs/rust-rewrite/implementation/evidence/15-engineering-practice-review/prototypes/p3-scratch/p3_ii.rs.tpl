use memory_core::privacy::{self, Admission, Authorization, PolicyContext, Profile, ResolvedSourceFacts};
fn mk() -> PolicyContext {
    PolicyContext { valid: true, revision: "r".into(), rules: Some(privacy::rules_digest()),
        collector_paths: vec![], collector_identities: vec![99]  }
}
fn facts(p: &str, prof: Profile) -> ResolvedSourceFacts {
    ResolvedSourceFacts { identities: vec![p.to_owned()], file_identity: Some(1), ancestor_identities: vec![2],
        path_resolved: true, root_resolved: true, owner: Some("o".into()), profile: prof }
}
fn adm(c: &PolicyContext, p: &str, prof: Profile) -> privacy::AdmittedSource {
    match privacy::admit(c, facts(p, prof)) { Admission::Allow(a) => a, _ => panic!("admit") }
}
fn show(r: Authorization) -> String {
    match r { Authorization::Allow(_) => "Allow".into(), Authorization::Deny(x) => format!("Deny({x:?})"), Authorization::Unknown(x) => format!("Unknown({x:?})") }
}

#[test] fn cross() {
    let ctx = mk();
    let x = adm(&ctx, "transcript.jsonl", Profile::Transcript);
    let y = adm(&ctx, "settings.json", Profile::Config);
    let red = privacy::transform(&x, b"raw").unwrap_or_else(|_| panic!("t"));
    println!("RESULT_II {}", show(privacy::authorize(@Y@, red, &ctx)));
}
