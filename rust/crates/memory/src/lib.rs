//! Runtime shell: capture, commit, local state and the adapters behind the environment seams
//! (`realfs`, `s3`). `run` is the daemon's run loop: startup delay, a safety reconcile every
//! 15 minutes, stop on SIGTERM or SIGINT.
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
pub mod restore;
pub mod roots;
pub mod run;
pub mod s3;
pub mod state;
pub mod token;
pub mod watch;
