"""Prove each inherited lint by matching its diagnostic, not any compiler failure."""
import json
import pathlib
import subprocess
import tomllib

root = pathlib.Path.cwd()
manifest = tomllib.loads((root / "Cargo.toml").read_text())
fixtures = {
    "clippy::unwrap_used": "pub fn violation() { let _ = Some(1).unwrap(); }",
    "clippy::expect_used": 'pub fn violation() { let _ = Some(1).expect("bad"); }',
    "clippy::panic": 'pub fn violation() { panic!("bad"); }',
    "clippy::indexing_slicing": "pub fn violation(x: &[u8], i: usize) -> u8 { x[i] }",
    "clippy::todo": "pub fn violation() { todo!(); }",
    "clippy::dbg_macro": "pub fn violation() { dbg!(1); }",
    "clippy::print_stdout": 'pub fn violation() { println!("bad"); }',
    "clippy::allow_attributes": "#[allow(dead_code)] pub fn violation() {}",
    "unsafe_code": "pub unsafe fn violation() {}",
    "missing_docs": "pub fn violation() {}",
    "clippy::missing_errors_doc": "/// Diagnostic.\npub fn violation() -> Result<(), ()> { Ok(()) }",
    "unfulfilled_lint_expectations": '#[expect(clippy::unwrap_used, reason = "deliberately stale")] pub fn violation() {}',
}
work = root / "artifacts/lint-check"
(work / "src").mkdir(parents=True, exist_ok=True)
lints = (root / "Cargo.toml").read_text().split("[workspace.lints.rust]", 1)[1]
lints = "[lints.rust]" + lints.replace("[workspace.lints.clippy]", "[lints.clippy]")
(work / "Cargo.toml").write_text('[package]\nname="lint-check"\nversion="0.0.0"\nedition="2024"\n[workspace]\n' + lints)
for code, fixture in fixtures.items():
    source = '//! Deliberate lint violation.\n' + fixture + '\n'
    (work / "src/lib.rs").write_text(source)
    run = subprocess.run(["cargo", "clippy", "--offline", "--manifest-path", str(work / "Cargo.toml"), "--message-format=json", "--", "-Dwarnings"], capture_output=True, text=True)
    diagnostics = []
    for line in run.stdout.splitlines():
        try:
            message = json.loads(line).get("message", {})
            diagnostics.append((message.get("code") or {}).get("code"))
        except (ValueError, AttributeError):
            pass
    if run.returncode == 0 or code not in diagnostics:
        raise SystemExit(f"lint proof failed for {code}, exit={run.returncode}: {run.stderr} {run.stdout}")
    print(f"proved {code}: compiler exit {run.returncode}, matching diagnostic")
# A safe control must compile under the identical table.
(work / "src/lib.rs").write_text('//! Safe diagnostic control.\n/// Identity.\npub fn identity(x: u8) -> u8 { x }\n')
subprocess.run(["cargo", "clippy", "--offline", "--manifest-path", str(work / "Cargo.toml"), "--", "-Dwarnings"], check=True)
print("safe control: exit 0")
