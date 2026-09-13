use scout_agent::identity::load_or_create;

#[test]
fn identity_is_durable_and_public_key_matches_secret() {
    let directory = tempfile::tempdir().expect("temp directory");
    let first = load_or_create(&directory.path().join("identity.json")).expect("first identity");
    let second = load_or_create(&directory.path().join("identity.json")).expect("second identity");

    assert_eq!(first.public_key(), second.public_key());
    assert_eq!(first.public_key().len(), 43);
}
