//! Runtime shell: capture, commit, local state and the adapters behind the environment seams
//! (`realfs`, `s3`). The daemon's run loop is not wired yet.
pub mod assemble;
pub mod capture;
pub mod clock;
pub mod commit;
pub mod config;
pub mod daemon;
pub mod environment;
pub mod hook;
pub mod realfs;
pub mod resolve;
pub mod roots;
pub mod s3;
pub mod state;
pub mod token;
pub mod watch;
