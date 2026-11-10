//! The shared in-memory store is the oracle for the writer's conditional-write handling, so its
//! own semantics are pinned here directly, against the conditional-write behaviour measured on
//! R2 (P2 `r2-os.jsonl`): a test that relied on a looser fake would prove nothing.
#[path = "common/store.rs"]
mod mem_store;

use mem_store::{EtagModel, MemStore};
use memory::environment::{Published, Store, StoreError};

type R<T = ()> = Result<T, String>;
fn e<E: std::fmt::Debug>(err: E) -> String {
    format!("{err:?}")
}

fn body(text: &str) -> Published {
    Published::sim(text.as_bytes().to_vec())
}
fn store_with(model: EtagModel) -> MemStore {
    MemStore {
        model,
        ..MemStore::default()
    }
}

#[test]
fn if_none_match_on_an_existing_key_is_412_and_changes_nothing() -> R {
    let mut s = MemStore::default();
    s.put_if_absent("k", body("first")).map_err(e)?;
    assert_eq!(
        s.put_if_absent("k", body("second")),
        Err(StoreError::PreconditionFailed)
    );
    assert_eq!(s.get("k").map_err(e)?.body, b"first");
    assert_eq!(s.version, 1);
    Ok(())
}

#[test]
fn if_match_with_a_stale_etag_is_412_and_the_current_one_lands() -> R {
    let mut s = MemStore::default();
    let first = s.put_if_absent("k", body("a")).map_err(e)?;
    let second = s.put_if_match("k", &first, body("b")).map_err(e)?;
    assert_ne!(first, second);
    assert_eq!(
        s.put_if_match("k", &first, body("c")),
        Err(StoreError::PreconditionFailed)
    );
    assert_eq!(s.get("k").map_err(e)?.body, b"b");
    assert_eq!(s.get("k").map_err(e)?.etag, second);
    Ok(())
}

#[test]
fn if_match_on_a_missing_key_is_412_not_404() -> R {
    let mut s = MemStore::default();
    let etag = MemStore::tag(1).map_err(e)?;
    assert_eq!(
        s.put_if_match("absent", &etag, body("x")),
        Err(StoreError::PreconditionFailed)
    );
    assert!(s.objects.is_empty());
    assert_eq!(s.get("absent").map(|_| ()), Err(StoreError::NotFound));
    Ok(())
}

#[test]
fn an_identical_rewrite_changes_the_etag_per_write_and_keeps_it_by_content() -> R {
    for (model, same) in [(EtagModel::PerWrite, false), (EtagModel::Content, true)] {
        let mut s = store_with(model);
        let first = s.put_if_absent("k", body("same")).map_err(e)?;
        let again = s.put_if_match("k", &first, body("same")).map_err(e)?;
        assert_eq!(first == again, same, "{model:?}");
        // The rewrite landed either way, and a different body always gets a different ETag.
        assert_eq!(s.version, 2, "{model:?}");
        let other = s.put_if_match("k", &again, body("other")).map_err(e)?;
        assert_ne!(again, other, "{model:?}");
    }
    Ok(())
}

#[test]
fn the_model_is_picked_from_a_generated_bool() {
    assert_eq!(EtagModel::from(true), EtagModel::Content);
    assert_eq!(EtagModel::from(false), EtagModel::PerWrite);
}

/// The live R2 probe of 2026-11-11 (50 PUTs): the default model is content-derived, a fence
/// taken before another writer's identical rewrite still holds, and one taken before a rewrite
/// with different bytes is refused.
#[test]
fn the_default_model_is_the_measured_r2_one() -> R {
    assert_eq!(EtagModel::default(), EtagModel::Content);
    let mut s = MemStore::default();
    let held = s.put_if_absent("k", body("v")).map_err(e)?;
    let rewritten = s.put_if_match("k", &held, body("v")).map_err(e)?;
    assert_eq!(held, rewritten);
    let held = s.put_if_match("k", &held, body("w")).map_err(e)?;
    s.put_if_match("k", &held, body("x")).map_err(e)?;
    assert_eq!(
        s.put_if_match("k", &held, body("y")),
        Err(StoreError::PreconditionFailed)
    );
    Ok(())
}
