//! Coverage guard shared by the generated-property tests (DESIGN.md section 15).
//!
//! A property that never generated a required shape proves nothing about it. Each property
//! owns a `static Shapes`, bumps the shape it just exercised and calls [`Shapes::guard`]
//! after its run. The guard itself is tested once, in `coverage_guard.rs`.
use std::sync::atomic::{AtomicU64, Ordering};

/// Counters of generated case shapes, in a fixed order.
pub struct Shapes<const N: usize> {
    names: [&'static str; N],
    counts: [AtomicU64; N],
}

impl<const N: usize> Shapes<N> {
    pub const fn new(names: [&'static str; N]) -> Self {
        Self {
            names,
            counts: [const { AtomicU64::new(0) }; N],
        }
    }

    /// Record that shape number `shape` was generated.
    pub fn bump(&self, shape: usize) {
        if let Some(count) = self.counts.get(shape) {
            count.fetch_add(1, Ordering::Relaxed);
        }
    }

    /// Fails, naming every missing shape, unless each of the first `required` shapes was
    /// generated at least once. Requiring zero shapes is itself a failure.
    pub fn guard(&self, required: usize) -> Result<(), String> {
        let missing: Vec<&str> = self
            .names
            .iter()
            .zip(&self.counts)
            .take(required)
            .filter(|(_, count)| count.load(Ordering::Relaxed) == 0)
            .map(|(name, _)| *name)
            .collect();
        if missing.is_empty() && required > 0 {
            Ok(())
        } else {
            Err(format!(
                "coverage guard: generator never produced {missing:?}"
            ))
        }
    }
}
