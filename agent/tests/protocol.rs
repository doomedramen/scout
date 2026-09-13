use scout_agent::protocol::{body_digest, signed_request_message};

#[test]
fn signed_request_canonicalization_is_stable() {
    assert_eq!(
        body_digest("{\"ok\":true}"),
        "QGLtr3UPuAdOfoPgyQKMlOMkaKi28WFHdDKO8EUVD5M"
    );
    assert_eq!(
        signed_request_message(
            "post",
            "/api/agent/v1/heartbeat",
            "1700000000",
            "request-1",
            "{\"ok\":true}"
        ),
        "POST\n/api/agent/v1/heartbeat\n1700000000\nrequest-1\nQGLtr3UPuAdOfoPgyQKMlOMkaKi28WFHdDKO8EUVD5M"
    );
}
