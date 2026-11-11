//! In-memory object store with the conditional-write semantics measured on R2 (P2
//! `r2-os.jsonl`), shared by the integration tests that need a store.
//!
//! Fault injection is not built in. A test that needs scripted faults wraps a `MemStore` and
//! intercepts the three `Store` calls around the inner one (see `Ghost` in `daemon.rs`, the
//! `FakeStore` in `commit_protocol.rs` and `capture_property.rs`): the decorator is the single
//! mechanism, and the R2 rules below stay in one place.
//!
//! Every item here must be used by every test crate that includes this file: the workspace
//! denies `#[allow]` and fails on an unfulfilled `#[expect]`, so an item only some crates use
//! would break the lint gate. That is why the module is split per concern and included
//! with `#[path]` rather than as one `common/mod.rs`.
use memory::capture::Published;
use memory::environment::{Store, StoreError, Stored};
use memory_core::domain::ETag;
use std::collections::BTreeMap;

/// How the fake derives an ETag.
///
/// Measured on R2 (live probe, 2026-11-11, 50 PUTs): the ETag is the hex MD5 of the body on
/// every single-part PUT, an identical rewrite keeps it, an `If-Match` PUT fenced on the ETag
/// from before another writer's identical-bytes rewrite succeeds, and one fenced before a
/// rewrite with different bytes is refused with 412. [`EtagModel::Content`] is therefore the
/// default. [`EtagModel::PerWrite`] is a deliberately stricter model for tests that need a
/// rewrite of identical bytes to look like a change (an ETag churn, a stale fence).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum EtagModel {
    /// The ETag is a function of the body: an identical rewrite keeps it (R2).
    #[default]
    Content,
    /// Every write gets a new ETag, even an identical rewrite (stricter than R2).
    PerWrite,
}

/// `true` selects [`EtagModel::Content`]; this is how generated cases pick the model.
impl From<bool> for EtagModel {
    fn from(content: bool) -> Self {
        if content {
            Self::Content
        } else {
            Self::PerWrite
        }
    }
}

fn content_token(body: &[u8]) -> u64 {
    body.iter().fold(0xcbf2_9ce4_8422_2325_u64, |h, b| {
        (h ^ u64::from(*b)).wrapping_mul(0x0100_0000_01b3)
    })
}

/// Objects by key, each with its body and ETag token; `version` counts landed writes.
#[derive(Default)]
pub struct MemStore {
    /// Body and ETag token per key.
    pub objects: BTreeMap<String, (Vec<u8>, u64)>,
    /// Count of landed writes.
    pub version: u64,
    /// How a write derives its token.
    pub model: EtagModel,
}

impl MemStore {
    /// The ETag a token reads as.
    ///
    /// # Errors
    /// [`StoreError::Server`] if the token cannot form an ETag (never for these tokens).
    pub fn tag(token: u64) -> Result<ETag, StoreError> {
        ETag::try_from(format!("\"v{token}\"")).map_err(|_| StoreError::Server)
    }

    /// Land `body` at `key` unconditionally; returns the new ETag token.
    pub fn write(&mut self, key: &str, body: Vec<u8>) -> u64 {
        self.version += 1;
        let token = match self.model {
            EtagModel::PerWrite => self.version,
            EtagModel::Content => content_token(&body),
        };
        self.objects.insert(key.to_owned(), (body, token));
        token
    }

    /// Whether a write fenced by `fence` (`None`: `If-None-Match: *`) may land, and the token
    /// currently stored at `key`.
    ///
    /// # Errors
    /// [`StoreError::PreconditionFailed`] for `If-None-Match` on an existing key and for
    /// `If-Match` with a stale ETag or on a missing key (412, not 404: measured on R2).
    pub fn check(&self, key: &str, fence: Option<&ETag>) -> Result<Option<u64>, StoreError> {
        let existing = self.objects.get(key).map(|(_, token)| *token);
        match (fence, existing) {
            (None, None) => Ok(None),
            (Some(want), Some(token)) if &Self::tag(token)? == want => Ok(Some(token)),
            _ => Err(StoreError::PreconditionFailed),
        }
    }

    fn put(
        &mut self,
        key: &str,
        body: &Published,
        fence: Option<&ETag>,
    ) -> Result<ETag, StoreError> {
        self.check(key, fence)?;
        Self::tag(self.write(key, body.as_ref().to_vec()))
    }
}

impl Store for MemStore {
    fn put_if_absent(&mut self, key: &str, body: Published) -> Result<ETag, StoreError> {
        self.put(key, &body, None)
    }
    fn put_if_match(
        &mut self,
        key: &str,
        etag: &ETag,
        body: Published,
    ) -> Result<ETag, StoreError> {
        self.put(key, &body, Some(etag))
    }
    fn get(&mut self, key: &str) -> Result<Stored, StoreError> {
        let (body, token) = self.objects.get(key).ok_or(StoreError::NotFound)?;
        Ok(Stored {
            body: body.clone(),
            etag: Self::tag(*token)?,
        })
    }
}
