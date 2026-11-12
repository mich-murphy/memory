//! Lists and deletes every object under one `rust-acceptance/skeleton-<run id>/` prefix (refuses
//! anything else), then relists. Prints keys and counts only: never an endpoint, a credential
//! or an error value (a transport error can carry the request URL).
use rusty_s3::{actions::ListObjectsV2, Bucket, Credentials, S3Action, UrlStyle};
use std::time::Duration;

const SIGN: Duration = Duration::from_secs(120);

fn die(what: &str) -> ! {
    eprintln!("{what}");
    std::process::exit(2)
}
fn env(name: &str) -> String {
    std::env::var(name).unwrap_or_else(|_| die(&format!("missing {name}")))
}
fn valid(prefix: &str) -> bool {
    prefix
        .strip_prefix("rust-acceptance/skeleton-")
        .and_then(|rest| rest.strip_suffix('/'))
        .is_some_and(|id| {
            !id.is_empty() && id.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-')
        })
}
fn list(agent: &ureq::Agent, bucket: &Bucket, cred: &Credentials, prefix: &str, requests: &mut usize) -> Vec<String> {
    let (mut keys, mut token) = (Vec::new(), None::<String>);
    loop {
        let mut action = bucket.list_objects_v2(Some(cred));
        action.with_prefix(prefix);
        if let Some(t) = &token {
            action.with_continuation_token(t.clone());
        }
        *requests += 1;
        let mut reply = agent
            .get(action.sign(SIGN).as_str())
            .call()
            .unwrap_or_else(|_| die("list request failed"));
        if reply.status().as_u16() != 200 {
            die(&format!("list status {}", reply.status().as_u16()));
        }
        let text = reply
            .body_mut()
            .read_to_string()
            .unwrap_or_else(|_| die("list body unreadable"));
        let page = ListObjectsV2::parse_response(&text).unwrap_or_else(|_| die("list reply unparsable"));
        keys.extend(page.contents.into_iter().map(|o| o.key));
        match page.next_continuation_token {
            Some(t) => token = Some(t),
            None => return keys,
        }
    }
}
fn main() {
    let mut args = std::env::args().skip(1);
    let mode = args.next().unwrap_or_default();
    let prefix = args.next().unwrap_or_default();
    if !matches!(mode.as_str(), "list" | "delete") || !valid(&prefix) {
        die("usage: skeleton-cleanup list|delete rust-acceptance/skeleton-<run id>/");
    }
    let endpoint = env("R2_ENDPOINT").parse().unwrap_or_else(|_| die("endpoint is not a URL"));
    let bucket = Bucket::new(endpoint, UrlStyle::Path, env("R2_BUCKET"), "auto".to_owned())
        .unwrap_or_else(|_| die("bucket rejected"));
    let cred = Credentials::new(env("R2_ACCESS_KEY_ID"), env("R2_SECRET_ACCESS_KEY"));
    let agent: ureq::Agent = ureq::Agent::config_builder()
        .http_status_as_error(false)
        .max_redirects(0)
        .proxy(None)
        .timeout_global(Some(Duration::from_secs(60)))
        .build()
        .into();
    let (mut lists, mut deletes) = (0usize, 0usize);
    let keys = list(&agent, &bucket, &cred, &prefix, &mut lists);
    for key in &keys {
        if !key.starts_with(&prefix) {
            die("listing returned a key outside the prefix");
        }
        println!("{{\"key\":\"{key}\"}}");
    }
    if mode == "delete" {
        for key in &keys {
            let url = bucket.delete_object(Some(&cred), key).sign(SIGN);
            let reply = agent
                .delete(url.as_str())
                .call()
                .unwrap_or_else(|_| die("delete request failed"));
            deletes += 1;
            if reply.status().as_u16() / 100 != 2 {
                die(&format!("delete status {}", reply.status().as_u16()));
            }
        }
    }
    let remaining = if mode == "delete" {
        list(&agent, &bucket, &cred, &prefix, &mut lists).len()
    } else {
        keys.len()
    };
    println!(
        "{{\"mode\":\"{mode}\",\"prefix\":\"{prefix}\",\"listed\":{},\"deleted\":{deletes},\"list_requests\":{lists},\"delete_requests\":{deletes},\"remaining_after\":{remaining}}}",
        keys.len()
    );
}
