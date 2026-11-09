use super::{Reason, names};

struct Word {
    raw: String,
    value: String,
    complete: bool,
}

// Bounded shell-like assignment guard, not an evaluator. Literal dollar signs
// become NUL (excluded from guarded input), so they cannot imitate references.
fn words(line: &str) -> Vec<Vec<Word>> {
    let mut chars = line.chars().peekable();
    let mut commands = Vec::new();
    let mut command = Vec::new();
    loop {
        while chars.peek().is_some_and(|c| *c == ' ' || *c == '\t') {
            chars.next();
        }
        let Some(first) = chars.peek().copied() else {
            break;
        };
        if first == '#' {
            break;
        }
        if ";|&".contains(first) {
            chars.next();
            commands.push(command);
            command = Vec::new();
            continue;
        }
        let mut raw = String::new();
        let mut value = String::new();
        let mut quoted = false;
        let mut complete = true;
        while let Some(c) = chars.peek().copied() {
            if !quoted && " \t;|&".contains(c) {
                break;
            }
            chars.next();
            raw.push(c);
            match c {
                '"' => quoted = !quoted,
                '\\' => match chars.peek().copied() {
                    Some(next) if !quoted || "\\\"$`".contains(next) => {
                        chars.next();
                        raw.push(next);
                        value.push(if next == '$' { '\0' } else { next });
                    }
                    Some(_) => value.push('\\'),
                    None => {
                        value.push('\\');
                        complete = false;
                    }
                },
                '\'' if !quoted => {
                    let mut closed = false;
                    for next in chars.by_ref() {
                        raw.push(next);
                        if next == '\'' {
                            closed = true;
                            break;
                        }
                        value.push(if next == '$' { '\0' } else { next });
                    }
                    if !closed {
                        complete = false;
                    }
                }
                _ => value.push(c),
            }
        }
        command.push(Word {
            raw,
            value,
            complete: complete && !quoted,
        });
    }
    commands.push(command);
    commands
}
fn literal(command: &[Word]) -> bool {
    let mut input = command.iter().peekable();
    if input
        .peek()
        .is_some_and(|w| w.raw == "and" || w.raw == "or")
    {
        input.next();
    }
    if input.peek().is_some_and(|w| w.complete && w.value == "set") {
        input.next();
        let (mut global, mut export) = (false, false);
        while let Some(word) = input.peek() {
            if !word.complete {
                return false;
            }
            match word.value.as_str() {
                "-g" | "--global" => global = true,
                "-x" | "--export" => export = true,
                "-gx" | "-xg" => {
                    global = true;
                    export = true;
                }
                "--" => {
                    input.next();
                    break;
                }
                s if !s.starts_with('-') => break,
                _ => return false,
            }
            input.next();
        }
        if !global || !export {
            return false;
        }
        let Some(name) = input.next() else {
            return false;
        };
        if !name.complete
            || !names::matches(r"^[A-Za-z_][A-Za-z0-9_]*$", &name.value)
            || !names::is_secret_name(&name.value)
        {
            return false;
        }
        let values: Vec<&Word> = input.collect();
        return !values.is_empty()
            && (values.iter().any(|v| !v.complete)
                || !names::reference(
                    &values
                        .iter()
                        .map(|v| v.value.as_str())
                        .collect::<Vec<_>>()
                        .join(" "),
                ));
    }
    if input
        .peek()
        .is_some_and(|w| w.complete && w.value == "export")
    {
        input.next();
    }
    for word in input {
        let Some((name, _)) = word.raw.split_once('=') else {
            break;
        };
        if !names::matches(r"^[A-Za-z_][A-Za-z0-9_]*$", name) {
            break;
        }
        let value = word.value.split_once('=').map_or("", |(_, v)| v);
        if names::is_secret_name(name) && (!word.complete || !names::reference(value)) {
            return true;
        }
    }
    false
}

pub(super) fn check(identities: &[String], bytes: &[u8]) -> Result<(), Reason> {
    if bytes.len() > 1024 * 1024 || bytes.contains(&0) {
        return Ok(());
    }
    let Ok(source) = std::str::from_utf8(bytes) else {
        return Ok(());
    };
    let extensions = [
        "py", "pyi", "rb", "js", "mjs", "cjs", "jsx", "ts", "mts", "cts", "tsx", "go", "rs",
        "java", "kt", "kts", "scala", "swift", "c", "h", "cc", "cpp", "hpp", "cs", "php", "lua",
        "r", "jl", "ex", "exs", "dart",
    ];
    if !identities.is_empty()
        && identities.iter().all(|p| {
            names::basename(p)
                .rsplit_once('.')
                .is_some_and(|(_, ext)| extensions.contains(&ext))
        })
    {
        return Ok(());
    }
    if source
        .lines()
        .any(|line| words(line.trim()).iter().any(|command| literal(command)))
    {
        return Err(Reason::SecretInText);
    }
    Ok(())
}
