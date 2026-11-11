use hegel::generators as gs;
use hegel::TestCase;

// Append-only pieces must reassemble the file at every capture.
#[hegel::test]
fn pieces_concatenate(tc: TestCase) {
    let appends: Vec<Vec<u8>> = tc.draw(gs::vecs(gs::vecs(gs::integers::<u8>())));
    let mut file: Vec<u8> = Vec::new();
    let mut pieces: Vec<Vec<u8>> = Vec::new();
    for a in &appends {
        file.extend_from_slice(a);
        pieces.push(a.clone());
    }
    assert_eq!(pieces.concat(), file, "pieces must reassemble the file");
}

// Deliberately wrong coalescer: drops an empty append, so offsets drift. Hegel should shrink to a tiny example.
#[hegel::test]
fn coalescer_bug_is_found(tc: TestCase) {
    let appends: Vec<Vec<u8>> = tc.draw(gs::vecs(gs::vecs(gs::integers::<u8>())));
    let mut offset = 0usize;
    let mut recorded = 0usize;
    for a in &appends {
        offset += a.len();
        if a.len() > 1 { recorded += a.len(); }
    }
    assert_eq!(recorded, offset, "recorded offset must equal file offset");
}
