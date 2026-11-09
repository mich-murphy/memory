//! Pure known-file privacy policy. Filesystem facts are supplied by the shell;
//! this module neither resolves paths nor establishes an atomic source snapshot.
//!
//! Capabilities have no public constructors or deserializers.
//! ```compile_fail
//! use memory_core::privacy::RedactedBytes;
//! let _ = RedactedBytes { bytes: vec![], binding: [0; 32] };
//! ```
//! ```compile_fail
//! use memory_core::privacy::AuthorizedBody;
//! let _ = AuthorizedBody { bytes: vec![], digest: [0; 32], owner: String::new() };
//! ```
//! ```compile_fail
//! use memory_core::privacy::{AdmittedSource, ResolvedSourceFacts};
//! fn bypass(facts: ResolvedSourceFacts) -> AdmittedSource {
//!     AdmittedSource { facts, binding: [0; 32] }
//! }
//! ```
//! ```compile_fail
//! let _: memory_core::privacy::RedactedBytes = serde_json::from_str("{}").unwrap();
//! ```
//! ```compile_fail
//! let _: memory_core::privacy::AuthorizedBody = serde_json::from_str("{}").unwrap();
//! ```

mod document;
mod launch;
mod names;
mod text;

pub use launch::{SanitizedLaunch, sanitize_launch};
pub use names::{basename, is_credential, is_secret_name};

use sha2::{Digest, Sha256};

/// Explicit semantic version; bump for parser, reference, copy or guard changes.
pub const TRANSFORM_VERSION: &str = "memory.privacy/2";
/// Whole-document protected input limit. Never falls back to raw on overflow.
pub const MAX_PROTECTED_BYTES: usize = 8 * 1024 * 1024;

/// Fixed, content-free withholding reasons.
#[derive(Clone, Copy, Debug, PartialEq, Eq, thiserror::Error)]
pub enum Reason {
    /// Known credential identity.
    #[error("credential")]
    Credential,
    /// Collector config, token or state identity.
    #[error("collector_state")]
    CollectorState,
    /// No admitted kind.
    #[error("unknown_kind")]
    UnknownKind,
    /// No unambiguous owner.
    #[error("unknown_owner")]
    UnknownOwner,
    /// Invalid protected document.
    #[error("malformed_protected")]
    MalformedProtected,
    /// Transform cannot safely complete.
    #[error("scrub_failed")]
    ScrubFailed,
    /// Literal secret assignment in a guarded text asset.
    #[error("secret_in_text")]
    SecretInText,
    /// Whole protected document exceeds the bound.
    #[error("source_limit")]
    SourceLimit,
    /// Capability and current context do not match.
    #[error("context_changed")]
    ContextChanged,
}

/// Unknown policy is never an allow.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum UnknownReason {
    /// Operator policy invalid.
    ConfigInvalid,
    /// Root facts unavailable.
    RootUnresolvable,
    /// Path identities incomplete or unresolved.
    PathUnresolvable,
    /// Rules missing or unexpected.
    RulesUnavailable,
}

/// Minimal independent source profiles; locator integration remains parent-owned.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Profile {
    /// Safe transcript bytes, never text-DLP scanned.
    Transcript,
    /// Known configuration; recognized basenames always select structural rules.
    Config,
    /// Known small non-transcript assets subject to assignment guard.
    TextAsset,
    /// Other explicitly known safe source.
    OtherKnown,
    /// Unknown kind cannot take a raw route.
    Unknown,
}

/// Observations, not authority: the shell must actually validate all these facts.
#[derive(Clone, Debug)]
pub struct ResolvedSourceFacts {
    /// Every logical, intermediate and final path spelling.
    pub identities: Vec<String>,
    /// Final device/inode-like identity, as an opaque shell-assigned identifier.
    pub file_identity: Option<u128>,
    /// Directory ancestry identities used to recognize collector state aliases.
    pub ancestor_identities: Vec<u128>,
    /// All links/components resolved successfully (including cycle/255-link checks).
    pub path_resolved: bool,
    /// Root resolved and admitted by the shell.
    pub root_resolved: bool,
    /// Validated owner coordinate, absent if unknown/ambiguous.
    pub owner: Option<String>,
    /// Located known source profile.
    pub profile: Profile,
}

/// Immutable policy observation supplied by the shell, not deserialized as authority.
#[derive(Clone, Debug)]
pub struct PolicyContext {
    /// Invalid/missing config is fail closed.
    pub valid: bool,
    /// Monotonic policy revision used to bind a capture.
    pub revision: String,
    /// Expected current rules digest, absent if unavailable.
    pub rules: Option<[u8; 32]>,
    /// Resolved identity spellings of collector config/token/state.
    pub collector_paths: Vec<String>,
    /// Final file/directory identities of collector config/token/state.
    pub collector_identities: Vec<u128>,
}

/// Admission is not permission to upload arbitrary bytes.
pub enum Admission {
    /// Bound to source facts and context.
    Allow(AdmittedSource),
    /// Known denial.
    Deny(Reason),
    /// Missing policy evidence.
    Unknown(UnknownReason),
}

/// Opaque admitted source; only admission can construct it.
pub struct AdmittedSource {
    facts: ResolvedSourceFacts,
    binding: [u8; 32],
}

/// Opaque transformed bytes; only transformation can construct them.
pub struct RedactedBytes {
    bytes: Vec<u8>,
    binding: [u8; 32],
}

/// Opaque final body; no serde, Default, arbitrary From or public fields.
pub struct AuthorizedBody {
    bytes: Vec<u8>,
    digest: [u8; 32],
    owner: String,
}

