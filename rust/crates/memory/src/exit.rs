//! Exit codes and store-failure output shared by `export`, `owners` and `install`.
//!
//! A store failure prints `store cause=<code>` (brief 52) and exits 75 (transient, retry),
//! 77 (auth) or 1 (see [`store_exit`]). No line carries a key, the endpoint, the bucket or a
//! credential.
use crate::environment::StoreError;

/// Exit code: usage error or unparsable owner.
pub const EXIT_USAGE: u8 = 64;
/// Exit code: the owner has no `current.json`.
pub const EXIT_ABSENT: u8 = 66;
/// Exit code: any other failure.
pub const EXIT_FAILED: u8 = 1;

/// A fetch error the commands can classify. `()` (test fetchers) carries no store error.
pub trait StoreFault {
    /// The store error behind this fetch failure, if any.
    fn store_error(&self) -> Option<StoreError>;
}
impl StoreFault for StoreError {
    fn store_error(&self) -> Option<StoreError> {
        Some(*self)
    }
}
impl StoreFault for () {
    fn store_error(&self) -> Option<StoreError> {
        None
    }
}

/// Exit code: a transient store failure (EX_TEMPFAIL); retrying later may succeed.
pub const EXIT_TEMPFAIL: u8 = 75;
/// Exit code: the store refused the credential (EX_NOPERM); retrying cannot help.
pub const EXIT_NOPERM: u8 = 77;

/// Closed line for a store failure: `store cause=<code>` (`store` alone when unknown).
#[must_use]
pub fn store_line(error: Option<StoreError>) -> String {
    match error {
        Some(e) => format!("store cause={}", e.code()),
        None => "store".to_owned(),
    }
}

/// Exit code of a store failure (brief 52): 77 for auth, 75 for a transient one (the commands
/// only read, so a retry is always safe), else 1. Exhaustive: a new `StoreError` variant must
/// choose its exit here.
#[must_use]
pub const fn store_exit(error: Option<StoreError>) -> u8 {
    match error {
        None => EXIT_FAILED,
        Some(StoreError::Auth) => EXIT_NOPERM,
        Some(
            StoreError::Offline(_)
            | StoreError::Ambiguous(_)
            | StoreError::Throttled(_)
            | StoreError::Server,
        ) => EXIT_TEMPFAIL,
        Some(StoreError::PreconditionFailed | StoreError::NotFound) => EXIT_FAILED,
    }
}
