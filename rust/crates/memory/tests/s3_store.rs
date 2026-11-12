//! The S3 adapter against a local std-only HTTP server (no remote host is contacted): R2
//! conditional conformance, one HTTP exchange per call, request shape, the reply mapping,
//! secret hygiene, and the commit protocol (DESIGN.md 7.4, amendment A6) through the adapter.
//!
//! `s3_store/skeleton.rs` is a module of this test binary because it shares the fake server
//! (`common/store.rs` explains why a helper file has one set of users). Its ignored test is the
//! only one here that contacts a remote host.
use memory::commit::{
    Commit, CommitEnd, Failure, KnownOurs, LandedHow, Upload, commit, commit_known,
};
use memory::environment::{Published, Store, StoreError};
use memory::s3::{Operation, S3ConfigError, S3Store, TimeoutPolicy, map_reply, map_transport};
use memory_core::domain::{
    Agent, ArchiveId, ETag, MachineId, Offset, Owner, SessionId, SessionKey, SourceClass, SourceId,
    SourceKind,
};
use memory_core::limits::current_key;
use memory_core::manifest::{
    Change, Current, EpochStamp, EpochWriter, Header, Piece, SourceWrite, Timestamp, Writer,
    manifest_hash, object_key,
};
use std::net::TcpListener;
use std::time::Duration;

#[path = "s3_store/assemble.rs"]
mod assemble;
#[path = "common/clock.rs"]
mod clock;
#[path = "common/s3_fake.rs"]
mod fake;
#[path = "s3_store/skeleton.rs"]
mod skeleton;
use fake::{Fake, Fault, content_md5_of, etag_of};

type R<T = ()> = Result<T, String>;

const BUCKET: &str = "bkt";
const KEY_ID: &str = "AKIATESTKEY0000";
const SECRET: &str = "s3cr3t-test-secret-value";

fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}
fn need<T>(value: Option<T>, what: &str) -> R<T> {
    value.ok_or_else(|| what.to_owned())
}
fn short() -> TimeoutPolicy {
    TimeoutPolicy::new(
        Duration::from_millis(400),
        1 << 20,
        Duration::from_millis(400),
    )
}
fn store_at(url: &str, prefix: &str, timeouts: TimeoutPolicy) -> R<S3Store> {
    S3Store::new(url, BUCKET, "auto", KEY_ID, SECRET, prefix, timeouts).map_err(e)
}
fn rig(prefix: &str) -> R<(Fake, S3Store)> {
    let fake = Fake::start()?;
    let store = store_at(&fake.url(), prefix, TimeoutPolicy::PRODUCTION)?;
    Ok((fake, store))
}
fn body(bytes: &[u8]) -> Published {
    Published::sim(bytes.to_vec())
}
fn tag(text: &str) -> R<ETag> {
    ETag::try_from(text.to_owned()).map_err(e)
}

// ------------------------------------------------------------------ conformance

#[test]
fn conformance_with_the_r2_conditional_rules() -> R {
    let (fake, mut s) = rig("")?;
    // Create, then repeat-create.
    let created = s.put_if_absent("a/doc.json", body(b"v1")).map_err(e)?;
    assert_eq!(created, tag(&etag_of(b"v1"))?);
    assert_eq!(
        s.put_if_absent("a/doc.json", body(b"other")),
        Err(StoreError::PreconditionFailed)
    );
    // GET returns the body and the very ETag the PUT returned, quotes included.
    let got = s.get("a/doc.json").map_err(e)?;
    assert_eq!(got.body, b"v1");
    assert_eq!(got.etag, created);
    assert!(got.etag.as_str().starts_with('"'));
    // If-Match: fresh succeeds, stale fails, missing key fails with 412 (never NotFound).
    let second = s
        .put_if_match("a/doc.json", &created, body(b"v2"))
        .map_err(e)?;
    assert_ne!(second, created);
    assert_eq!(
        s.put_if_match("a/doc.json", &created, body(b"v3")),
        Err(StoreError::PreconditionFailed)
    );
    assert_eq!(
        s.put_if_match("missing.json", &created, body(b"v3")),
        Err(StoreError::PreconditionFailed)
    );
    assert_eq!(s.get("missing.json"), Err(StoreError::NotFound));
    let (stored, _) = need(fake.object("/bkt/a/doc.json"), "object")?;
    assert_eq!(stored, b"v2");
    Ok(())
}

