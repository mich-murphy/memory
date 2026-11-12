p='rust/crates/memory-core/src/privacy/mod.rs'
s=open(p).read()
s=s.replace("pub struct AdmittedSource {\n    facts: ResolvedSourceFacts,","pub struct AdmittedSource {\n    facts: ResolvedSourceFacts,\n    /// Validated non-empty owner, proven present by admission.\n    owner: String,",1)
old="    if facts.owner.as_ref().is_none_or(String::is_empty) {\n        return Admission::Deny(Reason::UnknownOwner);\n    }\n    Admission::Allow(AdmittedSource {\n        binding: binding(context, &facts),\n        facts,\n    })"
assert old in s
s=s.replace(old,"    let Some(owner) = facts.owner.clone().filter(|o| !o.is_empty()) else {\n        return Admission::Deny(Reason::UnknownOwner);\n    };\n    Admission::Allow(AdmittedSource {\n        binding: binding(context, &facts),\n        owner,\n        facts,\n    })")
i=s.index("    match admit(context, source.facts.clone()) {")
j=s.index("\n}\n",i)
s=s[:i]+"""    match admit(context, source.facts.clone()) {
        Admission::Allow(_) => Authorization::Allow(AuthorizedBody {
            digest: Sha256::digest(&redacted.bytes).into(),
            bytes: redacted.bytes,
            owner: source.owner.clone(),
        }),
        Admission::Deny(reason) => Authorization::Deny(reason),
        Admission::Unknown(reason) => Authorization::Unknown(reason),
    }"""+s[j:]
open(p,'w').write(s)
