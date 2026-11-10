//! Check schema command dispatch and explicit CLI failure.
//!
//! No test here runs `xtask schemas` in write mode: that rewrites the tracked `schemas/` files
//! and would let a stale schema pass the later `xtask schemas --check` gate. Write-mode dispatch
//! is covered by the in-crate test against a scratch directory.
use std::{io, process::Command};

#[test]
fn schema_cli_check_dispatches_and_rejects_unknown_commands() -> io::Result<()> {
    let executable = env!("CARGO_BIN_EXE_xtask");
    assert!(
        Command::new(executable)
            .args(["schemas", "--check"])
            .output()?
            .status
            .success()
    );
    for args in [vec![], vec!["unknown"], vec!["schemas", "--unknown"]] {
        let output = Command::new(executable).args(args).output()?;
        assert_eq!(output.status.code(), Some(1));
        assert!(output.stdout.is_empty());
    }
    Ok(())
}
