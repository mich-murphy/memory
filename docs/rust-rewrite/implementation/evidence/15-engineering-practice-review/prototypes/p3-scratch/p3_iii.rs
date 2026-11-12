use memory_core::privacy::{AdmittedSource, AuthorizedBody, RedactedBytes};
#[test] fn forge() {
    let _a = AuthorizedBody { bytes: vec![], digest: [0; 32], owner: String::new() };
    let _r = RedactedBytes { bytes: vec![], binding: [0; 32] };
    let _s = AdmittedSource { binding: [0; 32] };
}