#[test]
fn status_404_means_not_found_only_for_get_and_put_if_absent() -> R {
    let (fake, mut s) = rig("")?;
    fake.queue("PUT", "x", Fault::Status(404, None));
    assert_eq!(s.put_if_absent("x", body(b"1")), Err(StoreError::NotFound));
    fake.queue("PUT", "x", Fault::Status(404, None));
    let fence = tag("\"f\"")?;
    assert_eq!(
        s.put_if_match("x", &fence, body(b"1")),
        Err(StoreError::PreconditionFailed)
    );
    Ok(())
}

// ------------------------------------------------------------------ one exchange per call

fn one_put_after(fault: Fault, expected: StoreError) -> R {
    let (fake, mut s) = rig("")?;
    fake.queue("PUT", "current.json", fault);
    let result = s.put_if_absent("o/current.json", body(b"{}"));
    assert_eq!(result, Err(expected), "{fault:?}");
    assert_eq!(fake.count("PUT"), 1, "{fault:?}: the adapter resent");
    Ok(())
}

#[test]
fn a_503_after_a_landed_write_is_throttled_and_sent_once() -> R {
    one_put_after(Fault::ApplyThenStatus(503), StoreError::Throttled(None))
}
#[test]
fn a_500_after_a_landed_write_is_server_and_sent_once() -> R {
    one_put_after(Fault::ApplyThenStatus(500), StoreError::Server)
}
#[test]
fn a_dropped_reply_is_ambiguous_and_sent_once() -> R {
    one_put_after(Fault::ApplyThenDrop, StoreError::Ambiguous)
}
#[test]
fn a_redirect_is_not_followed() -> R {
    one_put_after(Fault::Status(307, None), StoreError::Server)
}

#[test]
fn a_hang_is_ambiguous_for_a_write_and_offline_for_a_read() -> R {
    let fake = Fake::start()?;
    let mut s = store_at(&fake.url(), "", short())?;
    fake.queue("PUT", "k", Fault::ApplyThenHang(Duration::from_secs(3)));
    assert_eq!(s.put_if_absent("k", body(b"1")), Err(StoreError::Ambiguous));
    assert_eq!(fake.count("PUT"), 1);
    fake.queue("GET", "k", Fault::ApplyThenHang(Duration::from_secs(3)));
    assert_eq!(s.get("k").map(|g| g.body), Err(StoreError::Offline));
    assert_eq!(fake.count("GET"), 1);
    Ok(())
}

#[test]
fn a_refused_connection_is_offline() -> R {
    let listener = TcpListener::bind("127.0.0.1:0").map_err(e)?;
    let url = format!("http://{}", listener.local_addr().map_err(e)?);
    drop(listener);
    let mut s = store_at(&url, "", short())?;
    assert_eq!(s.put_if_absent("k", body(b"1")), Err(StoreError::Offline));
    assert_eq!(s.get("k").map(|g| g.body), Err(StoreError::Offline));
    Ok(())
}

