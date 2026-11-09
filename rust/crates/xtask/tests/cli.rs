//! Check schema command dispatch and explicit CLI failure.
use std::{io, process::Command};

#[test]
fn schema_cli_dispatches_and_rejects_unknown_commands() -> io::Result<()> {
    let executable = env!("CARGO_BIN_EXE_xtask");
    for args in [vec!["schemas"], vec!["schemas", "--check"]] {
        assert!(
            Command::new(executable)
                .args(args)
                .output()?
                .status
                .success()
        );
    }
    for args in [vec![], vec!["unknown"], vec!["schemas", "--unknown"]] {
        let output = Command::new(executable).args(args).output()?;
        assert_eq!(output.status.code(), Some(1));
        assert!(output.stdout.is_empty());
    }
    Ok(())
}
