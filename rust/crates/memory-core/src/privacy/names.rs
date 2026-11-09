use regex::Regex;
use std::sync::OnceLock;

fn suffix() -> Option<&'static Regex> {
    static RE: OnceLock<Option<Regex>> = OnceLock::new();
    RE.get_or_init(|| Regex::new(r"(\.?~[0-9]+~|~|\.bak|\.backup|\.orig|\.old|-[0-9]{8}(-[0-9]{6})?|\.before-[a-z0-9][a-z0-9_.-]*|\.(gz|bz2|xz|zst|zstd|zip|7z|rar|tar|tgz)|\.agent-sessions-backup-[0-9-]+)$").ok()).as_ref()
}

/// Fold basename case and normalize repeated known backup suffixes.
pub fn basename(path: &str) -> String {
    let mut name = path.rsplit('/').next().unwrap_or_default().to_lowercase();
    if let Some(re) = suffix() {
        loop {
            let next = re.replace(&name, "").into_owned();
            if next == name {
                break;
            }
            name = next;
        }
    }
    name
}

/// Known credential identity predicate. The exact primary .claude.json is scrubbed.
pub fn is_credential(path: &str) -> bool {
    let name = basename(path);
    let primary = path.rsplit('/').next() == Some(".claude.json");
    let names = [
        ".credentials.json",
        "auth.json",
        "oauth.json",
        "mcp-auth.json",
        "config.env",
        ".git-credentials",
        ".netrc",
        ".npmrc",
        ".pypirc",
        "id_rsa",
        "id_dsa",
        "id_ecdsa",
        "id_ed25519",
        "id_ecdsa_sk",
        "id_ed25519_sk",
    ];
    names.contains(&name.as_str())
        || name.ends_with(".key")
        || name.ends_with(".pem")
        || name == ".env"
        || name.starts_with(".env.")
        || name.starts_with(".claude.json") && !primary
        || [
            "/.ssh",
            "/.gnupg",
            "/.aws",
            "/.azure",
            "/.kube",
            "/.config/gcloud",
        ]
        .iter()
        .any(|dir| path.ends_with(dir) || path.contains(&format!("{dir}/")))
}

pub(super) fn collector_match(path: &str, collector: &str) -> bool {
    path == collector
        || path.starts_with(&format!("{}/", collector.trim_end_matches('/')))
        || path
            .rsplit_once('/')
            .zip(collector.rsplit_once('/'))
            .is_some_and(|((parent, name), (other_parent, other_name))| {
                parent == other_parent && basename(name) == basename(other_name)
            })
}

/// Secret-name rules shared by argv, references and text guards.
pub fn is_secret_name(name: &str) -> bool {
    let name = name.rsplit('.').next().unwrap_or_default().to_lowercase();
    name.split(['-', '_'])
        .any(|s| ["token", "key", "auth"].contains(&s))
        || [
            "secret",
            "passw",
            "passphrase",
            "credential",
            "cookie",
            "apikey",
            "authorization",
        ]
        .iter()
        .any(|s| name.contains(s))
}

pub(super) fn matches(pattern: &str, value: &str) -> bool {
    Regex::new(pattern).is_ok_and(|re| re.is_match(value))
}

pub(super) fn reference(value: &str) -> bool {
    if ["", "[redacted]", "true", "false", "yes", "no", "on", "off"]
        .contains(&value.to_lowercase().as_str())
        || matches(r"^-?[0-9]+(\.[0-9]+)?$", value)
    {
        return true;
    }
    let Ok(re) = Regex::new(r"\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*") else {
        return false;
    };
    re.is_match(value)
        && re
            .replace_all(value, " ")
            .split_whitespace()
            .all(|word| ["bearer", "basic", "token"].contains(&word.to_lowercase().as_str()))
}
