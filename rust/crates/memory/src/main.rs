//! Command entry point. `memory hook <agent>` and `memory daemon` (startup and refusal only,
//! until the real adapters exist) are wired; everything else exits 78.
use std::process::ExitCode;

fn main() -> ExitCode {
    let mut args = std::env::args_os().skip(1);
    match args.next() {
        Some(word) if word == "hook" => {
            let agent = args
                .next()
                .and_then(|word| word.into_string().ok())
                .unwrap_or_default();
            memory::hook::main_entry(&agent)
        }
        Some(word) if word == "daemon" => memory::daemon::main_entry(),
        // Fail explicitly rather than simulate successful capture or restore.
        _ => ExitCode::from(78),
    }
}
