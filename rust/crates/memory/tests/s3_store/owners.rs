//! `memory owners` against the loopback fake: the listing equals the local walk, paging,
//! strict keys, closed failure codes and no leaked endpoint, bucket, prefix or token.
use super::assemble::{PREFIX, Rig};
use super::consumer_gate::{captured, export, fixture, owners as walk_owners, run};
use super::fake::{Fake, Fault};
use super::{BUCKET, R};
use std::process::Output;

fn seed_key(server: &Fake, key: &str) {
    server.seed(&format!("/{BUCKET}/{PREFIX}{key}"), b"{}");
}

fn owners_cmd(rig: &Rig, args: &[&str]) -> R<Output> {
    let mut all: Vec<&std::ffi::OsStr> = vec!["owners".as_ref()];
    all.extend(args.iter().map(|a| std::ffi::OsStr::new(*a)));
    run(&rig.dir, &all)
}

fn text(bytes: &[u8]) -> String {
    String::from_utf8_lossy(bytes).into_owned()
}

/// No line of either stream names the endpoint, bucket, prefix or a token.
fn no_leak(out: &Output, server: &Fake, extra: &[&str]) {
    let all = format!("{}{}", text(&out.stdout), text(&out.stderr));
    let host = server.url().replace("http://", "");
    for needle in [
        host.as_str(),
        BUCKET,
        PREFIX,
        "rust-acceptance",
        "TOKEN-secret-9z",
    ]
    .into_iter()
    .chain(extra.iter().copied())
    {
        assert!(!all.contains(needle), "output contains {needle}");
    }
}

fn failing(server: &Fake, rig: &Rig, code: &str, exit: i32) -> R {
    let out = owners_cmd(rig, &[])?;
    assert_eq!(out.status.code(), Some(exit), "{}", text(&out.stderr));
    assert!(out.stdout.is_empty(), "no stdout on failure");
    assert_eq!(text(&out.stderr), format!("memory: {code}\n"));
    no_leak(&out, server, &[]);
    Ok(())
}

fn rig(server: &Fake, label: &str) -> R<Rig> {
    Rig::new(label, &server.url(), PREFIX, false)
}

#[test]
fn owners_lists_exactly_the_local_walk_and_every_owner_exports() -> R {
    let server = Fake::start()?;
    let fx = fixture(&server, "own-gate")?;
    let _daemon = captured(&fx)?;
    let want = walk_owners(&fx.rig)?;
    assert_eq!(want.len(), 5);
    let out = owners_cmd(&fx.rig, &[])?;
    assert_eq!(out.status.code(), Some(0), "{}", text(&out.stderr));
    let got: Vec<String> = text(&out.stdout).lines().map(str::to_owned).collect();
    assert_eq!(got, want);
    assert_eq!(text(&out.stderr), "memory: owners=5\n");
    no_leak(&out, &server, &[]);
    for (i, owner) in got.iter().enumerate() {
        export(&fx, owner, &fx.rig.dir.join(format!("exp/{i}")))?;
    }
    Ok(())
}

#[test]
fn owners_2500_keys_across_three_pages_sorted_with_colon_and_non_ascii() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-pages")?;
    let mut want: Vec<String> = (0..2499)
        .map(|i| format!("sessions/a/m/claude/s{i:05}"))
        .collect();
    want.push("sessions/a/m/pi/2026-11-17T10:00:00.000Z_x".to_owned());
    for owner in &want {
        seed_key(&server, &format!("{owner}/current.json"));
    }
    want.sort();
    let out = owners_cmd(&rig, &[])?;
    assert_eq!(out.status.code(), Some(0), "{}", text(&out.stderr));
    let got: Vec<String> = text(&out.stdout).lines().map(str::to_owned).collect();
    assert_eq!(got, want);
    assert_eq!(text(&out.stderr), "memory: owners=2500\n");
    assert_eq!(server.count("GET"), 3, "three pages");
    no_leak(&out, &server, &[]);
    Ok(())
}

#[test]
fn owners_empty_family_is_exit_0_without_stdout() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-empty")?;
    seed_key(&server, "shared/a/m/claude/current.json");
    let out = owners_cmd(&rig, &[])?;
    assert_eq!(out.status.code(), Some(0));
    assert!(out.stdout.is_empty());
    assert_eq!(text(&out.stderr), "memory: owners=0\n");
    Ok(())
}

#[test]
fn owners_shared_key_under_sessions_is_unexpected() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-shared")?;
    seed_key(&server, "sessions/a/m/claude/s1/current.json");
    seed_key(&server, "sessions/shared/a/m/current.json");
    failing(&server, &rig, "listing_unexpected_key", 1)
}

#[test]
fn owners_manifest_key_under_sessions_is_unexpected() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-manifest")?;
    seed_key(&server, "sessions/a/m/claude/s1/manifests/abc");
    failing(&server, &rig, "listing_unexpected_key", 1)
}

#[test]
fn owners_refused_owner_text_is_unexpected() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-bad-text")?;
    seed_key(&server, "sessions/a/m/bogus-agent/s1/current.json");
    failing(&server, &rig, "listing_unexpected_key", 1)
}

#[test]
fn owners_key_outside_the_prefix_fails() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-outside")?;
    server.queue(
        "GET",
        "",
        Fault::ListBody(concat!(
            "<?xml version=\"1.0\"?><ListBucketResult xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\">",
            "<EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>",
            "<Contents><Key>other/sessions/a/m/claude/s1/current.json</Key></Contents></ListBucketResult>"
        )),
    );
    failing(&server, &rig, "listing_outside_prefix", 1)
}

#[test]
fn owners_repeated_continuation_token_is_a_loop() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-loop")?;
    for _ in 0..3 {
        server.queue(
            "GET",
            "",
            Fault::ListBody(concat!(
                "<?xml version=\"1.0\"?><ListBucketResult xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\">",
                "<EncodingType>url</EncodingType><IsTruncated>true</IsTruncated>",
                "<NextContinuationToken>TOKEN-secret-9z</NextContinuationToken></ListBucketResult>"
            )),
        );
    }
    failing(&server, &rig, "listing_loop", 1)
}

#[test]
fn owners_a_500_fails_as_store() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-500")?;
    server.queue("GET", "", Fault::Status(500, None));
    failing(&server, &rig, "store", 1)
}

#[test]
fn owners_a_403_fails_as_store() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-403")?;
    server.queue("GET", "", Fault::Status(403, None));
    failing(&server, &rig, "store", 1)
}

#[test]
fn owners_unparsable_body_fails_as_store() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-garbage")?;
    server.queue("GET", "", Fault::ListBody("this is not xml"));
    failing(&server, &rig, "store", 1)
}

#[test]
fn owners_an_argument_is_usage_64() -> R {
    let server = Fake::start()?;
    let rig = rig(&server, "own-usage")?;
    let out = owners_cmd(&rig, &["--all"])?;
    assert_eq!(out.status.code(), Some(64));
    assert!(out.stdout.is_empty());
    assert_eq!(text(&out.stderr), "memory: usage\n");
    assert!(server.requests().is_empty());
    Ok(())
}

#[test]
fn owners_empty_config_prefix_is_exit_2() -> R {
    let server = Fake::start()?;
    let rig = Rig::new("own-noprefix", &server.url(), "", false)?;
    failing(&server, &rig, "config_prefix_required", 2)?;
    assert!(server.requests().is_empty(), "no network call");
    Ok(())
}
