//! Hint retention predicate (DESIGN.md 9.1): the 7/8 day boundary and the facts.
use memory_core::hint::{RetentionFacts, day_number, past_retention, removable};

const T: i64 = 20_763 * 86_400 + 3_600; // 2026-11-06 01:00 UTC

fn whole() -> RetentionFacts {
    RetentionFacts {
        whole: true,
        offset: 10,
        next_offset: 10,
        len: 10,
        pending_bytes: 0,
    }
}

#[test]
fn day_boundary_table() {
    let cases: [(&str, i64, bool); 13] = [
        ("2026-10-29", T, true),
        ("2026-10-30", T, false),
        ("2026-10-31", T, false),
        ("2026-11-05", T, false),
        ("2026-11-06", T, false),
        ("2026-11-07", T, false),
        ("2027-01-01", T, false),
        ("2026-10-29", 20_763 * 86_400 - 1, false),
        ("2026-10-28", 20_763 * 86_400 - 1, true),
        ("2026-13-01", T, false),
        ("2026-02-30", T, false),
        ("2026-00-10", T, false),
        ("2026-1a-10", T, false),
    ];
    for (day, wall, want) in cases {
        assert_eq!(past_retention(day, wall), want, "{day} at {wall}");
    }
    assert!(!past_retention("1969-01-01", -1));
    assert!(!past_retention("1960-01-01", i64::MIN));
    assert_eq!(day_number("1970-01-01"), Some(0));
    assert_eq!(day_number("2024-02-29"), Some(19_782));
    assert_eq!(day_number("2023-02-29"), None);
    assert_eq!(day_number("1900-02-29"), None);
    assert_eq!(day_number("2000-02-29"), Some(11_016));
}

#[test]
fn facts_table() {
    let old = "2026-10-29";
    assert!(removable(old, T, &whole()));
    assert!(!removable("2026-10-30", T, &whole()), "7 days kept");
    let cut = RetentionFacts {
        whole: false,
        ..whole()
    };
    assert!(!removable(old, T, &cut), "cut read");
    let torn = RetentionFacts {
        pending_bytes: 3,
        len: 13,
        ..whole()
    };
    assert!(!removable(old, T, &torn), "torn tail");
    // Inconsistent facts: a torn tail alone must keep the file, whatever the lengths say.
    let torn_alone = RetentionFacts {
        pending_bytes: 3,
        ..whole()
    };
    assert!(!removable(old, T, &torn_alone), "pending bytes alone");
    let behind = RetentionFacts {
        offset: 4,
        ..whole()
    };
    assert!(!removable(old, T, &behind), "offset behind");
    let long = RetentionFacts { len: 11, ..whole() };
    assert!(!removable(old, T, &long), "bytes after the last line");
}
