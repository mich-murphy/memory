//! Diagnostic check that the scaffold binary refuses capture.
#[test]
fn scaffold_binary_refuses_capture_instead_of_success() -> std::io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    // Isolated state root: a test must never resolve or create the host's real state dir.
    let base = std::env::temp_dir().join(format!("memory-harness-{}", std::process::id()));
    let root = base.join("state");
    std::fs::create_dir_all(&root)?;
    std::fs::set_permissions(&base, std::fs::Permissions::from_mode(0o700))?;
    std::fs::set_permissions(&root, std::fs::Permissions::from_mode(0o700))?;
    let token = base.join("token");
    std::fs::write(&token, "x")?;
    std::fs::set_permissions(&token, std::fs::Permissions::from_mode(0o600))?;
    std::fs::write(
        root.join("config.toml"),
        format!(
            "archive_id = \"a\"\nmachine_id = \"m\"\nbucket = \"bucket-1\"\ntoken_path = \"{}\"\n",
            token.display()
        ),
    )?;
    let mut cmd = std::process::Command::new(env!("CARGO_BIN_EXE_memory"));
    cmd.arg("daemon").env_clear().env("MEMORY_STATE_DIR", &root);
    if let Some(profile) = std::env::var_os("LLVM_PROFILE_FILE") {
        cmd.env("LLVM_PROFILE_FILE", profile);
    }
    let output = cmd.output();
    let _ = std::fs::remove_dir_all(&base);
    let output = output?;
    // Startup succeeds, then the binary refuses: no adapters, never a simulated capture.
    assert_eq!(output.status.code(), Some(78));
    assert!(output.stdout.is_empty());
    assert!(output.stderr.is_empty());
    Ok(())
}
