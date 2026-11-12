//! The one launch record shared by hint lines and manifest headers (DESIGN.md 6.3, 7.6).
//!
//! DESIGN.md 9.3 step 4 compares the launch digest a hint carries with the digests manifests
//! carry, so there is exactly one record type and one digest function. The digest is
//! domain-separated and length-prefixed, so no JSON encoding is involved and none can fail.
use crate::domain::Sha256;
use crate::privacy::SanitizedLaunch;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256 as Hasher};
use std::collections::BTreeMap;

/// Sanitized launch evidence: allowlisted argv and environment with a content digest.
///
/// Constructible only from [`SanitizedLaunch`]; fields are private. Deserialisation of our
/// own files is digest-checked by the readers (`digest_matches`).
/// ```compile_fail
/// let _ = memory_core::manifest::LaunchRecord {
///     digest: memory_core::domain::Sha256::from_bytes([0; 32]),
///     argv: vec![],
///     env: Default::default(),
/// };
/// ```
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct LaunchRecord {
    digest: Sha256,
    argv: Vec<String>,
    env: BTreeMap<String, String>,
}

fn put(hasher: &mut Hasher, bytes: &[u8]) {
    hasher.update(u64::try_from(bytes.len()).unwrap_or(u64::MAX).to_be_bytes());
    hasher.update(bytes);
}

fn launch_digest(argv: &[String], env: &BTreeMap<String, String>) -> Sha256 {
    let mut hasher = Hasher::new();
    hasher.update(b"memory.launch/1\0argv");
    for arg in argv {
        put(&mut hasher, arg.as_bytes());
    }
    hasher.update(b"env");
    for (key, value) in env {
        put(&mut hasher, key.as_bytes());
        put(&mut hasher, value.as_bytes());
    }
    Sha256::from_bytes(hasher.finalize().into())
}

impl LaunchRecord {
    /// Build from sanitized evidence only; the digest covers argv and env.
    pub fn from_sanitized(launch: &SanitizedLaunch) -> Self {
        let argv = launch.argv().to_vec();
        let env = launch.env().clone();
        Self {
            digest: launch_digest(&argv, &env),
            argv,
            env,
        }
    }
    /// Content digest; the key that deduplicates records on a manifest header.
    pub fn digest(&self) -> Sha256 {
        self.digest
    }
    /// Allowlisted argv.
    pub fn argv(&self) -> &[String] {
        &self.argv
    }
    /// Allowlisted environment.
    pub fn env(&self) -> &BTreeMap<String, String> {
        &self.env
    }
    /// Whether the stored digest matches the stored content.
    pub fn digest_matches(&self) -> bool {
        launch_digest(&self.argv, &self.env) == self.digest
    }
}
