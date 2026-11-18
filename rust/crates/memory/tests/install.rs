//! `memory install` / `memory uninstall`: the ordered JSON type, the merge rules, the written
//! files and the real binary under a temporary `HOME`.
use memory::install::json::{self, Value};
use proptest::prelude::*;
use serde_json::Number;

type R<T = ()> = Result<T, String>;

fn parse(text: &str) -> R<Value> {
    json::parse(text).map_err(|error| error.to_string())
}

fn round_trips(text: &str) -> R {
    let out = json::to_text(&parse(text)?);
    if out == text {
        Ok(())
    } else {
        Err(format!("not identical:\n{text}\n--\n{out}"))
    }
}

#[test]
fn json_round_trip_nested_objects_and_arrays() -> R {
    round_trips(
        "{\n  \"b\": {\n    \"z\": [\n      1,\n      {\n        \"y\": null\n      }\n    ],\n    \"a\": true\n  },\n  \"a\": false\n}\n",
    )
}

#[test]
fn json_round_trip_empty_containers() -> R {
    round_trips("{\n  \"a\": [],\n  \"b\": {},\n  \"c\": [\n    []\n  ]\n}\n")?;
    round_trips("{}\n")
}

#[test]
fn json_round_trip_unicode_and_escapes() -> R {
    round_trips(
        "{\n  \"naïve ☃\": \"héllo 日本 😀\",\n  \"esc\": \"a\\\"b\\\\c\\n\\r\\t\\b\\f\\u0001\\u001f/\"\n}\n",
    )
}

#[test]
fn json_round_trip_numbers() -> R {
    round_trips(
        "{\n  \"i\": 0,\n  \"n\": -17,\n  \"big\": 18446744073709551615,\n  \"f\": 1.5,\n  \"g\": -0.25,\n  \"h\": 3.0\n}\n",
    )
}

#[test]
fn json_blank_is_empty_object() -> R {
    for text in ["", "  \n\t"] {
        if parse(text)? != Value::Object(Vec::new()) {
            return Err("blank is not an empty object".into());
        }
    }
    Ok(())
}

#[test]
fn json_duplicate_keys_refused() {
    for text in [
        "{\"a\":1,\"a\":2}",
        "{\"x\":{\"a\":1,\"b\":2,\"a\":3}}",
        "{\"x\":[{\"k\":1,\"k\":1}]}",
    ] {
        assert_eq!(
            json::parse(text),
            Err(json::JsonError::Unparsable),
            "{text}"
        );
    }
}

#[test]
fn json_non_object_and_garbage_refused() {
    for text in ["[]", "1", "\"s\"", "null", "true"] {
        assert_eq!(json::parse(text), Err(json::JsonError::NotObject), "{text}");
    }
    for text in ["{", "{} x", "{\"a\":}", "nope", "{\"a\":1,}"] {
        assert_eq!(
            json::parse(text),
            Err(json::JsonError::Unparsable),
            "{text}"
        );
    }
}

#[test]
fn json_key_order_kept() -> R {
    let Value::Object(members) = parse("{\"z\":1,\"a\":2,\"m\":3}")? else {
        return Err("not an object".into());
    };
    let keys: Vec<&str> = members.iter().map(|(key, _)| key.as_str()).collect();
    if keys == ["z", "a", "m"] {
        Ok(())
    } else {
        Err(format!("{keys:?}"))
    }
}

fn arb_text() -> impl Strategy<Value = String> {
    proptest::collection::vec(any::<char>(), 0..8).prop_map(String::from_iter)
}

fn arb_value() -> impl Strategy<Value = Value> {
    let leaf = prop_oneof![
        Just(Value::Null),
        any::<bool>().prop_map(Value::Bool),
        any::<i64>().prop_map(|n| Value::Number(n.into())),
        any::<u64>().prop_map(|n| Value::Number(n.into())),
        // Quarter steps are exact in binary; serde_json without `float_roundtrip` may misread
        // 17-digit floats, and the brief forbids a feature change.
        (-4_000_000_i32..4_000_000)
            .prop_map(|n| Number::from_f64(f64::from(n) / 4.0).map_or(Value::Null, Value::Number)),
        arb_text().prop_map(Value::String),
    ];
    leaf.prop_recursive(4, 48, 4, |inner| {
        prop_oneof![
            proptest::collection::vec(inner.clone(), 0..4).prop_map(Value::Array),
            proptest::collection::vec((arb_text(), inner), 0..4).prop_map(|members| {
                let mut seen = std::collections::HashSet::new();
                Value::Object(
                    members
                        .into_iter()
                        .filter(|(key, _)| seen.insert(key.clone()))
                        .collect(),
                )
            }),
        ]
    })
}

fn arb_document() -> impl Strategy<Value = Value> {
    proptest::collection::vec((arb_text(), arb_value()), 0..5).prop_map(|members| {
        let mut seen = std::collections::HashSet::new();
        Value::Object(
            members
                .into_iter()
                .filter(|(key, _)| seen.insert(key.clone()))
                .collect(),
        )
    })
}

proptest! {
    #[test]
    fn proptest_json_write_parse_write_is_stable(doc in arb_document()) {
        let text = json::to_text(&doc);
        let back = json::parse(&text).map_err(|e| TestCaseError::fail(e.to_string()))?;
        prop_assert_eq!(&back, &doc);
        prop_assert_eq!(json::to_text(&back), text);
    }
}