#[test]
fn auth_and_throttle_statuses_map_with_retry_after() -> R {
    let (fake, mut s) = rig("")?;
    let fence = tag("\"f\"")?;
    let cases: [(u16, Option<&'static str>, StoreError); 7] = [
        (401, None, StoreError::Auth),
        (403, None, StoreError::Auth),
        (429, None, StoreError::Throttled(None)),
        (429, Some("7"), StoreError::Throttled(Some(7))),
        (503, Some("120"), StoreError::Throttled(Some(120))),
        (
            503,
            Some("Wed, 21 Oct 2026 07:28:00 GMT"),
            StoreError::Throttled(None),
        ),
        (400, None, StoreError::Server),
    ];
    for (status, retry_after, expected) in cases {
        fake.queue("PUT", "k", Fault::Status(status, retry_after));
        assert_eq!(s.put_if_absent("k", body(b"1")), Err(expected), "{status}");
        fake.queue("PUT", "k", Fault::Status(status, retry_after));
        let matched = s.put_if_match("k", &fence, body(b"1"));
        assert_eq!(matched, Err(expected), "{status}");
        fake.queue("GET", "k", Fault::Status(status, retry_after));
        assert_eq!(s.get("k").map(|g| g.body), Err(expected), "{status}");
    }
    assert_eq!(
        StoreError::Throttled(Some(7)).retry_after(),
        Some(Duration::from_secs(7))
    );
    Ok(())
}

// ------------------------------------------------------------------ request shape

#[test]
fn requests_carry_the_prefixed_path_the_condition_and_the_md5() -> R {
    let (fake, mut s) = rig("rust-acceptance/run-1/")?;
    let created = s
        .put_if_absent("objects/a.zst", body(b"first body"))
        .map_err(e)?;
    s.put_if_match("objects/a.zst", &created, body(b"second body"))
        .map_err(e)?;
    s.get("objects/a.zst").map_err(e)?;
    let seen = fake.requests();
    let [create, replace, read] = seen.as_slice() else {
        return Err(format!("expected 3 requests, saw {}", seen.len()));
    };
    let path = "/bkt/rust-acceptance/run-1/objects/a.zst";
    assert_eq!(
        (create.method.as_str(), create.path.as_str()),
        ("PUT", path)
    );
    assert_eq!(create.if_none_match.as_deref(), Some("*"));
    assert_eq!(create.if_match, None);
    assert_eq!(
        create.content_md5.as_deref(),
        Some(content_md5_of(b"first body").as_str())
    );
    assert_eq!(create.body, b"first body");
    assert_eq!(replace.path, path);
    assert_eq!(replace.if_match.as_deref(), Some(created.as_str()));
    assert_eq!(replace.if_none_match, None);
    assert_eq!(
        replace.content_md5.as_deref(),
        Some(content_md5_of(b"second body").as_str())
    );
    assert_eq!((read.method.as_str(), read.path.as_str()), ("GET", path));
    assert_eq!(read.status, 200);
    assert!(fake.object(path).is_some());
    assert!(fake.object("/bkt/objects/a.zst").is_none());
    Ok(())
}

#[test]
fn a_body_damaged_in_flight_is_rejected_by_the_md5_and_maps_to_server() -> R {
    let (fake, mut s) = rig("")?;
    fake.queue("PUT", "k", Fault::CorruptInFlight);
    assert_eq!(
        s.put_if_absent("k", body(b"precious bytes")),
        Err(StoreError::Server)
    );
    assert!(fake.object("/bkt/k").is_none());
    let seen = fake.requests();
    assert_eq!(seen.iter().map(|r| r.status).collect::<Vec<_>>(), [400]);
    Ok(())
}

// ------------------------------------------------------------------ pure mapping

type Row = (
    Operation,
    u16,
    Option<&'static str>,
    Option<&'static str>,
    Result<ETag, StoreError>,
);

#[test]
fn reply_mapping_table() -> R {
    use Operation::{Get, PutIfAbsent, PutIfMatch};
    let quoted = tag("\"abc\"")?;
    let weak = tag("W/\"abc\"")?;
    let ok = |t: &ETag| Ok(t.clone());
    let table: Vec<Row> = vec![
        (PutIfAbsent, 200, Some("\"abc\""), None, ok(&quoted)),
        (Get, 200, Some("\"abc\""), None, ok(&quoted)),
        (PutIfMatch, 200, Some("W/\"abc\""), None, ok(&weak)),
        (PutIfAbsent, 200, None, None, Err(StoreError::Ambiguous)),
        (Get, 200, Some(""), None, Err(StoreError::Ambiguous)),
        (
            PutIfAbsent,
            412,
            None,
            None,
            Err(StoreError::PreconditionFailed),
        ),
        (
            PutIfMatch,
            412,
            None,
            None,
            Err(StoreError::PreconditionFailed),
        ),
        (Get, 404, None, None, Err(StoreError::NotFound)),
        (PutIfAbsent, 404, None, None, Err(StoreError::NotFound)),
        (
            PutIfMatch,
            404,
            None,
            None,
            Err(StoreError::PreconditionFailed),
        ),
        (Get, 401, None, None, Err(StoreError::Auth)),
        (PutIfAbsent, 403, None, None, Err(StoreError::Auth)),
        (Get, 429, None, None, Err(StoreError::Throttled(None))),
        (
            Get,
            429,
            None,
            Some("5"),
            Err(StoreError::Throttled(Some(5))),
        ),
        (
            Get,
            503,
            None,
            Some(" 9 "),
            Err(StoreError::Throttled(Some(9))),
        ),
        (Get, 503, None, Some("-1"), Err(StoreError::Throttled(None))),
        (
            Get,
            503,
            None,
            Some("soon"),
            Err(StoreError::Throttled(None)),
        ),
        (PutIfAbsent, 400, None, None, Err(StoreError::Server)),
        (PutIfAbsent, 500, None, None, Err(StoreError::Server)),
        (PutIfAbsent, 502, None, None, Err(StoreError::Server)),
        (Get, 301, None, None, Err(StoreError::Server)),
        (Get, 307, None, None, Err(StoreError::Server)),
        (Get, 204, None, None, Err(StoreError::Server)),
    ];
    for (op, status, etag, retry_after, expected) in table {
        assert_eq!(
            map_reply(op, status, etag, retry_after),
            expected,
            "{op:?} {status} {etag:?} {retry_after:?}"
        );
    }
    Ok(())
}

#[test]
fn transport_mapping_table() {
    use ureq::{Error as E, Timeout as T};
    let other = || E::Io(std::io::Error::other("x"));
    let refused = || E::Io(std::io::Error::from(std::io::ErrorKind::ConnectionRefused));
    let table: Vec<(E, StoreError, StoreError)> = vec![
        (E::HostNotFound, StoreError::Offline, StoreError::Offline),
        (
            E::ConnectionFailed,
            StoreError::Offline,
            StoreError::Offline,
        ),
        (E::Tls("x"), StoreError::Offline, StoreError::Offline),
        (
            E::Timeout(T::Resolve),
            StoreError::Offline,
            StoreError::Offline,
        ),
        (
            E::Timeout(T::Connect),
            StoreError::Offline,
            StoreError::Offline,
        ),
        (refused(), StoreError::Offline, StoreError::Offline),
        (
            E::Timeout(T::Global),
            StoreError::Ambiguous,
            StoreError::Offline,
        ),
        (
            E::Timeout(T::RecvResponse),
            StoreError::Ambiguous,
            StoreError::Offline,
        ),
        (other(), StoreError::Ambiguous, StoreError::Offline),
    ];
    for (error, on_write, on_read) in table {
        assert_eq!(map_transport(true, &error), on_write, "write {error:?}");
        assert_eq!(map_transport(false, &error), on_read, "read {error:?}");
    }
}

#[test]
fn timeout_policy_is_30s_plus_size_at_1mib_per_s_with_a_60s_floor() {
    let p = TimeoutPolicy::PRODUCTION;
    assert_eq!(p, TimeoutPolicy::default());
    assert_eq!(p.for_body(0), Duration::from_secs(60));
    assert_eq!(p.for_body(8 << 20), Duration::from_secs(60));
    assert_eq!(p.for_body(30 << 20), Duration::from_secs(60));
    assert_eq!(p.for_body(40 << 20), Duration::from_secs(70));
    assert_eq!(p.for_body(100 << 20), Duration::from_secs(130));
    assert_eq!(short().for_body(0), Duration::from_millis(400));
}

// ------------------------------------------------------------------ construction and secrets

#[test]
fn construction_validates_every_part_without_panicking() {
    let make =
        |endpoint: &str, bucket: &str, region: &str, key: &str, secret: &str, prefix: &str| {
            S3Store::new(
                endpoint,
                bucket,
                region,
                key,
                secret,
                prefix,
                TimeoutPolicy::PRODUCTION,
            )
            .map(|_| ())
        };
    let url = "http://127.0.0.1:9";
    assert_eq!(make(url, "b", "auto", "k", "s", ""), Ok(()));
    assert_eq!(make(url, "b", "auto", "k", "s", "a/b/"), Ok(()));
    assert_eq!(
        make(url, "b", "auto", "k", "s", "a"),
        Err(S3ConfigError::Prefix)
    );
    assert_eq!(
        make(url, "b", "auto", "k", "s", "/a/"),
        Err(S3ConfigError::Prefix)
    );
    assert_eq!(
        make(url, "b", "auto", "k", "s", "/"),
        Err(S3ConfigError::Prefix)
    );
    assert_eq!(
        make("not a url", "b", "auto", "k", "s", ""),
        Err(S3ConfigError::Endpoint)
    );
    assert_eq!(
        make("ftp://host", "b", "auto", "k", "s", ""),
        Err(S3ConfigError::Endpoint)
    );
    assert_eq!(
        make(url, "", "auto", "k", "s", ""),
        Err(S3ConfigError::Bucket)
    );
    assert_eq!(make(url, "b", "", "k", "s", ""), Err(S3ConfigError::Region));
    assert_eq!(
        make(url, "b", "auto", "", "s", ""),
        Err(S3ConfigError::Credentials)
    );
    assert_eq!(
        make(url, "b", "auto", "k", "", ""),
        Err(S3ConfigError::Credentials)
    );
}

#[test]
fn the_secret_and_the_access_key_never_appear_in_debug_or_error_text() -> R {
    let (fake, mut s) = rig("pre/")?;
    let mut texts = vec![format!("{s:?}"), format!("{s:#?}")];
    fake.queue("PUT", "k", Fault::Status(403, None));
    fake.queue("PUT", "k", Fault::ApplyThenDrop);
    let mut errors = vec![
        s.put_if_absent("k", body(b"1")),
        s.put_if_absent("k", body(b"1")),
        s.put_if_absent("k", body(b"1")).and(Ok(tag("\"x\"")?)),
    ];
    errors.push(s.put_if_absent("k", body(b"1")));
    errors.push(s.get("nothing").map(|g| g.etag));
    for result in errors {
        if let Err(error) = result {
            texts.push(format!("{error:?} / {error}"));
        }
    }
    for error in [
        StoreError::PreconditionFailed,
        StoreError::NotFound,
        StoreError::Offline,
        StoreError::Throttled(Some(3)),
        StoreError::Server,
        StoreError::Auth,
        StoreError::Ambiguous,
    ] {
        texts.push(format!("{error:?} / {error}"));
    }
    for error in [
        S3ConfigError::Endpoint,
        S3ConfigError::Bucket,
        S3ConfigError::Region,
        S3ConfigError::Credentials,
        S3ConfigError::Prefix,
    ] {
        texts.push(format!("{error:?} / {error}"));
    }
    assert!(texts.first().is_some_and(|t| t.contains("S3Store")));
    for text in &texts {
        assert!(!text.contains(SECRET), "{text}");
        assert!(!text.contains(KEY_ID), "{text}");
        assert!(!text.contains("X-Amz"), "{text}");
        assert!(!text.contains("http"), "{text}");
    }
    Ok(())
}

// ------------------------------------------------------------------ commit protocol

fn owner() -> R<Owner> {
    Ok(Owner::Session(SessionKey {
        archive: ArchiveId::try_from("a".to_owned()).map_err(e)?,
        machine: MachineId::try_from("m".to_owned()).map_err(e)?,
        agent: Agent::Claude,
        session: SessionId::try_from("s1".to_owned()).map_err(e)?,
    }))
}
fn ts() -> R<Timestamp> {
    Timestamp::try_from("2026-11-07T10:30:00Z".to_owned()).map_err(e)
}

/// A first commit of one source holding `content` as one piece (stored bytes are `content`
/// itself; the adapter does not look inside).
fn request(owner: &Owner, content: &[u8]) -> R<Commit> {
    let raw = manifest_hash(content);
    let len = u64::try_from(content.len()).map_err(e)?;
    let hash = String::from(raw);
    let piece: Piece = serde_json::from_value(serde_json::json!({
        "offset": Offset::new(0).get(), "len": len, "sha256": hash,
        "object": format!("objects/{hash}.zst"), "encoding": "zstd-3",
        "encoded_len": len, "encoded_sha256": String::from(manifest_hash(content)),
    }))
    .map_err(e)?;
    let write = SourceWrite {
        id: SourceId::try_from("src0".to_owned()).map_err(e)?,
        kind: SourceKind::ClaudeTranscript,
        path: "p/src0.jsonl".to_owned(),
        class: SourceClass::Append,
        epoch: EpochStamp {
            n: 0,
            writer: EpochWriter {
                version: "0.1.0".to_owned(),
                commit: "abc".to_owned(),
            },
            rules: None,
        },
        size: len,
        sha256: raw,
        pieces: vec![piece],
    };
    Ok(Commit {
        header: Header {
            owner: owner.clone(),
            captured_at: ts()?,
            writer: Writer {
                name: "memory".to_owned(),
                version: "0.1.0".to_owned(),
                commit: "abc".to_owned(),
            },
            launch: vec![],
        },
        changes: vec![Change::Epoch(write)],
        uploads: vec![Upload {
            key: object_key(&raw),
            body: Published::sim(content.to_vec()),
        }],
    })
}

#[test]
fn a_clean_commit_through_the_adapter_lands_as_committed() -> R {
    let (fake, mut s) = rig("rust-acceptance/c1/")?;
    let owner = owner()?;
    let end = commit(&mut s, None, request(&owner, b"line one\n")?, &mut |_| {}).map_err(e)?;
    let CommitEnd::Landed(landed) = end else {
        return Err(format!("expected Landed, got {end:?}"));
    };
    assert_eq!(landed.how(), LandedHow::Committed);
    let path = format!("/bkt/rust-acceptance/c1/{}", current_key(&owner));
    let (_, etag) = need(fake.object(&path), "current.json stored")?;
    assert_eq!(landed.etag().as_str(), etag);
    assert_eq!(fake.count("PUT"), 3); // piece, manifest, current.json
    assert_eq!(fake.count("GET"), 0);
    Ok(())
}

#[test]
fn a_lost_reply_on_current_json_ends_as_already_ours() -> R {
    let (fake, mut s) = rig("")?;
    let owner = owner()?;
    fake.queue("PUT", "current.json", Fault::ApplyThenDrop);
    let end = commit(&mut s, None, request(&owner, b"line one\n")?, &mut |_| {}).map_err(e)?;
    let CommitEnd::Landed(landed) = end else {
        return Err(format!("expected Landed, got {end:?}"));
    };
    assert_eq!(landed.how(), LandedHow::AlreadyOurs);
    let current_puts = fake
        .requests()
        .iter()
        .filter(|r| r.method == "PUT" && r.path.ends_with("current.json"))
        .count();
    assert_eq!(current_puts, 1, "current.json must never be resent");
    assert_eq!(fake.count("GET"), 1);
    Ok(())
}

#[test]
fn a_late_landing_of_an_earlier_attempt_is_adopted_through_known_ours() -> R {
    let (fake, mut s) = rig("")?;
    let owner = owner()?;
    let mut known = KnownOurs::new();
    // Capture A builds M1; its current.json PUT is refused and every verification GET fails.
    fake.queue("PUT", "current.json", Fault::Status(503, None));
    for _ in 0..4 {
        fake.queue("GET", "current.json", Fault::Status(500, None));
    }
    let a = commit_known(
        &mut s,
        None,
        request(&owner, b"line one\n")?,
        &mut known,
        &mut |_| {},
    );
    assert!(matches!(a, Err(Failure::LandingUnknown(_))), "{a:?}");
    assert_eq!(known.len(), 1);
    let m1 = need(known.keys().next().copied(), "m1")?;
    let seq = need(known.get(&m1), "built M1")?.position.seq;
    // M1's delayed current.json write lands, as another connection's would.
    let pointer = Current::new(m1, seq, ts()?).to_bytes().map_err(e)?;
    fake.seed(&format!("/bkt/{}", current_key(&owner)), &pointer);
    // Capture B builds a different manifest, is refused with 412 and finds M1 named.
    let b = commit_known(
        &mut s,
        None,
        request(&owner, b"line one\nline two\n")?,
        &mut known,
        &mut |_| {},
    )
    .map_err(e)?;
    let CommitEnd::Adopted(adopted) = b else {
        return Err(format!("expected Adopted, got {b:?}"));
    };
    assert_eq!(adopted.built().hash, m1);
    assert!(known.is_empty());
    Ok(())
}
