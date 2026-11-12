//! Synthetic per-piece privacy sequence measurement (admit, transform, authorize); no real content.
use memory_core::privacy::{
    self, Admission, AdmittedSource, Authorization, PolicyContext, Profile, ResolvedSourceFacts,
};
use std::hint::black_box;
use std::time::Instant;

type R = Result<(), String>;

fn context() -> PolicyContext {
    PolicyContext {
        valid: true,
        revision: "bench-v1".into(),
        rules: Some(privacy::rules_digest()),
        collector_paths: vec![
            "/collector/config.toml".into(),
            "/collector/state".into(),
            "/collector/token".into(),
        ],
        collector_identities: vec![90, 91, 92],
    }
}

fn facts() -> ResolvedSourceFacts {
    ResolvedSourceFacts {
        identities: vec![
            "/home/u/.agent/projects/p1/session-0001.jsonl".into(),
            "/home/u/link/projects/p1/session-0001.jsonl".into(),
            "/mnt/data/agent/projects/p1/session-0001.jsonl".into(),
            "/home/u/.agent/projects/p1".into(),
        ],
        file_identity: Some(1001),
        ancestor_identities: vec![11, 12, 13, 14, 15, 16],
        path_resolved: true,
        root_resolved: true,
        owner: Some("bench-owner".into()),
        profile: Profile::Transcript,
    }
}

fn jsonl(len: usize) -> Vec<u8> {
    let line = b"{\"type\":\"message\",\"role\":\"user\",\"text\":\"synthetic filler line\"}\n";
    let mut v = Vec::with_capacity(len + line.len());
    while v.len() < len {
        v.extend_from_slice(line);
    }
    v.truncate(len);
    v
}

fn admit_ok(ctx: &PolicyContext, f: ResolvedSourceFacts) -> Result<AdmittedSource, String> {
    match privacy::admit(ctx, f) {
        Admission::Allow(a) => Ok(a),
        _ => Err("fixture admission failed".into()),
    }
}

/// Mean ns per call: (obtaining the admitted source, transform, authorize, whole sequence).
fn run(size: usize, iters: u32) -> Result<(f64, f64, f64, f64), String> {
    let ctx = context();
    let f = facts();
    let held = admit_ok(&ctx, f.clone())?;
    let data = jsonl(size);
    let (mut adm, mut tr, mut au) = (0u128, 0u128, 0u128);
    let total = Instant::now();
    for _ in 0..iters {
        let t = Instant::now();
        // BENCH-LINE: obtain the admitted source for this piece.
        let for_authorize = &held;
        adm += t.elapsed().as_nanos();
        let t = Instant::now();
        let red = privacy::transform(black_box(&held), black_box(&data))
            .map_err(|_| "transform failed".to_owned())?;
        tr += t.elapsed().as_nanos();
        let t = Instant::now();
        // BENCH-LINE: authorize call.
        let out = privacy::authorize(for_authorize, red, &ctx);
        au += t.elapsed().as_nanos();
        match out {
            Authorization::Allow(b) => {
                black_box(b.bytes().len());
            }
            _ => return Err("authorize failed".into()),
        }
    }
    let n = f64::from(iters);
    Ok((
        adm as f64 / n,
        tr as f64 / n,
        au as f64 / n,
        total.elapsed().as_nanos() as f64 / n,
    ))
}

#[test]
#[ignore = "measurement: run with --release -- --ignored --nocapture"]
fn authorize_sequence() -> R {
    eprintln!(
        "size_of AdmittedSource = {}",
        std::mem::size_of::<AdmittedSource>()
    );
    for (label, size, iters) in [("4KiB", 4096usize, 20_000u32), ("1MiB", 1 << 20, 300)] {
        run(size, iters / 10)?;
        let (a, t, z, all) = run(size, iters)?;
        eprintln!(
            "RESULT {label} admission_ns={a:.0} transform_ns={t:.0} authorize_ns={z:.0} total_ns={all:.0}"
        );
    }
    Ok(())
}
