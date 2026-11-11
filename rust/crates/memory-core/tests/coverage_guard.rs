//! Negative controls for the shared coverage guard: the guard must fail, naming the shape,
//! whenever any one required shape was never generated (mutant: `guard` ignores one shape,
//! or passes when nothing is required).
mod common;
use common::Shapes;

const NAMES: [&str; 4] = ["alpha", "beta", "gamma", "delta"];

#[test]
fn guard_names_every_missing_shape_and_only_passes_when_all_were_seen() {
    let none = Shapes::new(NAMES);
    let err = none.guard(4).err().unwrap_or_default();
    for name in NAMES {
        assert!(err.contains(name), "{err}");
    }
    assert!(Shapes::new(NAMES).guard(0).is_err());
    for absent in 0..NAMES.len() {
        let some = Shapes::new(NAMES);
        for shape in (0..NAMES.len()).filter(|shape| *shape != absent) {
            some.bump(shape);
        }
        let err = some.guard(4).err().unwrap_or_default();
        assert!(NAMES.get(absent).is_some_and(|name| err.contains(name)));
        assert_eq!(
            err.matches(',').count(),
            0,
            "only one shape is missing: {err}"
        );
    }
    let all = Shapes::new(NAMES);
    for shape in 0..NAMES.len() {
        all.bump(shape);
    }
    assert!(all.guard(4).is_ok());
    // Only the first `required` shapes are demanded.
    let first_two = Shapes::new(NAMES);
    first_two.bump(0);
    first_two.bump(1);
    assert!(first_two.guard(2).is_ok());
    assert!(first_two.guard(3).is_err());
}