impl AuthorizedBody {
    /// Publishable uncompressed bytes. Compression must preserve this provenance.
    pub fn bytes(&self) -> &[u8] {
        &self.bytes
    }
    /// Digest of publishable bytes, never pre-transform secret-bearing input.
    pub fn digest(&self) -> [u8; 32] {
        self.digest
    }
    /// Bound owner coordinate.
    pub fn owner(&self) -> &str {
        &self.owner
    }
}

/// Result of final policy recheck.
pub enum Authorization {
    /// The exact transformed body, bound to source and current policy.
    Allow(AuthorizedBody),
    /// Known denial.
    Deny(Reason),
    /// Missing policy evidence.
    Unknown(UnknownReason),
}

/// Semantic version and canonical rule table digest.
pub fn rules_digest() -> [u8; 32] {
    let mut h = Sha256::new();
    h.update(TRANSFORM_VERSION.as_bytes());
    document::digest_rules(&mut h);
    h.finalize().into()
}

fn binding(context: &PolicyContext, facts: &ResolvedSourceFacts) -> [u8; 32] {
    fn field(h: &mut Sha256, value: &[u8]) {
        h.update(value.len().to_le_bytes());
        h.update(value);
    }
    let mut h = Sha256::new();
    h.update([u8::from(context.valid)]);
    field(&mut h, context.revision.as_bytes());
    match context.rules {
        Some(rules) => {
            h.update([1]);
            h.update(rules);
        }
        None => h.update([0]),
    }
    for paths in [&context.collector_paths, &facts.identities] {
        h.update(paths.len().to_le_bytes());
        for path in paths {
            field(&mut h, path.as_bytes());
        }
    }
    for ids in [&context.collector_identities, &facts.ancestor_identities] {
        h.update(ids.len().to_le_bytes());
        for id in ids {
            h.update(id.to_le_bytes());
        }
    }
    match facts.file_identity {
        Some(id) => {
            h.update([1]);
            h.update(id.to_le_bytes());
        }
        None => h.update([0]),
    }
    h.update([u8::from(facts.path_resolved), u8::from(facts.root_resolved)]);
    match &facts.owner {
        Some(owner) => {
            h.update([1]);
            field(&mut h, owner.as_bytes());
        }
        None => h.update([0]),
    }
    h.update([match facts.profile {
        Profile::Transcript => 0,
        Profile::Config => 1,
        Profile::TextAsset => 2,
        Profile::OtherKnown => 3,
        Profile::Unknown => 4,
    }]);
    h.finalize().into()
}

/// Admit resolved observations without reading bytes.
pub fn admit(context: &PolicyContext, facts: ResolvedSourceFacts) -> Admission {
    if !context.valid {
        return Admission::Unknown(UnknownReason::ConfigInvalid);
    }
    if !facts.root_resolved {
        return Admission::Unknown(UnknownReason::RootUnresolvable);
    }
    if !facts.path_resolved || facts.identities.is_empty() || facts.file_identity.is_none() {
        return Admission::Unknown(UnknownReason::PathUnresolvable);
    }
    if (matches!(facts.profile, Profile::Config | Profile::TextAsset)
        || facts.identities.iter().any(|p| document::recognizes(p)))
        && context.rules != Some(rules_digest())
    {
        return Admission::Unknown(UnknownReason::RulesUnavailable);
    }
    for path in &facts.identities {
        if is_credential(path) {
            return Admission::Deny(Reason::Credential);
        }
        if context
            .collector_paths
            .iter()
            .any(|p| names::collector_match(path, p))
        {
            return Admission::Deny(Reason::CollectorState);
        }
    }
    if context
        .collector_identities
        .iter()
        .any(|id| facts.file_identity == Some(*id) || facts.ancestor_identities.contains(id))
    {
        return Admission::Deny(Reason::CollectorState);
    }
    if facts.profile == Profile::Unknown {
        return Admission::Deny(Reason::UnknownKind);
    }
    if facts.owner.as_ref().is_none_or(String::is_empty) {
        return Admission::Deny(Reason::UnknownOwner);
    }
    Admission::Allow(AdmittedSource {
        binding: binding(context, &facts),
        facts,
    })
}

/// Transform a complete protected document before hashing or splitting it.
///
/// # Errors
/// Returns only fixed reasons; parser diagnostics and source values never escape.
pub fn transform(source: &AdmittedSource, bytes: &[u8]) -> Result<RedactedBytes, Reason> {
    let protected = source
        .facts
        .identities
        .iter()
        .any(|name| document::recognizes(name));
    if protected && bytes.len() > MAX_PROTECTED_BYTES {
        return Err(Reason::SourceLimit);
    }
    let mut out = bytes.to_vec();
    for name in &source.facts.identities {
        if document::recognizes(name) {
            out = document::scrub(name, &out)?;
        }
    }
    if matches!(source.facts.profile, Profile::Config | Profile::TextAsset) {
        text::check(&source.facts.identities, &out)?;
    }
    Ok(RedactedBytes {
        bytes: out,
        binding: source.binding,
    })
}

/// Recheck policy and bind the exact transformed bytes to their source.
pub fn authorize(
    source: AdmittedSource,
    redacted: RedactedBytes,
    context: &PolicyContext,
) -> Authorization {
    if redacted.binding != source.binding || source.binding != binding(context, &source.facts) {
        return Authorization::Deny(Reason::ContextChanged);
    }
    match admit(context, source.facts) {
        Admission::Allow(admitted) => Authorization::Allow(AuthorizedBody {
            digest: Sha256::digest(&redacted.bytes).into(),
            bytes: redacted.bytes,
            owner: admitted.facts.owner.unwrap_or_default(),
        }),
        Admission::Deny(reason) => Authorization::Deny(reason),
        Admission::Unknown(reason) => Authorization::Unknown(reason),
    }
}
