//! Harness checks, not the later capture correctness properties.
use hegel::{TestCase, generators as gs};
use memory_core::slice::{RangeError, bytes};
use proptest::prelude::*;

#[test]
fn checked_ranges_cover_edges() {
    assert_eq!(bytes(b"abc", 0..3), Ok(b"abc".as_slice()));
    assert_eq!(bytes(b"abc", 3..3), Ok(b"".as_slice()));
    assert_eq!(bytes(b"abc", 0..4), Err(RangeError));
    let start = 2;
    let end = 1;
    assert_eq!(bytes(b"abc", start..end), Err(RangeError));
}

fn range_oracle(data: &[u8], start: usize, end: usize) -> Option<Vec<u8>> {
    if start > end || end > data.len() {
        None
    } else {
        Some(data.iter().copied().skip(start).take(end - start).collect())
    }
}

#[hegel::test]
fn hegel_checked_ranges_match_independent_iterator_oracle(tc: TestCase) {
    let data: Vec<u8> = tc.draw(gs::vecs(gs::integers::<u8>()));
    let start = usize::from(tc.draw(gs::integers::<u8>()));
    let end = usize::from(tc.draw(gs::integers::<u8>()));
    assert_eq!(
        bytes(&data, start..end).ok().map(<[u8]>::to_vec),
        range_oracle(&data, start, end)
    );
}

proptest! {
    #[test]
    fn proptest_checked_ranges_match_independent_iterator_oracle(
        data in prop::collection::vec(any::<u8>(), 0..512), start in 0usize..600, end in 0usize..600
    ) {
        prop_assert_eq!(bytes(&data, start..end).ok().map(<[u8]>::to_vec), range_oracle(&data, start, end));
    }
}
