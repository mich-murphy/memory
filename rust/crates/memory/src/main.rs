//! Command entry point. `memory hook <agent>`, `memory export`, `memory owners` (list captured
//! owners), `memory status` (offline local health view), `memory unpark` (clear parked owners),
//! `memory reconcile` (one manual round), `memory install` / `memory uninstall` and
//! `memory daemon` (the run loop) are wired; everything else exits 78.
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
        Some(word) if word == "daemon" => memory::run::main_entry(),
        Some(word) if word == "install" => memory::install::main_entry(true, args),
        Some(word) if word == "uninstall" => memory::install::main_entry(false, args),
        Some(word) if word == "export" => memory::export::main_entry(args),
        Some(word) if word == "owners" => memory::owners::main_entry(args),
        Some(word) if word == "status" => memory::status::main_entry(args),
        Some(word) if word == "unpark" => memory::unpark::main_entry(args),
        Some(word) if word == "reconcile" => memory::reconcile::main_entry(args),
        // Fail explicitly rather than simulate successful capture.
        _ => ExitCode::from(78),
    }
}
