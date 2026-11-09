//! Synchronous, I/O-free capture decisions and protected-source transformations.
//!
//! Runtime adapters must supply validated observations; this crate performs no I/O.
pub mod coalescer;
pub mod coordinates;
pub mod disposition;
pub mod domain;
pub mod hint;
pub mod limits;
pub mod locator;
pub mod manifest;
pub mod planner;
pub mod privacy;
pub mod slice;
