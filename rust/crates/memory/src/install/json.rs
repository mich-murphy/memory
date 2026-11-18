//! A small ordered JSON type. Objects keep their file order, duplicate keys refuse the file,
//! numbers keep their exact text, and the writer prints 2-space indentation with a trailing
//! newline and non-ASCII text unescaped, the way `serde_json` pretty-prints, so a rewrite of a
//! file that was written that way is byte-identical.
//!
//! Parsing goes through `serde_json`'s `RawValue` (feature `raw_value`, already enabled): the
//! document is validated whole, then each value's raw text is split by its first byte. No
//! number is converted to a float, so no digit can change on a rewrite.
use serde::de::{self, Deserialize, Deserializer, MapAccess, Visitor};
use serde_json::value::RawValue;
use std::collections::HashSet;
use std::fmt;

/// An object: members in file order, keys unique.
pub type Object = Vec<(String, Value)>;

/// A JSON value with ordered objects.
#[derive(Debug, Clone, PartialEq)]
pub enum Value {
    /// `null`.
    Null,
    /// `true` or `false`.
    Bool(bool),
    /// A number, its exact text as validated by `serde_json`.
    Number(String),
    /// A string.
    String(String),
    /// An array.
    Array(Vec<Value>),
    /// An object in file order.
    Object(Object),
}

/// Why a document was refused. Closed; carries no file text.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum JsonError {
    /// Not JSON, or an object with a duplicate key.
    #[error("unparsable")]
    Unparsable,
    /// Valid JSON that is not an object.
    #[error("not_an_object")]
    NotObject,
}

/// An object's members as raw values, in file order; a duplicate key is an error.
struct Members<'de>(Vec<(String, &'de RawValue)>);

struct MembersVisitor;

impl<'de> Visitor<'de> for MembersVisitor {
    type Value = Members<'de>;

    fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("a JSON object")
    }

    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<Members<'de>, A::Error> {
        let mut members = Vec::new();
        let mut seen: HashSet<String> = HashSet::new();
        while let Some(key) = map.next_key::<String>()? {
            if !seen.insert(key.clone()) {
                return Err(de::Error::custom("duplicate key"));
            }
            members.push((key, map.next_value::<&'de RawValue>()?));
        }
        Ok(Members(members))
    }
}

impl<'de> Deserialize<'de> for Members<'de> {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        deserializer.deserialize_map(MembersVisitor)
    }
}

/// One value from its raw text, which `serde_json` has already validated as a whole document.
fn from_raw(text: &str) -> Result<Value, serde_json::Error> {
    let text = text.trim();
    Ok(match text.as_bytes().first() {
        Some(b'{') => {
            let Members(raw) = serde_json::from_str::<Members<'_>>(text)?;
            let mut members = Vec::with_capacity(raw.len());
            for (key, value) in raw {
                members.push((key, from_raw(value.get())?));
            }
            Value::Object(members)
        }
        Some(b'[') => {
            let raw = serde_json::from_str::<Vec<&RawValue>>(text)?;
            let mut items = Vec::with_capacity(raw.len());
            for value in raw {
                items.push(from_raw(value.get())?);
            }
            Value::Array(items)
        }
        Some(b'"') => Value::String(serde_json::from_str::<String>(text)?),
        Some(b't' | b'f') => Value::Bool(serde_json::from_str::<bool>(text)?),
        Some(b'n') => {
            serde_json::from_str::<()>(text)?;
            Value::Null
        }
        Some(b'-' | b'0'..=b'9') => {
            // Validates the number's syntax without converting it.
            serde_json::from_str::<&RawValue>(text)?;
            Value::Number(text.to_owned())
        }
        _ => return Err(de::Error::custom("not a JSON value")),
    })
}

/// Parse a document. Blank text is an empty object.
///
/// # Errors
/// [`JsonError::Unparsable`] for invalid JSON, trailing text or a duplicate key;
/// [`JsonError::NotObject`] when the top level is not an object.
pub fn parse(text: &str) -> Result<Value, JsonError> {
    if text.trim().is_empty() {
        return Ok(Value::Object(Vec::new()));
    }
    // The whole document first: syntax, trailing text and depth are judged once here.
    let whole = serde_json::from_str::<&RawValue>(text).map_err(|_| JsonError::Unparsable)?;
    match from_raw(whole.get()) {
        Ok(value @ Value::Object(_)) => Ok(value),
        Ok(_) => Err(JsonError::NotObject),
        Err(_) => Err(JsonError::Unparsable),
    }
}

/// Write a value with 2-space indentation and a trailing newline.
#[must_use]
pub fn to_text(value: &Value) -> String {
    let mut out = String::new();
    write_value(&mut out, value, 0);
    out.push('\n');
    out
}

fn indent(out: &mut String, depth: usize) {
    for _ in 0..depth {
        out.push_str("  ");
    }
}

fn write_value(out: &mut String, value: &Value, depth: usize) {
    match value {
        Value::Null => out.push_str("null"),
        Value::Bool(true) => out.push_str("true"),
        Value::Bool(false) => out.push_str("false"),
        Value::Number(number) => out.push_str(number),
        Value::String(text) => write_string(out, text),
        Value::Array(items) => {
            if items.is_empty() {
                out.push_str("[]");
                return;
            }
            out.push_str("[\n");
            for (index, item) in items.iter().enumerate() {
                if index > 0 {
                    out.push_str(",\n");
                }
                indent(out, depth + 1);
                write_value(out, item, depth + 1);
            }
            out.push('\n');
            indent(out, depth);
            out.push(']');
        }
        Value::Object(members) => {
            if members.is_empty() {
                out.push_str("{}");
                return;
            }
            out.push_str("{\n");
            for (index, (key, member)) in members.iter().enumerate() {
                if index > 0 {
                    out.push_str(",\n");
                }
                indent(out, depth + 1);
                write_string(out, key);
                out.push_str(": ");
                write_value(out, member, depth + 1);
            }
            out.push('\n');
            indent(out, depth);
            out.push('}');
        }
    }
}

/// `serde_json`'s escaping: `"` `\` and C0 controls only, lowercase `\u00xx`.
fn write_string(out: &mut String, text: &str) {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    out.push('"');
    for ch in text.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            c if u32::from(c) < 0x20 => {
                let code = usize::try_from(u32::from(c)).unwrap_or(0);
                out.push_str("\\u00");
                for nibble in [code >> 4, code & 0xf] {
                    out.push(char::from(HEX.get(nibble).copied().unwrap_or(b'0')));
                }
            }
            c => out.push(c),
        }
    }
    out.push('"');
}
