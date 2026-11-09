//! Repository-local development commands.
use std::{
    env,
    error::Error,
    fs,
    path::{Path, PathBuf},
    process::ExitCode,
};

fn schemas(check: bool) -> Result<(), Box<dyn Error>> {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..");
    schemas_at(&root, check)
}

/// Every committed wire schema: file name and generated JSON Schema.
fn exported() -> Vec<(&'static str, schemars::Schema)> {
    vec![
        (
            "range-error.json",
            schemars::schema_for!(memory_core::slice::RangeError),
        ),
        (
            "memory.manifest.1.json",
            schemars::schema_for!(memory_core::manifest::Manifest),
        ),
        (
            "memory.current.1.json",
            schemars::schema_for!(memory_core::manifest::Current),
        ),
        (
            "memory.hint.1.json",
            schemars::schema_for!(memory_core::hint::HintRecord),
        ),
    ]
}

fn schemas_at(root: &Path, check: bool) -> Result<(), Box<dyn Error>> {
    for (name, schema) in exported() {
        let path = root.join("schemas").join(name);
        let output = format!("{}\n", serde_json::to_string_pretty(&schema)?);
        if check {
            if fs::read_to_string(path)? != output {
                return Err("schema drift: run cargo xtask schemas".into());
            }
        } else {
            fs::create_dir_all(root.join("schemas"))?;
            fs::write(path, output)?;
        }
    }
    Ok(())
}

fn run() -> Result<(), Box<dyn Error>> {
    let args: Vec<_> = env::args().skip(1).collect();
    match args.as_slice() {
        [command] if command == "schemas" => schemas(false),
        [command, flag] if command == "schemas" && flag == "--check" => schemas(true),
        _ => Err("usage: cargo xtask schemas [--check]".into()),
    }
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{error}");
            ExitCode::FAILURE
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn schemas_detect_missing_drift_and_roundtrip() -> Result<(), Box<dyn Error>> {
        let root =
            PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../artifacts/schema-diagnostic");
        if root.exists() {
            fs::remove_dir_all(&root)?;
        }
        assert!(schemas_at(&root, true).is_err());
        schemas_at(&root, false)?;
        schemas_at(&root, true)?;
        fs::write(root.join("schemas/range-error.json"), b"{}")?;
        assert!(schemas_at(&root, true).is_err());
        schemas_at(&root, false)?;
        fs::write(root.join("schemas/memory.manifest.1.json"), b"{}")?;
        assert!(schemas_at(&root, true).is_err());
        fs::remove_dir_all(&root)?;
        Ok(())
    }
}
